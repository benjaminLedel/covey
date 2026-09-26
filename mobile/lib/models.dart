import 'package:flutter/foundation.dart';

// The shapes the app reads, as the instance writes them. Only the fields the
// app shows are read; everything else in the JSON is ignored, so a newer
// instance with more fields does not break an older app.

DateTime? _time(Object? v) => v is String ? DateTime.tryParse(v)?.toLocal() : null;

/// GET /auth/me — the seat this key works from.
class Me {
  Me({
    required this.email,
    required this.displayName,
    required this.role,
    required this.teamSurface,
    this.canWrite = true,
    this.id = '',
    this.photoId,
  });

  factory Me.fromJson(Map<String, dynamic> j) => Me(
    email: j['Email'] as String? ?? '',
    displayName: j['DisplayName'] as String? ?? '',
    role: j['Role'] as String? ?? '',
    // Absent on an instance older than #328: there the surface was always on.
    teamSurface: j['TeamSurface'] as bool? ?? true,
    // Absent on an instance older than #339: then the server's 403 is the
    // only word, and the app lets the person try.
    canWrite: j['CanWrite'] as bool? ?? true,
    id: j['ID'] as String? ?? '',
    photoId: j['PhotoID'] as String?,
  );

  final String email;
  final String displayName;
  final String role;

  /// Whether the organisation has the team surface on (#328). Off, the
  /// instance refuses a message with 403 — the app says so up front instead of
  /// at the first send.
  final bool teamSurface;

  /// Whether this seat's role may hand over work and answer (#339) — the
  /// server says so, the app does not keep a role table of its own. A seat
  /// that may not is never led into a conversation.
  final bool canWrite;

  /// The seat's id, and its profile photo (#377) — null is the monogram.
  final String id;
  final String? photoId;

  Me withPhoto(String? photoId) => Me(
    email: email,
    displayName: displayName,
    role: role,
    teamSurface: teamSurface,
    canWrite: canWrite,
    id: id,
    photoId: photoId,
  );
}

class Agent {
  Agent({
    required this.id,
    required this.slug,
    required this.displayName,
    required this.jobTitle,
    required this.status,
    required this.departmentId,
    required this.killed,
    this.hired = true,
  });

  factory Agent.fromJson(Map<String, dynamic> j) => Agent(
    id: j['id'] as String,
    slug: j['slug'] as String? ?? '',
    displayName: j['display_name'] as String? ?? '',
    jobTitle: j['job_title'] as String? ?? '',
    status: j['status'] as String? ?? '',
    departmentId: j['department_id'] as String?,
    killed: j['killed'] as bool? ?? false,
    // Absent is a draft: the instance leaves hired_at out until the first day.
    hired: j['hired_at'] != null,
  );

  final String id;
  final String slug;
  final String displayName;
  final String jobTitle;
  final String status;
  final String? departmentId;
  final bool killed;

  /// A draft is not a colleague (spec/27): nobody writes to an applicant.
  bool get isApplicant => status == 'applicant';

  /// Hired and not an application (#414): what the team list and the office
  /// show. Drafts belong to the administration, where they are hired.
  final bool hired;
  bool get isColleague => hired && !isApplicant;
}

class Department {
  Department({required this.id, required this.name, this.color = ''});

  factory Department.fromJson(Map<String, dynamic> j) =>
      Department(id: j['id'] as String, name: j['name'] as String? ?? '', color: j['color'] as String? ?? '');

  final String id;
  final String name;

  /// A CSS hex like "#6d8c5a", or "" — the office draws the room's door
  /// frame and skirting in it (#398).
  final String color;
}

/// One entry of GET /org/running: a task being worked on right now. In the
/// office it is what lights a screen (#398).
class Running {
  Running({required this.agentId, required this.title, this.step});

  factory Running.fromJson(Map<String, dynamic> j) =>
      Running(agentId: j['agent_id'] as String? ?? '', title: j['title'] as String? ?? '', step: j['step'] as String?);

  final String agentId;
  final String title;

  /// The kind of the last recorded step, not its content.
  final String? step;
}

/// One open point of GET /inbox.
class InboxEntry {
  InboxEntry({
    required this.type,
    required this.id,
    required this.agentId,
    required this.agentName,
    required this.agentSlug,
    required this.title,
    required this.createdAt,
  });

  factory InboxEntry.fromJson(Map<String, dynamic> j) => InboxEntry(
    type: j['type'] as String? ?? '',
    id: j['id'] as String,
    agentId: j['agent_id'] as String? ?? '',
    agentName: j['agent_name'] as String? ?? '',
    agentSlug: j['agent_slug'] as String? ?? '',
    title: j['title'] as String? ?? '',
    createdAt: _time(j['created_at']),
  );

  final String type;
  final String id;
  final String agentId;
  final String agentName;
  final String agentSlug;
  final String title;
  final DateTime? createdAt;
}

class InboxPage {
  InboxPage({required this.items, required this.pending});

  factory InboxPage.fromJson(Map<String, dynamic> j) => InboxPage(
    items: [for (final e in (j['items'] as List? ?? const [])) InboxEntry.fromJson(e as Map<String, dynamic>)],
    pending: j['pending'] as int? ?? 0,
  );

  final List<InboxEntry> items;
  final int pending;
}

/// One line of the thread: message | note | question | result | error.
class ThreadEntry {
  ThreadEntry({
    required this.kind,
    required this.id,
    required this.taskId,
    required this.taskTitle,
    required this.taskState,
    required this.author,
    required this.text,
    required this.at,
    this.said = '',
  });

