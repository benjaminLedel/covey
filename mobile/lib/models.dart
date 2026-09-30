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
    bool? canChat,
    this.id = '',
    this.photoId,
    this.orgId = '',
  }) : canChat = canChat ?? canWrite;

  factory Me.fromJson(Map<String, dynamic> j) => Me(
    email: j['Email'] as String? ?? '',
    displayName: j['DisplayName'] as String? ?? '',
    role: j['Role'] as String? ?? '',
    // Absent on an instance older than #328: there the surface was always on.
    teamSurface: j['TeamSurface'] as bool? ?? true,
    // Absent on an instance older than #339: then the server's 403 is the
    // only word, and the app lets the person try.
    canWrite: j['CanWrite'] as bool? ?? true,
    // Absent on an instance older than #440: there chatting was what the
    // role might write, and CanWrite says it.
    canChat: j['CanChat'] as bool? ?? j['CanWrite'] as bool? ?? true,
    id: j['ID'] as String? ?? '',
    photoId: j['PhotoID'] as String?,
    orgId: j['OrgID'] as String? ?? '',
  );

  /// The organisation of the seat this key works from (#417).
  final String orgId;

  final String email;
  final String displayName;
  final String role;

  /// Whether the organisation has the team surface on (#328). Off, the
  /// instance refuses a message with 403 — the app says so up front instead of
  /// at the first send.
  final bool teamSurface;

  /// Whether this seat's role may hand over work and answer (#339) — the
  /// server says so, the app does not keep a role table of its own. It also
  /// decides who may bring an agent into a group (#440).
  final bool canWrite;

  /// Whether the seat may write in conversations (#440) — every seat of an
  /// organisation, auditors and viewers included. Which agents it reaches
  /// directly is the organisation's reach, not the role. Hiring and handing
  /// over work by hand stay with [canWrite].
  final bool canChat;

  /// The seat's id, and its profile photo (#377) — null is the monogram.
  final String id;
  final String? photoId;

  Me withPhoto(String? photoId) => Me(
    email: email,
    displayName: displayName,
    role: role,
    teamSurface: teamSurface,
    canWrite: canWrite,
    canChat: canChat,
    id: id,
    photoId: photoId,
    orgId: orgId,
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
    this.speaker = '',
    this.speakerId = '',
    this.speakerSlug = '',
    this.speakerHuman = false,
    this.mine,
  });

  /// A line of a conversation (#440) as the thread draws it: `text` is a
  /// message, a told result keeps its report one tap away as in the agent's
  /// thread, and the speaker is whoever wrote it — in a group not always the
  /// same agent. [slug] is the author's, when it is an agent.
  factory ThreadEntry.fromMessage(ConversationMessage m, {required String meId, String slug = ''}) {
    final told = m.report.isNotEmpty && m.report != m.text;
    return ThreadEntry(
      kind: m.kind == 'text' ? 'message' : m.kind,
      id: m.id,
      taskId: m.taskId,
      taskTitle: m.taskTitle,
      taskState: m.taskState,
      author: '${m.authorKind}:${m.authorId ?? ''}',
      text: told ? m.report : m.text,
      said: told ? m.text : '',
      at: m.createdAt,
      speaker: m.authorName,
      speakerId: m.authorId ?? '',
      speakerSlug: slug,
      speakerHuman: m.authorKind == 'human',
      mine: meId.isNotEmpty && m.authorKind == 'human' && m.authorId == meId,
    );
  }

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

  /// Who said it, where it is not the thread's agent (a conversation, #440):
  /// the name, the member's id and, for an agent, its slug. Empty in an
  /// agent's thread, which has one speaker besides the person.
  final String speaker;
  final String speakerId;
  final String speakerSlug;
  final bool speakerHuman;

  /// Set in a conversation, where the author's id decides it; null in an
  /// agent's thread, where the author string does.
  final bool? mine;

  /// The person's side of the thread. In an agent's thread the author is the
  /// only thing that decides it: `chat:<mail>` for a message, `human:<mail>`
  /// for a note.
  bool get fromPerson => mine ?? (author.startsWith('chat:') || author.startsWith('human:'));

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

