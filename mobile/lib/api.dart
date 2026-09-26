import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;

import 'diagnostics.dart';

import 'models.dart';

/// An answer of the instance that was not a success. [status] is 0 when no
/// answer arrived at all.
class ApiException implements Exception {
  ApiException(this.status, this.message);

  final int status;
  final String message;

  @override
  String toString() => message;
}

/// Loopback, the private IPv4 ranges and mDNS names: the machines a
/// developer's phone can reach on the desk.
bool isLocalHost(String host) {
  if (host == 'localhost' || host.endsWith('.local')) return true;
  final p = host.split('.').map(int.tryParse).toList();
  if (p.length != 4 || p.contains(null)) return false;
  final (a, b) = (p[0]!, p[1]!);
  return a == 127 || a == 10 || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168);
}

/// Checks and normalises the address a person typed. HTTPS only (spec/27);
/// the one exception is a developer build (debug or profile — a profile
/// build is what starts on a phone without a debugger attached) talking to
/// the developer's own machine
/// — loopback, or a private network address, which is how a phone reaches
/// `make run` on the Mac beside it (#345). Release builds never accept http.
Uri parseInstance(String input) {
  var s = input.trim();
  if (!s.contains('://')) s = 'https://$s';
  final uri = Uri.tryParse(s);
  if (uri == null || uri.host.isEmpty) throw const FormatException('address');
  if (uri.scheme != 'https' && !(!kReleaseMode && uri.scheme == 'http' && isLocalHost(uri.host))) {
    throw const FormatException('https');
  }
  // Built anew rather than with Uri.replace: there a null query means "keep
  // it", and a pasted address with ?… or #… would carry that into every call.
  return Uri(
    scheme: uri.scheme,
    host: uri.host,
    port: uri.hasPort ? uri.port : null,
    path: uri.path.replaceAll(RegExp(r'/+$'), ''),
  );
}

/// The instance, spoken to with an API key as the bearer.
///
/// The key is the interim spec/27 names — not the design. The device badge
/// (decision 1) replaces it, and nothing outside this class may know which of
/// the two it is.
class CoveyApi {
  CoveyApi(this.base, this._key, {http.Client? client}) : _http = client ?? http.Client();

  final Uri base;
  final String _key;
  final http.Client _http;

  static const _timeout = Duration(seconds: 20);

  /// The API address for [path], which may carry a query. The query has to
  /// be split off: handed to Uri.replace as part of the path, its `?` is
  /// encoded as `%3F` and the instance answers 404.
  Uri _url(String path) {
    final rel = Uri.parse(path);
    return base.replace(
      path: '${base.path}/api/v1${rel.path}',
      queryParameters: rel.hasQuery ? rel.queryParameters : null,
    );
  }

  Map<String, String> get _headers => {'Authorization': 'Bearer $_key', 'Accept': 'application/json'};

  Future<dynamic> _send(Future<http.Response> Function() call) async {
    final http.Response res;
    final watch = Stopwatch()..start();
    try {
      res = await call().timeout(_timeout);
    } catch (e) {
      diag('api', 'failed after ${watch.elapsedMilliseconds} ms: $e');
      throw ApiException(0, e.toString());
    }
    // Method, path and status — not the query, which can carry a search.
    diag('api', '${res.request?.method} ${res.request?.url.path} ${res.statusCode} ${watch.elapsedMilliseconds} ms');
    // Not every answer is JSON: a route the instance does not know, or a
    // proxy in front of it, answers in plain text. That is an HTTP error with
    // a sentence, not a parse error.
    final text = utf8.decode(res.bodyBytes, allowMalformed: true);
    Object? body;
    try {
      body = text.isEmpty ? null : jsonDecode(text);
    } on FormatException {
      if (res.statusCode < 400) throw ApiException(res.statusCode, 'not JSON: ${text.trim()}');
      body = text.trim();
    }
    if (res.statusCode >= 400) {
      if (body is String && body.isNotEmpty) throw ApiException(res.statusCode, 'HTTP ${res.statusCode}: $body');
      final msg = body is Map && body['error'] is String ? body['error'] as String : 'HTTP ${res.statusCode}';
      throw ApiException(res.statusCode, msg);
    }
    return body;
  }

  Future<dynamic> get(String path) => _send(() => _http.get(_url(path), headers: _headers));

  Future<dynamic> patch(String path, Map<String, Object?> body) => _send(
    () => _http.patch(_url(path), headers: {..._headers, 'Content-Type': 'application/json'}, body: jsonEncode(body)),
  );

  Future<dynamic> delete(String path) => _send(() => _http.delete(_url(path), headers: _headers));