  factory ThreadEntry.fromJson(Map<String, dynamic> j) => ThreadEntry(
    kind: j['kind'] as String? ?? '',
    id: j['id'] as String? ?? '',
    taskId: j['task_id'] as String?,
    taskTitle: j['task_title'] as String? ?? '',
    taskState: j['task_state'] as String? ?? '',
    author: j['author'] as String? ?? '',
    text: j['text'] as String? ?? '',
    at: _time(j['at']),
    said: j['said'] as String? ?? '',
  );

  final String kind;
  final String id;
  final String? taskId;
  final String taskTitle;
  final String taskState;
  final String author;
  final String text;
  final DateTime? at;

  /// On a result or an error: what the agent said about it in the chat
  /// (#411), told from the report in [text], which stays one tap away.
  final String said;

  /// The person's side of the thread. The author is the only thing that
  /// decides it: `chat:<mail>` for a message, `human:<mail>` for a note.
  bool get fromPerson => author.startsWith('chat:') || author.startsWith('human:');

  /// A question the agent is parked on — the one line a reply goes to.
  bool get isOpenQuestion => kind == 'question' && taskState == 'blocked' && taskId != null;
}

/// One agent's thread as the list shows it (#378): how much the person has
/// not read, and the newest entry from the agent's side.
class ThreadState {
  ThreadState({required this.agentId, required this.unread, this.lastAt, this.lastText = '', this.lastKind = ''});

  factory ThreadState.fromJson(Map<String, dynamic> j) => ThreadState(
    agentId: j['agent_id'] as String,
    unread: (j['unread'] as num?)?.toInt() ?? 0,
    lastAt: _time(j['last_at']),
    lastText: j['last_text'] as String? ?? '',
    lastKind: j['last_kind'] as String? ?? '',
  );

  final String agentId;
  final int unread;
  final DateTime? lastAt;
  final String lastText;
  final String lastKind;
}

/// How many conversations' entries the person has not read (#378): the
/// team list writes it, the rail on a wide window shows it (#393).
final unreadTotal = ValueNotifier<int>(0);

/// Told when a thread has been read, so the list drops its badge at once
/// instead of at its next look.
final threadsRead = ValueNotifier<int>(0);

class Thread {
  Thread({required this.entries, required this.pending});

  factory Thread.fromJson(Map<String, dynamic> j) => Thread(
    entries: [for (final e in (j['entries'] as List? ?? const [])) ThreadEntry.fromJson(e as Map<String, dynamic>)],
    pending: j['pending'] as bool? ?? false,
  );

  final List<ThreadEntry> entries;

  /// A message is accepted and the triage has not decided yet.
  final bool pending;
}

/// One note of the notetaker (#336): the person's own, private.
class Note {
  Note({
    required this.id,
    required this.kind,
    required this.title,
    required this.body,
    required this.summary,
    required this.durationSeconds,
    required this.createdAt,
    this.icon = '',
    this.cover = '',
    this.status = '',
    this.due,
    this.tags = const [],
  });

  factory Note.fromJson(Map<String, dynamic> j) => Note(
    id: j['id'] as String,
    kind: j['kind'] as String? ?? 'text',
    title: j['title'] as String? ?? '',
    body: j['body'] as String? ?? '',
    summary: j['summary'] as String? ?? '',
    durationSeconds: j['duration_seconds'] as int? ?? 0,
    createdAt: _time(j['created_at']),
    icon: j['icon'] as String? ?? '',
    cover: j['cover'] as String? ?? '',
    status: j['status'] as String? ?? '',
    due: j['due'] == null ? null : DateTime.tryParse(j['due'] as String),
    tags: [for (final t in (j['tags'] as List? ?? const [])) '$t'],
  );

  final String id;

  /// text | voice | meeting
  final String kind;
  final String title;
  final String body;
  final String summary;
  final int durationSeconds;
  final DateTime? createdAt;

  /// An emoji; empty for none (#372).
  final String icon;

  /// A built-in gradient (`gradient:<name>`) or a picture of the note's
  /// media (`covey-media://<id>`); empty for none (#372).
  final String cover;

  /// Properties (#373): status is empty, todo, doing or done; due a day;
  /// tags a few short words.
  final String status;
  final DateTime? due;
  final List<String> tags;

  /// What the list shows as the line: the title, or else the first line of
  /// the text.
  String get heading => title.isNotEmpty ? title : body.split('\n').first;
}

class NotesPage {
  NotesPage({required this.notes, required this.summarize});

  factory NotesPage.fromJson(Map<String, dynamic> j) => NotesPage(
    notes: [for (final n in (j['notes'] as List? ?? const [])) Note.fromJson(n as Map<String, dynamic>)],
    summarize: j['summarize'] as bool? ?? false,
  );

  final List<Note> notes;

  /// Whether the instance can summarise here — the button is offered only
  /// then.
  final bool summarize;
}

/// A file handed over with a message (#340): a name and its bytes, streamed.
/// Its own type rather than the picker's, so the send path can be driven in
/// a test without a file dialog.
class Attachment {
  Attachment({required this.name, required this.length, required this.open});

  final String name;

  /// The size, if the platform knows it; the upload reads the bytes first
  /// when it does not.
  final int? length;
  final Stream<List<int>> Function() open;
}
