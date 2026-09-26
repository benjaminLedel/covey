import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import 'anywhere.dart';
import 'api.dart';
import 'diagnostics.dart';
import 'models.dart';
import 'prefs.dart';

/// The activity log on the Mac (#363): switched on by the person, it samples
/// what is in front every five seconds — app, window, page, the focused field
/// and an excerpt of it — and merges the samples into sessions, which go to
/// the instance, private to the seat, every two minutes. Never keystrokes,
/// never screenshots, never secure fields or password managers (the native
/// side leaves those out). A menu-bar item shows while it records.
class ActivityRecorder extends ChangeNotifier {
  ActivityRecorder._();

  static final ActivityRecorder instance = ActivityRecorder._();

  static bool get supported => Platform.isMacOS;

  static const _channel = MethodChannel('covey/flow');
  static const _enabledKey = 'activity.enabled';
  static const _sample = Duration(seconds: 5);
  static const _flush = Duration(minutes: 2);

  /// Idle this long ends a session: the person is away, not working.
  static const _idle = 120.0;

  /// A session shorter than this is a glance, not work.
  static const _minSession = Duration(seconds: 10);

  bool enabled = false;
  DateTime? pausedUntil;
  bool get paused => pausedUntil != null && DateTime.now().isBefore(pausedUntil!);

  /// Sessions sent today, as the instance counts them.
  int today = 0;
  String timeZone = 'UTC';

  /// Menu strings, in the app's language; set by the UI.
  Map<String, String> strings = const {};

  CoveyApi? _api;
  Timer? _sampler;
  Timer? _flusher;
  _Open? _open;
  final List<Map<String, Object?>> _pending = [];

  /// Called when the menu-bar item asks for the review: the UI opens it.
  void Function()? onReviewRequested;

  Future<void> attach(CoveyApi api) async {
    if (!supported) return;
    _api = api;
    _channel.setMethodCallHandler(_fromNative);
    try {
      timeZone = await _channel.invokeMethod<String>('timeZone') ?? 'UTC';
    } on Exception {
      timeZone = 'UTC';
    }
    enabled = await Prefs.instance.read(_enabledKey) == 'on';
    if (enabled) _start();
    unawaited(refreshCount());
    notifyListeners();
  }

  Future<void> detach() async {
    await _stop();
    _api = null;
  }

  Future<void> setEnabled(bool on) async {
    enabled = on;
    pausedUntil = null;
    if (on) {
      _start();
      // Window, page and field need the Accessibility permission.
      if (!DictateAnywhere.instance.trusted) await DictateAnywhere.instance.askTrust();
    } else {
      await _stop();
    }
    diag('activity', on ? 'on' : 'off');
    notifyListeners();
    await Prefs.instance.write(_enabledKey, on ? 'on' : 'off');
  }

  /// Pauses for an hour — or resumes, when paused.
  void togglePause() {
    if (paused) {
      pausedUntil = null;
    } else {
      pausedUntil = DateTime.now().add(const Duration(hours: 1));
      _close();
    }
    diag('activity', paused ? 'paused for an hour' : 'resumed');
    _status();
    notifyListeners();
  }

  void _start() {
    _sampler?.cancel();
    _flusher?.cancel();
    _sampler = Timer.periodic(_sample, (_) => unawaited(_tick()));
    _flusher = Timer.periodic(_flush, (_) => unawaited(flush()));
    _status();
  }

  Future<void> _stop() async {
    _sampler?.cancel();
    _flusher?.cancel();
    _sampler = null;
    _flusher = null;
    _close();
    await flush();
    _status();
  }

  Future<void> _fromNative(MethodCall call) async {
    if (call.method != 'statusAction') return;
    switch (call.arguments) {
      case 'pause':
      case 'resume':
        togglePause();
      case 'off':
        await setEnabled(false);
      case 'review':
        onReviewRequested?.call();
    }
  }

  /// The menu-bar item: shown while on, with pause, review and off.
  void _status() {
    final on = enabled && _sampler != null;
    unawaited(
      _channel
          .invokeMethod<void>('status', {
            'visible': on,
            'paused': paused,
            'title': paused ? (strings['paused'] ?? 'Paused') : (strings['title'] ?? 'Recording activity'),
            if (!paused) 'pause': strings['pause'] ?? 'Pause for an hour',
            if (paused) 'resume': strings['resume'] ?? 'Resume',
            'review': strings['review'] ?? 'Daily review',
            'off': strings['off'] ?? 'Turn off',
          })
          .catchError((Object _) {}),
    );
  }

