import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import 'api.dart';
import 'diagnostics.dart';
import 'i18n.dart';
import 'models.dart';
import 'prefs.dart';

/// Notifications (#379). On the iPhone they come from Apple's push service:
/// the app asks for permission, hands its device token to the instance, and
/// the instance (or the relay that holds the app's key) sends. On the Mac the
/// app runs anyway — it keeps the dictation shortcut — so it looks at the
/// unread markers itself and shows local notifications; nothing leaves the
/// machine for it.
///
/// Either way a tap opens the thread: [opens] carries the agent's id.
class PushNotices {
  PushNotices._();

  static final instance = PushNotices._();

  static const _channel = MethodChannel('covey/push');
  static const _prefOff = 'push.off';
  static const _prefToken = 'push.token';
  static const _prefSound = 'push.sound';

  /// The sounds a person can choose (#381): covey's three families, the
  /// system's sound, none.
  static const sounds = ['bot', 'schar', 'glas', 'system', 'none'];

  final _opens = StreamController<String>.broadcast();

  /// The agents whose notification was tapped.
  Stream<String> get opens => _opens.stream;

  static bool get supported => Platform.isIOS || Platform.isMacOS;

  CoveyApi? _api;
  Strings? _strings;
  Timer? _poll;
  Map<String, ThreadState>? _last;
  Map<String, String> _names = {};
  DateTime _namesAt = DateTime(0);
  bool _listening = false;

  Future<String> get sound async {
    final s = await Prefs.instance.read(_prefSound);
    return sounds.contains(s) ? s! : 'bot';
  }

  /// Chooses the sound, plays it once, and tells the instance, whose pushes
  /// carry it.
  Future<void> setSound(String sound) async {
    await Prefs.instance.write(_prefSound, sound);
    unawaited(preview(sound));
    if (await enabled && Platform.isIOS) await _switchOn();
  }

  /// The file a notification of [kind] plays with [sound].
  static String fileFor(String sound, String kind) => switch (sound) {
    'system' => 'default',
    'none' => '',
    _ => 'covey-$sound-${const {'question', 'answer', 'result', 'error'}.contains(kind) ? kind : 'answer'}.caf',
  };

  /// Plays what a question sounds like — the kind that matters most.
  Future<void> preview(String sound) async {
    if (sound == 'none') return;
    try {
      await _channel.invokeMethod<void>('preview', fileFor(sound, 'question'));
    } on PlatformException catch (_) {
    } on MissingPluginException catch (_) {}
  }

  /// Whether the person wants notifications; on unless switched off.
  Future<bool> get enabled async => await Prefs.instance.read(_prefOff) != 'true';

  /// Starts for the connected instance: listens for taps, and unless the
  /// person switched notifications off, registers (iPhone) or starts
  /// watching (Mac).
  Future<void> start(CoveyApi api, Strings strings) async {
    if (!supported) return;
    _api = api;
    _strings = strings;
    if (!_listening) {
      _listening = true;
      _channel.setMethodCallHandler((call) async {
        if (call.method == 'open' && call.arguments is String) _opens.add(call.arguments as String);
      });
      try {
        final agent = await _channel.invokeMethod<String>('launchAgent');
        if (agent != null) _opens.add(agent);
      } on PlatformException catch (_) {
      } on MissingPluginException catch (_) {}
    }
    if (await enabled) await _switchOn();
  }

  Future<void> setEnabled(bool on) async {
    await Prefs.instance.write(_prefOff, on ? null : 'true');
    if (on) {
      await _switchOn();
    } else {
      await _switchOff();
    }
  }

  Future<void> _switchOn() async {
    final api = _api;
    if (api == null) return;
    try {
      if (Platform.isIOS) {
        final token = await _channel.invokeMethod<String>('register');
        if (token == null) return;
        await api.registerPushDevice(
          token: token,
          platform: 'ios',
          // A build from Xcode or `flutter run` talks to Apple's sandbox; one
          // from the store or TestFlight to production.
          environment: kReleaseMode ? 'production' : 'development',
          lang: _strings?.language ?? 'en',
          sound: await sound,
        );
        await Prefs.instance.write(_prefToken, token);
        diag('push', 'registered');
      } else {
        final allowed = await _channel.invokeMethod<Object?>('authorize');
        diag('push', 'mac notifications: $allowed');
        _poll?.cancel();
        _poll = Timer.periodic(const Duration(seconds: 30), (_) => _look());
        unawaited(_look());
      }
    } on PlatformException catch (e) {
      diag('push', 'not on: ${e.code} ${e.message ?? ''}');
    } on MissingPluginException catch (_) {
    } on ApiException catch (e) {
      diag('push', 'instance refused the device: ${e.status}');
    }
  }

  Future<void> _switchOff() async {
    _poll?.cancel();
    _poll = null;
    _last = null;
    final token = await Prefs.instance.read(_prefToken);
    if (token != null) {
      try {
        await _api?.unregisterPushDevice(token);
      } on ApiException catch (_) {}
      await Prefs.instance.write(_prefToken, null);
    }
  }

  /// The app icon's number: what the person has not read.
  Future<void> badge(int unread) async {
    if (!supported) return;
    try {
      await _channel.invokeMethod<void>('badge', unread);
    } on PlatformException catch (_) {
    } on MissingPluginException catch (_) {}
  }

  /// The Mac's round: what has become unread since the last look is shown.
  /// The first look only takes stock — what was unread before the app
  /// started is not news.
  Future<void> _look() async {
    final api = _api;
    if (api == null) return;
    try {
      final now = await api.threads();
      if (DateTime.now().difference(_namesAt) > const Duration(minutes: 10)) {
        _names = {for (final a in await api.agents()) a.id: a.displayName};
        _namesAt = DateTime.now();
      }
      final last = _last;
      _last = now;
      await badge(now.values.fold<int>(0, (n, t) => n + t.unread));
      if (last == null) return;
      for (final t in now.values) {
        final before = last[t.agentId];
        final fresh = t.unread > 0 && (before == null || (t.lastAt != null && t.lastAt != before.lastAt));
        if (!fresh) continue;
        final name = _names[t.agentId] ?? '';
        final kind = t.lastKind == 'note' ? 'answer' : t.lastKind;
        final failed = await _channel.invokeMethod<String?>('notify', {
          'sound': fileFor(await sound, kind),
          'id': '${t.agentId}-${t.lastAt?.millisecondsSinceEpoch}',
          'title': name,
          'body': t.lastText.isNotEmpty ? t.lastText : _strings?.t('team.ungelesen', count: t.unread) ?? '',
          'agent': t.agentId,
        });
        diag('push', failed == null ? 'shown, sound ${fileFor(await sound, kind)}' : 'not shown: $failed');
      }
    } catch (e) {
      diag('push', 'look failed: $e');
    }
  }
}
