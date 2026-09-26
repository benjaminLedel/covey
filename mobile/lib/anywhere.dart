import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart' show AppLifecycleListener;
import 'package:hotkey_manager/hotkey_manager.dart';

import 'api.dart';
import 'diagnostics.dart';
import 'dictation.dart';
import 'speech_model.dart';
import 'prefs.dart';

/// Dictate anywhere (#355): a global shortcut on the Mac starts dictation in
/// whatever app has the focus, and the text lands where the cursor is.
///
/// ⌥ Space held is push-to-talk — released, the text goes in. A short tap
/// is hands-free: it listens until the next tap. While it listens a small
/// panel at the bottom of the screen shows the waveform and the words
/// (native, MainFlutterWindow.swift); it never takes the focus.
///
/// Recognition runs on the Mac with the model picked in settings, kept
/// loaded between dictations so a press starts listening at once. The
/// recognised text is then cleaned up by the instance when that is switched
/// on and possible — one fast turn, text only — and put in with ⌘V.
class DictateAnywhere extends ChangeNotifier {
  DictateAnywhere._();

  static final DictateAnywhere instance = DictateAnywhere._();

  /// Only the Mac for now; Windows and Linux would need their own insertion.
  static bool get supported => Platform.isMacOS;

  static const _channel = MethodChannel('covey/flow');
  static const _enabledKey = 'flow.enabled';
  static const _cleanKey = 'flow.clean';
  final _prefs = Prefs.instance;

  final hotKey = HotKey(key: PhysicalKeyboardKey.space, modifiers: [HotKeyModifier.alt], scope: HotKeyScope.system);

  bool enabled = false;
  bool clean = true;

  /// Whether the instance can clean up (it has a model credential).
  bool cleanAvailable = false;

  /// Whether macOS lets the app type into other apps.
  bool trusted = false;

  /// Strings for the panel, in the app's language; set by the UI.
  String listening = 'Listening …';
  String cleaning = 'Cleaning up …';
  String loading = 'Loading the speech model …';

  CoveyApi? _api;
  Dictation? _dictation;
  bool _registered = false;
  AppLifecycleListener? _lifecycle;
  Timer? _trustPoll;

  // One dictation's state.
  DateTime? _pressedAt;
  bool _handsFree = false;
  bool _busy = false;
  Future<bool>? _starting;
  String? _targetApp;
  DateTime _lastPush = DateTime.fromMillisecondsSinceEpoch(0);

  /// Connects to the instance the app is signed in to; registers the
  /// shortcut when dictate-anywhere is on.
  Future<void> attach(CoveyApi api) async {
    if (!supported) return;
    _api = api;
    _dictation?.dispose();
    _dictation = Dictation(api: api, keepModelLoaded: true)..addListener(_push);
    try {
      enabled = await _prefs.read(_enabledKey) == 'on';
      clean = await _prefs.read(_cleanKey) != 'off';
    } catch (_) {
      // Unreadable: the defaults.
    }
    await refresh();
    if (enabled) await _register();
    // The permission is granted in System Settings, outside the app: asked
    // again whenever the app comes back to the front.
    _lifecycle ??= AppLifecycleListener(onResume: () => unawaited(refresh()));
    notifyListeners();
  }

  /// Rereads what can change outside the app: the Accessibility permission
  /// and whether the instance can clean up.
  Future<void> refresh() async {
    try {
      trusted = await _channel.invokeMethod<bool>('trusted') ?? false;
    } on PlatformException {
      trusted = false;
    }
    final api = _api;
    if (api != null) {
      try {
        cleanAvailable = (await api.speechModel()).clean;
      } on ApiException {
        cleanAvailable = false;
      }
    }
    notifyListeners();
  }

  Future<void> setEnabled(bool on) async {
    enabled = on;
    if (on) {
      await _register();
      if (!trusted) await askTrust();
    } else {
      await _unregister();
    }
    notifyListeners();
    await _save(_enabledKey, on ? 'on' : 'off');
  }

  Future<void> setClean(bool on) async {
    clean = on;
    notifyListeners();
    await _save(_cleanKey, on ? 'on' : 'off');
  }

  /// Asks macOS for the Accessibility permission: the system's own prompt,
  /// then its settings pane, where the switch is.
  Future<void> askTrust() async {
    try {
      trusted = await _channel.invokeMethod<bool>('askTrust') ?? false;
      diag('flow', 'accessibility asked: ${trusted ? 'granted' : 'not granted'}');
      if (!trusted) await _channel.invokeMethod<void>('openAccessibilitySettings');
    } on PlatformException catch (e) {
      diag('flow', 'accessibility: ${e.message}');
    }
    notifyListeners();
    // Then watched for two minutes: the switch is flipped in System
    // Settings while the app waits in the background.
    _trustPoll?.cancel();
    var left = 60;
    _trustPoll = Timer.periodic(const Duration(seconds: 2), (t) async {
      if (--left <= 0 || trusted) {
        t.cancel();
        return;
      }
      final now = await _channel.invokeMethod<bool>('trusted') ?? false;
      if (now != trusted) {
        trusted = now;
        diag('flow', 'accessibility now ${now ? 'granted' : 'not granted'}');
        notifyListeners();
      }
    });
  }