  Future<void> _tick() async {
    if (paused) return;
    if (pausedUntil != null) {
      // The hour is over.
      pausedUntil = null;
      _status();
      notifyListeners();
    }
    final Map<String, Object?> s;
    try {
      s = await _channel.invokeMapMethod<String, Object?>('sample') ?? const {};
    } on Exception {
      return;
    }
    final idle = (s['idle'] as num?)?.toDouble() ?? 0;
    final app = s['app'] as String? ?? '';
    if (idle >= _idle || app.isEmpty) {
      _close();
      return;
    }
    // A password manager or a secure field: that it was in front, no more.
    final secret = s['excluded'] == true || s['secure'] == true;
    final window = secret ? '' : (s['window'] as String? ?? '');
    final url = secret ? '' : (s['url'] as String? ?? '');
    final key = '$app\u0000$window\u0000$url';
    final now = DateTime.now();
    final open = _open;
    if (open != null && open.key == key) {
      open.end = now;
    } else {
      _close();
      _open = _Open(key, now, app, s['bundle'] as String? ?? '', window, url);
    }
    if (!secret) {
      final field = s['field'] as String? ?? '';
      final before = s['before'] as String? ?? '';
      final after = s['after'] as String? ?? '';
      if (field.isNotEmpty) _open!.field = field;
      // The latest state of what was worked on: a draft is most telling
      // where it was left.
      final excerpt = '$before$after'.trim();
      if (excerpt.isNotEmpty) {
        _open!.excerpt = excerpt.length > 800 ? excerpt.substring(excerpt.length - 800) : excerpt;
      }
    }
  }

  void _close() {
    final o = _open;
    _open = null;
    if (o == null || o.end.difference(o.start) < _minSession) return;
    _pending.add(o.toJson());
    // Never more than a working day's worth held back while offline.
    if (_pending.length > 2000) _pending.removeRange(0, _pending.length - 2000);
  }

  /// Sends what is finished. A failure keeps it for the next round.
  Future<void> flush() async {
    final api = _api;
    if (api == null || _pending.isEmpty) return;
    final batch = List.of(_pending.take(500));
    try {
      await api.addActivity(batch);
      _pending.removeRange(0, batch.length);
      diag('activity', 'sent ${batch.length} sessions');
      await refreshCount();
    } on ApiException catch (e) {
      diag('activity', 'not sent, kept: ${e.message}');
    }
  }

  static String dayOf(DateTime d) =>
      '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

  Future<void> refreshCount() async {
    final api = _api;
    if (api == null) return;
    try {
      today = await api.activityCount(dayOf(DateTime.now()), timeZone);
      notifyListeners();
    } on ApiException {
      // Unknown; the setting shows the last count.
    }
  }

  /// Today's review, as a note. What is still open is sent first.
  Future<Note> review({required String lang, required String title}) async {
    final api = _api!;
    _close();
    await flush();
    return api.activityReview(
      dayOf(DateTime.now()),
      CoveyApi.zoneQuery(tz: timeZone),
      lang: lang,
      title: title,
    );
  }

  Future<void> deleteToday() async {
    _open = null;
    _pending.clear();
    await _api?.deleteActivity(day: dayOf(DateTime.now()), tz: timeZone);
    await refreshCount();
  }

  Future<void> deleteAll() async {
    _open = null;
    _pending.clear();
    await _api?.deleteActivity();
    await refreshCount();
  }
}

class _Open {
  _Open(this.key, this.start, this.app, this.bundle, this.window, this.url) : end = start;

  final String key;
  final DateTime start;
  DateTime end;
  final String app;
  final String bundle;
  final String window;
  final String url;
  String field = '';
  String excerpt = '';

  Map<String, Object?> toJson() => {
    'started_at': start.toUtc().toIso8601String(),
    'ended_at': end.toUtc().toIso8601String(),
    'app': _cap(app, 500),
    'bundle': _cap(bundle, 500),
    'window': _cap(window, 500),
    'url': _cap(url, 500),
    'field': _cap(field, 500),
    'excerpt': _cap(excerpt, 1000),
  };

  static String _cap(String s, int n) => s.length > n ? s.substring(0, n) : s;
}