/// Somebody a conversation can have as a member (#440): a person (a seat)
/// or an agent.
class MemberRef {
  const MemberRef(this.kind, this.id);

  /// `human` or `agent`.
  final String kind;
  final String id;

  bool get human => kind == 'human';

  Map<String, Object?> toJson() => {'kind': kind, 'id': id};

  @override
  bool operator ==(Object other) => other is MemberRef && other.kind == kind && other.id == id;

  @override
  int get hashCode => Object.hash(kind, id);
}

/// One member of a conversation, as GET /conversations names them.
class ConversationMember {
  ConversationMember({
    required this.kind,
    required this.id,
    required this.name,
    this.slug = '',
    this.email = '',
    this.role = 'member',
    this.left = false,
    this.muted = false,
  });

  factory ConversationMember.fromJson(Map<String, dynamic> j) => ConversationMember(
    kind: j['kind'] as String? ?? '',
    id: j['id'] as String? ?? '',
    name: j['name'] as String? ?? '',
    slug: j['slug'] as String? ?? '',
    email: j['email'] as String? ?? '',
    role: j['role'] as String? ?? 'member',
    left: j['left_at'] != null,
    muted: j['muted'] as bool? ?? false,
  );

  final String kind;
  final String id;
  final String name;
  final String slug;
  final String email;

  /// `owner` or `member`.
  final String role;

  /// Left the group; still named at what they wrote.
  final bool left;
  final bool muted;

  bool get human => kind == 'human';
  bool get agent => kind == 'agent';
  MemberRef get ref => MemberRef(kind, id);
}

/// One line of a conversation. `kind` is `text` for what somebody said;
/// `result`, `error` and `question` are what a task opened there reported
/// back, and a result or an error keeps the report it retells in [report].
class ConversationMessage {
  ConversationMessage({
    required this.id,
    required this.conversationId,
    required this.authorKind,
    required this.text,
    required this.kind,
    this.authorId,
    this.authorName = '',
    this.taskId,
    this.taskTitle = '',
    this.taskState = '',
    this.report = '',
    this.replyTo,
    this.createdAt,
  });

  factory ConversationMessage.fromJson(Map<String, dynamic> j) => ConversationMessage(
    id: j['id'] as String? ?? '',
    conversationId: j['conversation_id'] as String? ?? '',
    authorKind: j['author_kind'] as String? ?? '',
    authorId: j['author_id'] as String?,
    authorName: j['author_name'] as String? ?? '',
    text: j['text'] as String? ?? '',
    kind: j['kind'] as String? ?? 'text',
    taskId: j['task_id'] as String?,
    taskTitle: j['task_title'] as String? ?? '',
    taskState: j['task_state'] as String? ?? '',
    report: j['report'] as String? ?? '',
    replyTo: j['reply_to'] as String?,
    createdAt: _time(j['created_at']),
  );

  final String id;
  final String conversationId;

  /// `human`, `agent` or `system`.
  final String authorKind;
  final String? authorId;
  final String authorName;
  final String text;
  final String kind;
  final String? taskId;
  final String taskTitle;
  final String taskState;
  final String report;
  final String? replyTo;
  final DateTime? createdAt;
}

/// A conversation (#440): direct — two members, one per pair — or a group
/// with a title.
class Conversation {
  Conversation({
    required this.id,
    required this.kind,
    required this.members,
    this.title = '',
    this.lastMessageAt,
    this.unread = 0,
    this.muted = false,
    this.last,
  });

  factory Conversation.fromJson(Map<String, dynamic> j) => Conversation(
    id: j['id'] as String? ?? '',
    kind: j['kind'] as String? ?? 'direct',
    title: j['title'] as String? ?? '',
    members: [
      for (final m in (j['members'] as List? ?? const [])) ConversationMember.fromJson(m as Map<String, dynamic>),
    ],
    lastMessageAt: _time(j['last_message_at']),
    unread: (j['unread'] as num?)?.toInt() ?? 0,
    muted: j['muted'] as bool? ?? false,
    last: j['last'] is Map<String, dynamic> ? ConversationMessage.fromJson(j['last'] as Map<String, dynamic>) : null,
  );

