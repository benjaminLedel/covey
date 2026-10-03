import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'api.dart';
import 'diagnostics.dart';

/// One event from the instance (#419): what happened — `chat`, `task`,
/// `agent_status`, `approval` — and to which agent. What it means is read
/// from the instance afterwards; the event only says to look.
///
/// One type is the app's own: `resync` (#536), handed to every listener when
/// the stream has been opened again after a break. What happened while it
/// was down came with no event, so everyone looks once.
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
  static const types = {'chat', 'task', 'agent_status', 'approval'};

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

  /// The instance sends a keepalive every 20 s (#532): a stream silent for
  /// [silentFor] is dead — a proxy kept the connection after the instance
  /// behind it went away —, and is opened again.
  static const silentFor = Duration(seconds: 50);
  Timer? _watchdog;
  DateTime _heard = DateTime.now();

  /// Whether the stream is open right now.
  bool connected = false;

  /// Events of the given types, optionally of one agent or of one
  /// conversation (#440: a chat event names it in its data), gathered for
  /// [settle] so that a burst (a run's steps) causes one reload, not ten.
  ///
  /// A [resync] (#536) passes every filter: it says the stream was down and
  /// is back, and no listener knows what it missed. Pass `withResync: false`
  /// for a listener that must only react to a real event.
  Stream<void> of(
    Set<String> types, {
    String? agentId,
    String? conversationId,
    Duration settle = const Duration(milliseconds: 600),
    bool withResync = true,
  }) {
    late StreamController<void> out;
    StreamSubscription<LiveEvent>? sub;
    Timer? pending;
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
          pending = Timer(settle, () => out.add(null));
        });
      },
      onCancel: () {
        pending?.cancel();
        return sub?.cancel();
      },
    );
    return out.stream;
  }

  /// Listens to [api]'s instance; a new connection replaces the old one.
  void start(CoveyApi api) {
    if (identical(api, _api)) return;
    stop();
    _api = api;
    _backoff = 1;
    unawaited(_connect());
  }

  /// Closes the stream if it belongs to [api] — a screen that goes does not
  /// close the stream of the connection that replaced it.
  void stopFor(CoveyApi api) {
    if (identical(api, _api)) stop();
  }

  void stop() {
    _api = null;
    _broken = false;
    _retry?.cancel();
    _retry = null;
    _watchdog?.cancel();
    _watchdog = null;
    unawaited(_sub?.cancel());
    _sub = null;
    connected = false;
  }

  Future<void> _connect() async {
    final api = _api;
    if (api == null) return;
    try {
      final res = await api.events(types: types);
      if (!identical(api, _api)) return;
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
    if (!identical(api, _api)) return;
    _broken = true;
    _retry?.cancel();
    _retry = Timer(Duration(seconds: _backoff), () => unawaited(_connect()));
    _backoff = math.min(_backoff * 2, 30);
  }

  /// For tests: hands an event to the listeners as if it had come in.
  void inject(LiveEvent e) => _events.add(e);
}