  Future<void> _save(String key, String value) async {
    try {
      await _prefs.write(key, value);
    } catch (_) {
      // Kept for this run.
    }
  }

  Future<void> _register() async {
    if (_registered) return;
    try {
      await hotKeyManager.register(hotKey, keyDownHandler: (_) => _down(), keyUpHandler: (_) => _up());
      _registered = true;
      diag('flow', 'shortcut registered');
    } catch (e) {
      diag('flow', 'shortcut not registered: $e');
    }
  }

  Future<void> _unregister() async {
    if (!_registered) return;
    await hotKeyManager.unregister(hotKey);
    _registered = false;
  }

  void _down() {
    final d = _dictation;
    if (d == null || _busy) return;
    if (d.running || _starting != null) {
      // The second tap of a hands-free dictation.
      if (_handsFree) unawaited(_finish());
      return;
    }
    _pressedAt = DateTime.now();
    _handsFree = false;
    unawaited(_start(d));
  }

  void _up() {
    final at = _pressedAt;
    if (at == null || _handsFree || _busy) return;
    // A tap, not a hold: hands-free until the next tap.
    if (DateTime.now().difference(at) < const Duration(milliseconds: 350)) {
      _handsFree = true;
      diag('flow', 'hands-free');
      return;
    }
    unawaited(_finish());
  }

  Future<void> _start(Dictation d) async {
    try {
      _targetApp = await _channel.invokeMethod<String>('frontmostApp');
    } on PlatformException {
      _targetApp = null;
    }
    diag('flow', 'start');
    await _channel.invokeMethod<void>('show');
    _push(force: true);
    d.language = SpeechModel.instance.language ?? d.language;
    final starting = d.start();
    _starting = starting;
    final ok = await starting;
    _starting = null;
    if (!ok) {
      diag('flow', 'dictation did not start: ${d.failure?.name} ${d.detail ?? ''}');
      await _channel.invokeMethod<void>('update', {'text': d.detail ?? d.failure?.name ?? '', 'levels': <double>[]});
      await Future<void>.delayed(const Duration(seconds: 2));
      await _channel.invokeMethod<void>('hide');
      _pressedAt = null;
    }
  }

  Future<void> _finish() async {
    final d = _dictation;
    final api = _api;
    if (d == null || api == null || _busy) return;
    _busy = true;
    try {
      // A release while the model was still loading: let it start first.
      await _starting;
      var text = (await d.stop()).trim();
      if (text.isEmpty) {
        diag('flow', 'nothing recognised');
        return;
      }
      if (clean && cleanAvailable) {
        await _channel.invokeMethod<void>('update', {'text': cleaning, 'levels': <double>[], 'busy': true});
        final watch = Stopwatch()..start();
        try {
          text = await api.cleanDictation(text, app: _targetApp);
          diag('flow', 'cleaned in ${watch.elapsedMilliseconds} ms');
        } on ApiException catch (e) {
          // The raw text is better than none.
          diag('flow', 'cleanup failed, inserting as recognised: ${e.message}');
        }
      }
      final ok = await _channel.invokeMethod<bool>('insert', {'text': text}) ?? false;
      diag('flow', ok ? 'inserted ${text.length} characters' : 'not inserted: no Accessibility permission');
      if (!ok) {
        trusted = false;
        notifyListeners();
      }
    } finally {
      await _channel.invokeMethod<void>('hide');
      _busy = false;
      _pressedAt = null;
      _handsFree = false;
    }
  }

  /// The panel follows the dictation: the words, or the model's download,
  /// and the waveform — at most twenty times a second.
  void _push({bool force = false}) {
    final d = _dictation;
    if (d == null || _busy) return;
    final now = DateTime.now();
    if (!force && now.difference(_lastPush) < const Duration(milliseconds: 50)) return;
    _lastPush = now;
    final text = d.preparing
        ? loading
        : d.text.isEmpty
        ? listening
        : d.text;
    final levels = d.levels.length > 40 ? d.levels.sublist(d.levels.length - 40) : d.levels;
    unawaited(
      _channel.invokeMethod<void>('update', {
        'text': text.length > 160 ? '…${text.substring(text.length - 160)}' : text,
        'levels': levels,
        'busy': d.preparing,
      }),
    );
  }

  Future<void> detach() async {
    _trustPoll?.cancel();
    _lifecycle?.dispose();
    _lifecycle = null;
    await _unregister();
    _dictation?.dispose();
    _dictation = null;
    _api = null;
  }
}
