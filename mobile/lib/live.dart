import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/widgets.dart' show AppLifecycleListener;

import 'api.dart';
import 'diagnostics.dart';
import 'host.dart';

/// One event from the instance (#419): what happened — `chat`, `task`,
/// `agent_status`, `approval`, `roster` — and to which agent. What it means is read
/// from the instance afterwards; the event only says to look.
///
/// One type is the app's own: `resync` (#536), handed to every listener when
/// the stream has been opened again after a break. What happened while it
/// was down came with no event, so everyone looks once. It is also handed
/// out when the app comes back to the foreground (#447): the stream was
/// closed meanwhile.
class LiveEvent {
  const LiveEvent(this.type, this.agentId, this.data);
  final String type;
  final String agentId;
  final Map<String, dynamic> data;
}

/// The instance's event stream, held open while the app shows a connection
/// (#419). The app used to ask again every few seconds — the conversation
/// every 8, the list every 20, the Mac's notifications every 15 — and saw
/// what happened that long after. The instance already pushes: the web keeps
/// itself current with /api/v1/events, and a key may read it. So the app
/// listens there, and the timers stay only as a net under it.
class LiveEvents {
  LiveEvents._();

  static final instance = LiveEvents._();

  /// The event types the app reads; the stream is asked for these alone
  /// (#535). `recording` is not among them — a run publishes one per step,
  /// and unfiltered they filled the app's buffer at the instance and pushed
  /// out the `agent_status` or `chat` event it was waiting for.
  ///
  /// `roster` (#619) says that agents or departments changed: created,
  /// hired, renamed, moved, deleted.
  static const types = {'chat', 'task', 'agent_status', 'approval', 'roster'};

  /// The type of the event handed out when the stream is open again after a
  /// break (#536).
  static const resync = 'resync';

  /// Whether the stream has broken since it was last open — the next open
  /// then tells the listeners to look.
  var _broken = false;

  final _events = StreamController<LiveEvent>.broadcast();
  CoveyApi? _api;
  StreamSubscription<String>? _sub;
  Timer? _retry;
  var _backoff = 1;

  /// The keepalive the stream asks for (#447). The instance's default of
  /// 20 s woke a phone's radio three times a minute while the app sat idle;
  /// an instance older than the parameter keeps sending its 20 s.
  static const keepalive = Duration(seconds: 45);

  /// A stream silent for this long is dead — a proxy kept the connection
  /// after the instance behind it went away (#532) —, and is opened again.
  /// Two keepalives and some slack.
  static const silentFor = Duration(seconds: 100);

  /// Whether the app is in the background (#447): the stream is closed then,
  /// push carries what happens, and it is opened again on the way back.
  var _suspended = false;
  Timer? _watchdog;
  DateTime _heard = DateTime.now();

  /// Whether the stream is open right now. The screens' timers ask only
  /// while it is not (#447): they are the net under the stream, not a
  /// second channel beside it.
  bool connected = false;

  /// Events of the given types, optionally of one agent or of one
  /// conversation (#440: a chat event names it in its data), gathered for
  /// [settle] so that a burst (a run's steps) causes one reload, not ten.
  /// A burst that never pauses for [settle] still reloads [maxWait] after
  /// its first event (#447) — a busy run must not hold the screen still.
  ///
  /// A [resync] (#536) passes every filter: it says the stream was down and
  /// is back, and no listener knows what it missed. Pass `withResync: false`
  /// for a listener that must only react to a real event.
  Stream<void> of(
    Set<String> types, {
    String? agentId,
    String? conversationId,
    Duration settle = const Duration(milliseconds: 600),
    Duration maxWait = const Duration(seconds: 2),
    bool withResync = true,
  }) {
    late StreamController<void> out;
    StreamSubscription<LiveEvent>? sub;
    Timer? pending;
    Timer? cap;
    void fire() {
      pending?.cancel();
      cap?.cancel();
      pending = null;
      cap = null;
      out.add(null);
    }

    out = StreamController<void>(
      onListen: () {
        sub = _events.stream.listen((e) {
          if (e.type == resync) {
            if (!withResync) return;
          } else {
            if (!types.contains(e.type)) return;
            if (agentId != null && e.agentId != agentId) return;
            if (conversationId != null && e.data['conversation_id'] != conversationId) return;
          }
          pending?.cancel();
          pending = Timer(settle, fire);
          cap ??= Timer(maxWait < settle ? settle : maxWait, fire);
        });
      },
      onCancel: () {
        pending?.cancel();
        cap?.cancel();
        return sub?.cancel();
      },
    );
    return out.stream;
  }

  /// Listens to [api]'s instance; a new connection replaces the old one.
  void start(CoveyApi api) {
    if (identical(api, _api)) return;
    _api = null;
    _broken = false;
    _close();
    _api = api;
    _backoff = 1;
    if (!_suspended) unawaited(_connect());
  }

  /// The app went to the background (#447): the stream is closed. An open
  /// stream kept the radio busy with every keepalive, and push carries what
  /// happens meanwhile. The connection stays, for [resume].
  void suspend() {
    if (_suspended) return;
    _suspended = true;
    if (_api == null) return;
    diag('live', 'app in the background: event stream closed');
    _close();
    // What happens from now on comes with no event: on the way back,
    // everyone looks once.
    _broken = true;
  }

