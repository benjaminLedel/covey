// The shapes the app reads, as the instance writes them. Only the fields the
// app shows are read; everything else in the JSON is ignored, so a newer
// instance with more fields does not break an older app.

DateTime? _time(Object? v) => v is String ? DateTime.tryParse(v)?.toLocal() : null;

/// GET /auth/me — the seat this key works from.
class Me {
  Me({required this.email, required this.displayName, required this.role, required this.teamSurface});

  factory Me.fromJson(Map<String, dynamic> j) => Me(
    email: j['Email'] as String? ?? '',
    displayName: j['DisplayName'] as String? ?? '',
    role: j['Role'] as String? ?? '',
    // Absent on an instance older than #328: there the surface was always on.
    teamSurface: j['TeamSurface'] as bool? ?? true,
  );

  final String email;
  final String displayName;
  final String role;

  /// Whether the organisation has the team surface on (#328). Off, the
  /// instance refuses a message with 403 — the app says so up front instead of
  /// at the first send.
  final bool teamSurface;
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
  });

  factory Agent.fromJson(Map<String, dynamic> j) => Agent(
    id: j['id'] as String,
    slug: j['slug'] as String? ?? '',
    displayName: j['display_name'] as String? ?? '',
    jobTitle: j['job_title'] as String? ?? '',
    status: j['status'] as String? ?? '',
    departmentId: j['department_id'] as String?,
    killed: j['killed'] as bool? ?? false,
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
}

class Department {
  Department({required this.id, required this.name});

  factory Department.fromJson(Map<String, dynamic> j) =>
      Department(id: j['id'] as String, name: j['name'] as String? ?? '');

  final String id;
  final String name;
}

/// One open point of GET /inbox.
class InboxEntry {
  InboxEntry({
    required this.type,
    required this.id,
    required this.agentId,
    required this.agentName,
    required this.title,
    required this.createdAt,
  });

  factory InboxEntry.fromJson(Map<String, dynamic> j) => InboxEntry(
    type: j['type'] as String? ?? '',
    id: j['id'] as String,
    agentId: j['agent_id'] as String? ?? '',
    agentName: j['agent_name'] as String? ?? '',
    title: j['title'] as String? ?? '',
    createdAt: _time(j['created_at']),
  );

  final String type;
  final String id;
  final String agentId;
  final String agentName;
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
  );

  final String kind;
  final String id;
  final String? taskId;
  final String taskTitle;
  final String taskState;
  final String author;
  final String text;
  final DateTime? at;

  /// The person's side of the thread. The author is the only thing that
  /// decides it: `chat:<mail>` for a message, `human:<mail>` for a note.
  bool get fromPerson => author.startsWith('chat:') || author.startsWith('human:');

  /// A question the agent is parked on — the one line a reply goes to.
  bool get isOpenQuestion => kind == 'question' && taskState == 'blocked' && taskId != null;
}

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
