import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'api.dart';
import 'diagnostics.dart';

/// One event from the instance (#419): what happened — `chat`, `task`,
/// `agent_status`, `approval`, `recording` — and to which agent. What it
/// means is read from the instance afterwards; the event only says to look.
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

  final _events = StreamController<LiveEvent>.broadcast();
  CoveyApi? _api;
  StreamSubscription<String>? _sub;
  Timer? _retry;
  var _backoff = 1;

  /// Whether the stream is open right now.
  bool connected = false;

  /// Events of the given types, optionally of one agent, gathered for
  /// [settle] so that a burst (a run's steps) causes one reload, not ten.
  Stream<void> of(Set<String> types, {String? agentId, Duration settle = const Duration(milliseconds: 600)}) {
    late StreamController<void> out;
    StreamSubscription<LiveEvent>? sub;
    Timer? pending;
    out = StreamController<void>(
      onListen: () {
        sub = _events.stream.listen((e) {
          if (!types.contains(e.type)) return;
          if (agentId != null && e.agentId != agentId) return;
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
    _retry?.cancel();
    _retry = null;
    unawaited(_sub?.cancel());
    _sub = null;
    connected = false;
  }

  Future<void> _connect() async {
    final api = _api;
    if (api == null) return;
    try {
      final res = await api.events();
      if (!identical(api, _api)) return;
      if (res.statusCode != 200) {
        diag('live', 'event stream refused: ${res.statusCode}');
        _reconnect(api);
        return;
      }
      connected = true;
      _backoff = 1;
      diag('live', 'event stream open');
      var type = 'message';
      final data = StringBuffer();
      _sub = res.stream
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .listen(
            (line) {
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
    if (!identical(api, _api)) return;
    _retry?.cancel();
    _retry = Timer(Duration(seconds: _backoff), () => unawaited(_connect()));
    _backoff = math.min(_backoff * 2, 30);
  }

  /// For tests: hands an event to the listeners as if it had come in.
  void inject(LiveEvent e) => _events.add(e);
}
