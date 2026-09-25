import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;

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

/// Checks and normalises the address a person typed. HTTPS only (spec/27);
/// the one exception is a loopback address in a debug build, which is how the
/// app is developed against `make run`.
Uri parseInstance(String input) {
  var s = input.trim();
  if (!s.contains('://')) s = 'https://$s';
  final uri = Uri.tryParse(s);
  if (uri == null || uri.host.isEmpty) throw const FormatException('address');
  final loopback = {'localhost', '127.0.0.1', '10.0.2.2'}.contains(uri.host);
  if (uri.scheme != 'https' && !(kDebugMode && loopback && uri.scheme == 'http')) {
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
    try {
      res = await call().timeout(_timeout);
    } catch (e) {
      throw ApiException(0, e.toString());
    }
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

  Future<InboxPage> waiting() async =>
      InboxPage.fromJson(await get('/inbox?status=open&sort=urgent&limit=100') as Map<String, dynamic>);

  Future<Thread> thread(String agentId) async =>
      Thread.fromJson(await get('/agents/$agentId/thread') as Map<String, dynamic>);

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

  Future<NotesPage> notes() async => NotesPage.fromJson(await get('/me/notes') as Map<String, dynamic>);

  Future<Note> createNote({
    required String kind,
    required String body,
    String title = '',
    int durationSeconds = 0,
  }) async => Note.fromJson(
    await post('/me/notes', {'kind': kind, 'title': title, 'body': body, 'duration_seconds': durationSeconds})
        as Map<String, dynamic>,
  );

  Future<Note> updateNote(String id, {String? title, String? body}) async =>
      Note.fromJson(await patch('/me/notes/$id', {'title': ?title, 'body': ?body}) as Map<String, dynamic>);

  Future<void> deleteNote(String id) => delete('/me/notes/$id');

  /// A summary and the action items — one model turn on the instance.
  Future<Note> summarizeNote(String id) async =>
      Note.fromJson(await post('/me/notes/$id/summarize', const {}) as Map<String, dynamic>);
}
