import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

import 'api.dart';
import 'live.dart';
import 'diagnostics.dart';
import 'face.dart';
import 'i18n.dart';
import 'models.dart';
import 'prefs.dart';

/// Notifications (#379). On the iPhone and on Android they come through
/// Firebase Cloud Messaging (#424, #431), which passes an iPhone's on to
/// Apple: the app asks for permission, hands its FCM token to the instance,
/// and the instance (or the relay that holds the app's service account)
/// sends. On the Mac the
/// app runs anyway — it keeps the dictation shortcut — so it looks at the
/// unread markers itself and shows local notifications; nothing leaves the
/// machine for it.
///
/// Either way a tap opens the thread: [opens] carries the agent's id — or,
/// for a conversation that is no agent's thread (#440), `conversation:<id>`;
/// [linkFor] makes the link the app opens of it.
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

  /// The agents (or `conversation:<id>`) whose notification was tapped.
  Stream<String> get opens => _opens.stream;

  /// The link a tapped notification opens: the agent's thread, or the
  /// conversation it names.
  static Uri linkFor(String target) => target.startsWith('conversation:')
      ? Uri.parse('covey://team/c/${target.substring('conversation:'.length)}')
      : Uri.parse('covey://team/$target');

  static bool get supported => Platform.isIOS || Platform.isAndroid || Platform.isMacOS;

  /// Whether notifications come through a push service, with a token the
  /// instance keeps, rather than from the app's own look.
  static bool get _pushed => Platform.isIOS || Platform.isAndroid;

  CoveyApi? _api;
  Strings? _strings;
  Timer? _poll;
  StreamSubscription<void>? _live;

  /// What was last seen of each conversation — the newest entry's time —
  /// for the connection being watched; null until read.
  Map<String, DateTime?>? _seen;
  Map<String, Agent> _agents = {};
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
    if (await enabled && _pushed) await _switchOn();
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
  /// person switched notifications off, registers (iPhone, Android) or
  /// starts watching (Mac).
  Future<void> start(CoveyApi api, Strings strings) async {
    if (!supported) return;
    if (!identical(_api, api)) _seen = null;
    _api = api;
    _strings = strings;
    if (!_listening) {
      _listening = true;
      _channel.setMethodCallHandler((call) async {
        if (call.method == 'open' && call.arguments is String) _opens.add(call.arguments as String);
        // The iPhone's token was replaced (#431): register the new one.
        if (call.method == 'token' && await enabled) await _switchOn();
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
      if (_pushed) {
        // A build without the app's Firebase configuration answers
        // "unavailable": that build has no push.
        final token = await _channel.invokeMethod<String>('register');
        if (token == null) return;
        await api.registerPushDevice(
          token: token,
          platform: Platform.isAndroid ? 'android' : 'ios',
          // FCM has no sandbox: for an iPhone it picks Apple's environment
          // itself, from the APNs key uploaded to the Firebase project.
          environment: 'production',
          lang: _strings?.language ?? 'en',
          sound: await sound,
        );
        await Prefs.instance.write(_prefToken, token);
        diag('push', 'registered');
      } else {
        final allowed = await _channel.invokeMethod<Object?>('authorize');
        diag('push', 'mac notifications: $allowed');
        // At once on an answer or a task that moved (#419); the timer is the
        // net under the event stream.
        await _live?.cancel();
        _live = LiveEvents.instance.of({'chat', 'task'}).listen((_) => _look());
        _poll?.cancel();
        _poll = Timer.periodic(const Duration(minutes: 1), (_) => _look());
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
    await _live?.cancel();
    _live = null;
    _seen = null;
    if (Platform.isMacOS) {
      try {
        await _channel.invokeMethod<void>('stopWatching');
      } on PlatformException catch (_) {
      } on MissingPluginException catch (_) {}
    }
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
  ///
  /// "The last look" survives a start (#418): the newest entry seen of each
  /// conversation is kept per connection, so what arrived while the app was
  /// quitting or restarting is announced when it is back. Only the very first
  /// look on a connection takes stock — what was unread before the app ever
  /// watched is not news.
  Future<void> _look() async {
    final api = _api;
    if (api == null) return;
    try {
      final now = await api.threads();
      if (DateTime.now().difference(_namesAt) > const Duration(minutes: 10)) {
        _agents = {for (final a in await api.agents()) a.id: a};
        _namesAt = DateTime.now();
      }
      final seen = _seen ?? await _loadSeen(api);
      _seen = {for (final t in now.values) t.agentId: t.lastAt};
      await _saveSeen(api, _seen!);
      await badge(now.values.fold<int>(0, (n, t) => n + t.unread));
      for (final t in toAnnounce(seen, now.values)) {
        final agent = _agents[t.agentId];
        final name = agent?.displayName ?? '';
        final kind = t.lastKind == 'note' ? 'answer' : t.lastKind;
        final failed = await _channel.invokeMethod<String?>('notify', {
          'sound': fileFor(await sound, kind),
          'id': '${t.agentId}-${t.lastAt?.millisecondsSinceEpoch}',
          'title': name,
          'body': t.lastText.isNotEmpty ? t.lastText : _strings?.t('team.ungelesen', count: t.unread) ?? '',
          'agent': t.agentId,
          // The face as the sender's picture (#420), and as the attachment
          // where communication notifications are not available — two files,
          // since the system moves an attachment into its store.
          if (agent != null) 'face': await _faceFile(agent),
          if (agent != null) 'image': await _faceFile(agent),
        });
        diag('push', failed == null ? 'shown, sound ${fileFor(await sound, kind)}' : 'not shown: $failed');
      }
    } catch (e) {
      diag('push', 'look failed: $e');
    }
  }

  /// What a look announces: the conversations with something unread that is
  /// newer than what was last seen of them. Nothing on the very first look of
  /// a connection ([seen] null) — what was unread before is not news.
  @visibleForTesting
  static List<ThreadState> toAnnounce(Map<String, DateTime?>? seen, Iterable<ThreadState> now) {
    if (seen == null) return const [];
    return [
      for (final t in now)
        if (t.unread > 0 &&
            t.lastAt != null &&
            (!seen.containsKey(t.agentId) || seen[t.agentId] == null || t.lastAt!.isAfter(seen[t.agentId]!)))
          t,
    ];
  }

  /// Where the last look of a connection is kept: per connection, under a
  /// fingerprint — the key itself stays in the keychain.
  static String _seenKey(CoveyApi api) => 'push.seen.${api.fingerprint}';

  Future<Map<String, DateTime?>?> _loadSeen(CoveyApi api) async {
    final raw = await Prefs.instance.read(_seenKey(api));
    if (raw == null) return null;
    try {
      return (jsonDecode(raw) as Map<String, dynamic>).map(
        (k, v) => MapEntry(k, v == null ? null : DateTime.tryParse(v as String)),
      );
    } on FormatException {
      return null;
    }
  }

  Future<void> _saveSeen(CoveyApi api, Map<String, DateTime?> seen) =>
      Prefs.instance.write(_seenKey(api), jsonEncode(seen.map((k, v) => MapEntry(k, v?.toUtc().toIso8601String()))));

  /// The agent's face as a file for the notification's picture (#420). A new
  /// file each time: the system moves an attachment into its own store.
  /// Empty when it could not be drawn — the notification goes out without.
  Future<String> _faceFile(Agent a) async {
    try {
      final png = await facePng(
        a.slug,
        state: faceStateOf(killed: a.killed, status: a.status),
      );
      final dir = Directory('${(await getTemporaryDirectory()).path}/covey-faces');
      await dir.create(recursive: true);
      final f = File('${dir.path}/${a.slug}-${DateTime.now().microsecondsSinceEpoch}.png');
      await f.writeAsBytes(png);
      return f.path;
    } catch (e) {
      diag('push', 'face not drawn: $e');
      return '';
    }
  }
}