  final String id;

  /// `direct` or `group`.
  final String kind;
  final String title;
  final List<ConversationMember> members;
  final DateTime? lastMessageAt;

  /// Only in the list (GET /conversations): what the person has not read,
  /// whether they muted it, and the newest line, cut to its first line.
  final int unread;
  final bool muted;
  final ConversationMessage? last;

  bool get group => kind == 'group';

  /// The members who have not left.
  List<ConversationMember> get active => [
    for (final m in members)
      if (!m.left) m,
  ];

  /// The other side of a direct conversation, seen from the person [meId];
  /// null in a group.
  ConversationMember? other(String meId) => group ? null : active.where((m) => !(m.human && m.id == meId)).firstOrNull;

  /// The agent of a direct conversation with one. That conversation is the
  /// agent's thread and opens as it, as on the web.
  ConversationMember? get directAgent => group ? null : active.where((m) => m.agent).firstOrNull;

  /// The name it goes by: its title, or the other member (the web's
  /// gespraechName).
  String name(String meId) => group ? (title.isEmpty ? '…' : title) : (other(meId)?.name ?? '…');

  /// When anything was last said, for "latest first".
  DateTime get latest => last?.createdAt ?? lastMessageAt ?? DateTime(0);
}

/// A page of GET /conversations/{id}/messages, oldest first.
class ConversationPage {
  ConversationPage({required this.messages, required this.more, required this.pending});

  factory ConversationPage.fromJson(Map<String, dynamic> j) => ConversationPage(
    messages: [
      for (final m in (j['messages'] as List? ?? const [])) ConversationMessage.fromJson(m as Map<String, dynamic>),
    ],
    more: j['more'] as bool? ?? false,
    pending: j['pending'] as bool? ?? false,
  );

  final List<ConversationMessage> messages;

  /// On a first page or a `before` page: there are older ones. On an
  /// `after` page: there are newer ones beyond it.
  final bool more;

  /// A message still waits for a triage's decision.
  final bool pending;
}

/// A person of the organisation, as GET /org/chart lists them.
class OrgHuman {
  OrgHuman({required this.id, required this.displayName, this.email = '', this.jobTitle = '', this.photoId});

  factory OrgHuman.fromJson(Map<String, dynamic> j) => OrgHuman(
    id: j['id'] as String? ?? '',
    displayName: j['display_name'] as String? ?? '',
    email: j['email'] as String? ?? '',
    jobTitle: j['job_title'] as String? ?? '',
    photoId: j['photo_id'] as String?,
  );

  final String id;
  final String displayName;
  final String email;
  final String jobTitle;
  final String? photoId;
}

/// GET /org/chart: the people and the agents of the organisation.
class OrgChart {
  OrgChart({required this.humans, required this.agents});

  factory OrgChart.fromJson(Map<String, dynamic> j) => OrgChart(
    humans: [for (final h in (j['humans'] as List? ?? const [])) OrgHuman.fromJson(h as Map<String, dynamic>)],
    agents: [for (final a in (j['agents'] as List? ?? const [])) Agent.fromJson(a as Map<String, dynamic>)],
  );

  final List<OrgHuman> humans;
  final List<Agent> agents;
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

/// One seat of the person's account (GET /auth/memberships, #417): an
/// organisation they belong to, whether or not the app is paired there.
class Membership {
  Membership({required this.orgId, required this.orgName, required this.role});

  factory Membership.fromJson(Map<String, dynamic> j) => Membership(
    orgId: j['org_id'] as String? ?? '',
    orgName: j['org_name'] as String? ?? '',
    role: j['role'] as String? ?? '',
  );

  final String orgId;
  final String orgName;
  final String role;
}