  /// The app is back in the foreground (#447): the stream is opened again at
  /// once, not after a watchdog's minute, and the [resync] that follows
  /// tells every screen to look once.
  void resume() {
    if (!_suspended) return;
    _suspended = false;
    final api = _api;
    if (api == null) return;
    diag('live', 'app in the foreground: event stream opened again');
    _close();
    _broken = true;
    _backoff = 1;
    unawaited(_connect());
  }

  /// Counts the closes: an open that was under way when the stream was
  /// closed — and maybe opened again — must not add a second stream.
  var _generation = 0;

  /// Ends the stream and its timers, keeping the connection it belongs to.
  void _close() {
    _generation++;
    _retry?.cancel();
    _retry = null;
    _watchdog?.cancel();
    _watchdog = null;
    unawaited(_sub?.cancel());
    _sub = null;
    connected = false;
  }

  /// Closes the stream if it belongs to [api] — a screen that goes does not
  /// close the stream of the connection that replaced it.
  void stopFor(CoveyApi api) {
    if (identical(api, _api)) stop();
  }

  void stop() {
    _api = null;
    _broken = false;
    // Whoever starts the next connection follows the app's state from there;
    // a screen that went while the app was away must not leave the next
    // stream shut.
    _suspended = false;
    _close();
  }

  /// Whether the stream closes while the app is in the background (#447):
  /// on a phone, where push carries what happens meanwhile and a keepalive
  /// costs the radio. Not on the Mac, whose notifications come from the
  /// app's own look, which the stream drives.
  static bool get closesInBackground => Host.isIOS || Host.isAndroid;

  /// Closes the stream when the app is hidden and opens it again when it is
  /// shown; the caller disposes the listener with the screen that holds the
  /// connection.
  AppLifecycleListener followLifecycle() => AppLifecycleListener(onHide: suspend, onShow: resume);

  Future<void> _connect() async {
    final api = _api;
    if (api == null || _suspended) return;
    final generation = _generation;
    try {
      final res = await api.events(types: types, keepalive: keepalive);
      if (!identical(api, _api) || _suspended || generation != _generation) {
        // Replaced, closed or sent to the background while it was being
        // opened.
        unawaited(res.stream.listen(null).cancel());
        return;
      }
      if (res.statusCode != 200) {
        diag('live', 'event stream refused: ${res.statusCode}');
        _reconnect(api);
        return;
      }
      connected = true;
      _backoff = 1;
      diag('live', 'event stream open');
      _heard = DateTime.now();
      if (_broken) {
        // Open again after a break (#536): whatever happened meanwhile came
        // with no event, so every listener looks once.
        _broken = false;
        diag('live', 'event stream open again: everyone looks once');
        _events.add(const LiveEvent(resync, '', {}));
      }
      _watchdog?.cancel();
      _watchdog = Timer.periodic(const Duration(seconds: 10), (_) {
        if (!identical(api, _api) || DateTime.now().difference(_heard) < silentFor) return;
        diag('live', 'event stream silent for ${silentFor.inSeconds} s: opening it again');
        unawaited(_sub?.cancel());
        _sub = null;
        _reconnect(api);
      });
      var type = 'message';
      final data = StringBuffer();
      _sub = res.stream
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .listen(
            (line) {
              _heard = DateTime.now();
              if (line.isEmpty) {
                _dispatch(type, data.toString());
                type = 'message';
                data.clear();
              } else if (line.startsWith(':')) {
                // keepalive
              } else if (line.startsWith('event:')) {
                type = line.substring(6).trim();
              } else if (line.startsWith('data:')) {
                if (data.isNotEmpty) data.write('\n');
                data.write(line.substring(5).trim());
              }
            },
            onDone: () => _reconnect(api),
            onError: (Object e) {
              diag('live', 'event stream broke: $e');
              _reconnect(api);
            },
            cancelOnError: true,
          );
    } catch (e) {
      diag('live', 'event stream not reached: $e');
      _reconnect(api);
    }
  }

  void _dispatch(String type, String raw) {
    if (type == 'hello' || type == 'message') return;
    var data = <String, dynamic>{};
    var agent = '';
    try {
      final j = jsonDecode(raw) as Map<String, dynamic>;
      agent = j['agent_id'] as String? ?? '';
      if (j['data'] is Map) data = (j['data'] as Map).cast<String, dynamic>();
    } catch (_) {}
    _events.add(LiveEvent(type, agent, data));
  }

  /// Tries again after a pause that doubles up to half a minute: a server
  /// restarting for a deploy is back in seconds, one that is gone is not
  /// asked every second.
  void _reconnect(CoveyApi api) {
    connected = false;
    _watchdog?.cancel();
    _watchdog = null;
    if (!identical(api, _api) || _suspended) return;
    _broken = true;
    _retry?.cancel();
    _retry = Timer(Duration(seconds: _backoff), () => unawaited(_connect()));
    _backoff = math.min(_backoff * 2, 30);
  }

  /// For tests: hands an event to the listeners as if it had come in.
  void inject(LiveEvent e) => _events.add(e);
}

/// Lets only the newest of overlapping reads land (#619). An event, a resync
/// and a timer can each start a read of the same list; whichever answer comes
/// back last used to win, so a slow, older read could put back what a newer
/// one had already replaced. Each read takes a ticket with [begin], and
/// applies its answer only while [isCurrent] says no later read has started.
class Latest {
  var _n = 0;

  /// Starts a read and returns its ticket.
  int begin() => ++_n;

  /// Whether the read holding [ticket] is still the newest.
  bool isCurrent(int ticket) => ticket == _n;
}