  Future<dynamic> post(String path, Map<String, Object?> body) => _send(
    () => _http.post(_url(path), headers: {..._headers, 'Content-Type': 'application/json'}, body: jsonEncode(body)),
  );

  /// Whether anything answers at the address at all. The only thing an
  /// instance says without a badge, and "ok" does not yet say it is a covey —
  /// /auth/me does that, behind the key.
  Future<bool> reachable() async {
    try {
      final res = await _http.get(base.replace(path: '${base.path}/healthz')).timeout(_timeout);
      return res.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  Future<Me> me() async => Me.fromJson(await get('/auth/me') as Map<String, dynamic>);

  Future<String> version() async {
    final v = await get('/version') as Map<String, dynamic>;
    return v['version'] as String? ?? '';
  }

  Future<List<Agent>> agents() async => [
    for (final a in (await get('/agents') as List? ?? const [])) Agent.fromJson(a as Map<String, dynamic>),
  ];

  Future<List<Department>> departments() async => [
    for (final d in (await get('/departments') as List? ?? const [])) Department.fromJson(d as Map<String, dynamic>),
  ];

  Future<List<Running>> running() async => [
    for (final r in (await get('/org/running') as List? ?? const [])) Running.fromJson(r as Map<String, dynamic>),
  ];

  Future<InboxPage> waiting() async =>
      InboxPage.fromJson(await get('/inbox?status=open&sort=urgent&limit=100') as Map<String, dynamic>);

  Future<Thread> thread(String agentId) async =>
      Thread.fromJson(await get('/agents/$agentId/thread') as Map<String, dynamic>);

  /// Per agent, what the person has not read yet (#378). An instance older
  /// than that answers 404, and the list simply shows no badges.
  Future<Map<String, ThreadState>> threads() async {
    final out = await get('/me/threads') as Map<String, dynamic>;
    return {
      for (final t in (out['threads'] as List? ?? const []))
        (t as Map<String, dynamic>)['agent_id'] as String: ThreadState.fromJson(t),
    };
  }

  /// Registers this device for push notifications (#379).
  Future<void> registerPushDevice({
    required String token,
    required String platform,
    required String environment,
    required String lang,
    String sound = 'bot',
  }) => post('/me/push/devices', {
    'token': token,
    'platform': platform,
    'environment': environment,
    'lang': lang,
    'sound': sound,
  });

  Future<void> unregisterPushDevice(String token) => delete('/me/push/devices/${Uri.encodeComponent(token)}');

  /// The thread has been read up to [at], the newest entry shown.
  Future<void> markThreadRead(String agentId, DateTime at) =>
      post('/agents/$agentId/thread/read', {'at': at.toUtc().toIso8601String()});

  /// Hands work over. What becomes of it — a task, or an answer when the
  /// organisation runs the triage — the thread shows on its next read.
  Future<void> send(String agentId, String text) => post('/agents/$agentId/messages', {'text': text});

  /// Answers a parked question. Returns whether it woke the agent; false is
  /// not an error — nobody was waiting, and the text stays on the task as a
  /// note (spec/27).
  Future<bool> reply(String taskId, String text) async {
    final r = await post('/tasks/$taskId/reply', {'text': text}) as Map<String, dynamic>;
    return r['woken'] as bool? ?? false;
  }

  // --- The notetaker (#336): the seat's own notes. ---

  Future<NotesPage> notes({String q = ''}) async => NotesPage.fromJson(
    await get(q.trim().isEmpty ? '/me/notes' : '/me/notes?q=${Uri.encodeQueryComponent(q.trim())}')
        as Map<String, dynamic>,
  );

  Future<Note> createNote({
    required String kind,
    required String body,
    String title = '',
    int durationSeconds = 0,
  }) async => Note.fromJson(
    await post('/me/notes', {'kind': kind, 'title': title, 'body': body, 'duration_seconds': durationSeconds})
        as Map<String, dynamic>,
  );

  /// Changes a note; null leaves a field as it is. [due] as `YYYY-MM-DD`,
  /// empty to clear (#373).
  Future<Note> updateNote(
    String id, {
    String? title,
    String? body,
    String? icon,
    String? cover,
    String? status,
    String? due,
    List<String>? tags,
  }) async => Note.fromJson(
    await patch('/me/notes/$id', {
          'title': ?title,
          'body': ?body,
          'icon': ?icon,
          'cover': ?cover,
          'status': ?status,
          'due': ?due,
          'tags': ?tags,
        })
        as Map<String, dynamic>,
  );

  Future<void> deleteNote(String id) => delete('/me/notes/$id');

  /// A summary and the action items — one model turn on the instance.
  Future<Note> summarizeNote(String id) async =>
      Note.fromJson(await post('/me/notes/$id/summarize', const {}) as Map<String, dynamic>);

  /// The folder in the agent's home a message's files go to — the web's
  /// ANHANG_ORDNER (web/src/team/Thread.tsx), so a file from the phone lies
  /// where a file from the browser lies.
  static const inbox = 'eingang';

  /// Uploads files into the agent's home under [inbox] (#340) and returns
  /// the paths the message names. One multipart request, field "file", the
  /// route the web uses.
  Future<List<String>> uploadToInbox(String agentId, List<Attachment> files) async {
    final req = http.MultipartRequest('POST', _url('/agents/$agentId/files/upload?path=$inbox'))
      ..headers.addAll(_headers);
    for (final f in files) {
      final length = f.length ?? (await f.open().fold<int>(0, (n, chunk) => n + chunk.length));
      req.files.add(http.MultipartFile('file', f.open(), length, filename: f.name));
    }
    await _send(() async => http.Response.fromStream(await _http.send(req)));
    return [for (final f in files) '$inbox/${f.name}'];
  }

  /// Uploads a picture for the seat's notes (#344) and returns the reference
  /// a note's Markdown carries: `covey-media://<id>`.
  Future<String> uploadNoteMedia(Attachment f) async {
    final req = http.MultipartRequest('POST', _url('/me/notes/media'))..headers.addAll(_headers);
    final length = f.length ?? (await f.open().fold<int>(0, (n, chunk) => n + chunk.length));
    req.files.add(http.MultipartFile('file', f.open(), length, filename: f.name));
    final out = await _send(() async => http.Response.fromStream(await _http.send(req))) as Map<String, dynamic>;
    return out['ref'] as String;
  }

  /// A picture of the seat's notes, as bytes — fetched with the key, which is
  /// why the note cannot simply hand the image widget a URL.
  Future<Uint8List> noteMedia(String id) async {
    final http.Response res;
    try {
      res = await _http.get(_url('/me/notes/media/$id'), headers: _headers).timeout(_timeout);
    } catch (e) {
      throw ApiException(0, e.toString());
    }
    if (res.statusCode != 200) throw ApiException(res.statusCode, 'HTTP ${res.statusCode}');
    return res.bodyBytes;
  }

  /// Sets the seat's profile photo (#377) and returns its id. The server
  /// makes a square JPEG of it and keeps nothing else of the file.
  Future<String> setPhoto(Uint8List bytes) async {
    final req = http.MultipartRequest('PUT', _url('/me/photo'))
      ..headers.addAll(_headers)
      ..files.add(http.MultipartFile.fromBytes('file', bytes, filename: 'photo.png'));
    final out = await _send(() async => http.Response.fromStream(await _http.send(req))) as Map<String, dynamic>;
    return out['photo_id'] as String;
  }

  Future<void> deletePhoto() => delete('/me/photo');

  /// A person's profile photo, as bytes — fetched with the key.
  Future<Uint8List> humanPhoto(String humanId, String photoId) async {
    final http.Response res;
    try {
      res = await _http.get(_url('/humans/$humanId/photo?v=$photoId'), headers: _headers).timeout(_timeout);
    } catch (e) {
      throw ApiException(0, e.toString());
    }
    if (res.statusCode != 200) throw ApiException(res.statusCode, 'HTTP ${res.statusCode}');
    return res.bodyBytes;
  }

  /// The speech model this instance offers (#348): name, digest, size, and
  /// whether it can be fetched yet. `enabled: false` means speech is off here.
  /// [name] asks about one of the offered models (#351) — and makes the
  /// instance fetch it when it does not have it yet.
  Future<SpeechModelInfo> speechModel({String? name}) async => SpeechModelInfo.fromJson(
    await get(name == null ? '/speech/model' : '/speech/model?name=${Uri.encodeQueryComponent(name)}')
        as Map<String, dynamic>,
  );

  /// The activity log (#363): sessions the Mac recorded, the caller's own.
  Future<void> addActivity(List<Map<String, Object?>> sessions) => post('/me/activity', {'sessions': sessions});

  /// One day's sessions; [day] is YYYY-MM-DD in the IANA zone [tz].
  Future<int> activityCount(String day, String tz) async {
    final out = await get('/me/activity?day=$day&tz=${Uri.encodeQueryComponent(tz)}') as Map<String, dynamic>;
    return (out['sessions'] as List<dynamic>).length;
  }

  /// Deletes one day, or with no [day] the whole log.
  Future<void> deleteActivity({String? day, String? tz}) =>
      delete(day == null ? '/me/activity' : '/me/activity?day=$day&tz=${Uri.encodeQueryComponent(tz ?? 'UTC')}');

  /// Writes the review of a day as a note and returns it.
  Future<Note> activityReview(String day, String zone, {required String lang, required String title}) async =>
      Note.fromJson(
        await post('/me/activity/review?day=$day&$zone', {'lang': lang, 'title': title}) as Map<String, dynamic>,
      );

  /// The person's zone as the activity routes take it: the IANA name where
  /// it is known, else the offset from UTC (#368).
  static String zoneQuery({String? tz}) => tz != null && tz.isNotEmpty
      ? 'tz=${Uri.encodeQueryComponent(tz)}'
      : 'offset=${DateTime.now().timeZoneOffset.inMinutes}';

  /// The days with activity, newest first, with their review note (#368).
  Future<List<ActivityDay>> activityDays(String zone) async {
    final out = await get('/me/activity/days?$zone') as Map<String, dynamic>;
    return [
      for (final d in out['days'] as List<dynamic>)
        ActivityDay(
          day: (d as Map<String, dynamic>)['day'] as String,
          sessions: (d['sessions'] as num).toInt(),
          review: d['review'] as String?,
          stale: d['stale'] == true,
        ),
    ];
  }

  /// The seat's latest agent suggestions (#370), or null before the first.
  Future<AgentSuggestions?> suggestions() async {
    final out = await get('/me/activity/suggestions') as Map<String, dynamic>;
    return out['suggestions'] == null ? null : AgentSuggestions.fromJson(out);
  }

  /// Computes the agent suggestions from the last two weeks, anew.
  Future<AgentSuggestions> suggest(String zone, {required String lang}) async => AgentSuggestions.fromJson(
    await post('/me/activity/suggestions?$zone&lang=${Uri.encodeQueryComponent(lang)}', const {})
        as Map<String, dynamic>,
  );

  /// A job posting to the hiring flow (spec/20): the HR agent drafts the
  /// new agent from it. Needs a role that may manage agents.
  Future<void> hiringBrief(String description, {required String lang}) =>
      post('/hiring/brief?lang=${Uri.encodeQueryComponent(lang)}', {'description': description});

  Future<Note> note(String id) async => Note.fromJson(await get('/me/notes/$id') as Map<String, dynamic>);

  /// Dictated text as the person meant to write it (#355): filler words
  /// out, self-corrections applied, punctuation set. [app] names the
  /// application the text is for, so the form can fit it.
  ///
  /// [context] says where the text goes (#362): `window`, `field`, `before`
  /// and `after` the insertion point.
  Future<String> cleanDictation(String text, {String? app, Map<String, String>? context}) async {
    final out =
        await post('/me/dictation/clean', {'text': text, 'app': ?app, 'context': ?context}) as Map<String, dynamic>;
    return out['text'] as String;
  }

  /// The model file, from byte [from] on — a download cut off by a lost
  /// connection resumes rather than starting the 150 MB again. No timeout on
  /// the body: it takes as long as the network takes.
  Future<http.StreamedResponse> speechModelFile({String? name, String? file, int from = 0}) async {
    final q = [
      if (name != null) 'name=${Uri.encodeQueryComponent(name)}',
      if (file != null) 'file=${Uri.encodeQueryComponent(file)}',
    ];
    final path = q.isEmpty ? '/speech/model/file' : '/speech/model/file?${q.join('&')}';
    final req = http.Request('GET', _url(path))
      ..headers.addAll({..._headers, 'Accept': 'application/octet-stream', if (from > 0) 'Range': 'bytes=$from-'});
    final http.StreamedResponse res;
    try {
      res = await _http.send(req).timeout(_timeout);
    } catch (e) {
      throw ApiException(0, e.toString());
    }
    if (res.statusCode != 200 && res.statusCode != 206) {
      final text = await res.stream.bytesToString().catchError((_) => '');
      String msg = 'HTTP ${res.statusCode}';
      try {
        final body = jsonDecode(text);
        if (body is Map && body['error'] is String) msg = body['error'] as String;
      } on FormatException {
        // Not JSON: the status says enough.
      }
      throw ApiException(res.statusCode, msg);
    }
    return res;
  }
}

/// What `GET /speech/model` answers (#348, #351): one model's state at the
/// top level, and every model the instance offers under [models].
class SpeechModelInfo {
  const SpeechModelInfo({
    required this.enabled,
    this.name = '',
    this.sha256 = '',
    this.size = 0,
    this.ready = false,
    this.fetching = false,
    this.received = 0,
    this.error,
    this.defaultName = '',
    this.models = const [],
    this.engine = 'parakeet',
    this.credit,
    this.files = const [],
    this.clean = false,
  });

  factory SpeechModelInfo.fromJson(Map<String, dynamic> j) => SpeechModelInfo(
    enabled: j['enabled'] != false,
    name: j['name'] as String? ?? '',
    sha256: j['sha256'] as String? ?? '',
    size: (j['size'] as num?)?.toInt() ?? 0,
    ready: j['ready'] == true,
    fetching: j['fetching'] == true,
    received: (j['received'] as num?)?.toInt() ?? 0,
    error: j['error'] as String?,
    defaultName: j['default'] as String? ?? j['name'] as String? ?? '',
    models: [
      for (final m in (j['models'] as List<dynamic>? ?? const [])) SpeechModelInfo.fromJson(m as Map<String, dynamic>),
    ],
    clean: j['clean'] == true,
    engine: j['engine'] as String? ?? 'parakeet',
    credit: j['credit'] as String?,
    files: [
      for (final f in (j['files'] as List<dynamic>? ?? const []))
        SpeechModelFile(
          name: (f as Map<String, dynamic>)['name'] as String,
          sha256: f['sha256'] as String,
          size: (f['size'] as num).toInt(),
        ),
    ],
  );

  final bool enabled;
  final String name;
  final String sha256;
  final int size;
  final bool ready;

  /// The instance is downloading this model right now; [received] bytes so far.
  final bool fetching;
  final int received;
  final String? error;

  /// The instance's default model.
  final String defaultName;
  final List<SpeechModelInfo> models;

  /// Which model family it is: `parakeet` or `sensevoice`, both run by
  /// sherpa-onnx (#366).
  final String engine;

  /// The attribution the model's licence asks for.
  final String? credit;

  /// The model's files, each pinned by digest.
  final List<SpeechModelFile> files;

  /// Whether the instance can clean dictated text up (#355).
  final bool clean;
}

/// One file of a speech model.
class SpeechModelFile {
  const SpeechModelFile({required this.name, required this.sha256, required this.size});

  final String name;
  final String sha256;
  final int size;
}

/// One day of the activity log (#368).
class ActivityDay {
  const ActivityDay({required this.day, required this.sessions, this.review, this.stale = false});

  /// YYYY-MM-DD in the person's zone.
  final String day;
  final int sessions;

  /// The id of the day's review note, if it has one.
  final String? review;

  /// The day has activity after what its review covers (#369). The instance
  /// writes recent reviews again by itself; this says it has not yet.
  final bool stale;
}

/// Agent suggestions from the activity log (#370).
class AgentSuggestions {
  const AgentSuggestions({required this.days, required this.sessions, required this.items, required this.createdAt});

  factory AgentSuggestions.fromJson(Map<String, dynamic> j) => AgentSuggestions(
    days: (j['days'] as num?)?.toInt() ?? 0,
    sessions: (j['sessions'] as num?)?.toInt() ?? 0,
    createdAt: DateTime.tryParse(j['created_at'] as String? ?? '')?.toLocal() ?? DateTime.now(),
    items: [
      for (final x in (j['suggestions'] as List<dynamic>? ?? const []))
        AgentSuggestion.fromJson(x as Map<String, dynamic>),
    ],
  );

  final int days;
  final int sessions;
  final List<AgentSuggestion> items;
  final DateTime createdAt;
}

class AgentSuggestion {
  const AgentSuggestion({
    required this.title,
    this.description = '',
    this.pattern = '',
    this.minutesPerWeek = 0,
    this.systems = const [],
    this.agent = '',
    required this.brief,
    this.evidence = const [],
  });

  factory AgentSuggestion.fromJson(Map<String, dynamic> j) => AgentSuggestion(
    title: j['title'] as String? ?? '',
    description: j['description'] as String? ?? '',
    pattern: j['pattern'] as String? ?? '',
    minutesPerWeek: (j['minutes_per_week'] as num?)?.toInt() ?? 0,
    systems: [for (final s in (j['systems'] as List<dynamic>? ?? const [])) '$s'],
    agent: j['agent'] as String? ?? '',
    brief: j['brief'] as String? ?? '',
    evidence: [for (final s in (j['evidence'] as List<dynamic>? ?? const [])) '$s'],
  );

  final String title;
  final String description;
  final String pattern;
  final int minutesPerWeek;
  final List<String> systems;
  final String agent;
  final String brief;
  final List<String> evidence;
}
