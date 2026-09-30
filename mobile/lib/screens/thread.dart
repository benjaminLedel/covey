import 'dart:async';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:intl/intl.dart';

import '../chat_text.dart';
import '../chrome.dart';
import '../api.dart';
import '../call/call.dart';
import '../call/call_view.dart';
import '../live.dart';
import '../diagnostics.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../mention.dart';
import '../models.dart';
import '../photo.dart';
import '../theme.dart';
import '../ui.dart';

/// One agent, the conversation with it — or, since #440, any conversation:
/// a group, or a direct one with a colleague ([ThreadScreen.conversation]).
///
/// The compose box does what the web's does: a new message hands work over,
/// and an answer goes to the question it answers — chosen at the question,
/// not guessed from whichever task happens to be parked. Two intentions, two
/// places, the same rule as the web shell (#298).
///
/// A conversation reads through the conversation API and, once it has its
/// first page, only what came after the newest message it holds (#447) —
/// an open conversation that did not move costs a 304, not a page. Its
/// speakers are its members, each named at what they said; the direct
/// conversation with one agent stays the agent's thread, as on the web,
/// because it carries the agent's work around it.
class ThreadScreen extends StatefulWidget {
  const ThreadScreen({
    super.key,
    required this.api,
    required this.agentId,
    required this.agentName,
    required this.me,
    this.agentSlug = '',
    this.faceState = FaceState.working,
    this.pick,
  }) : conversation = null;

  const ThreadScreen.conversation({
    super.key,
    required this.api,
    required Conversation this.conversation,
    required this.me,
  }) : agentId = '',
       agentName = '',
       agentSlug = '',
       faceState = FaceState.working,
       pick = null;

  /// The conversation this screen reads; null for an agent's thread.
  final Conversation? conversation;

  /// Swapped in tests, so attaching needs no file dialog.
  final Future<List<Attachment>> Function(FileType type)? pick;

  final CoveyApi api;
  final String agentId;
  final String agentName;

  /// For the face in the header (#337); without a slug there is none.
  final String agentSlug;
  final FaceState faceState;
  final Me me;

  @override
  State<ThreadScreen> createState() => _ThreadScreenState();
}

class _ThreadScreenState extends State<ThreadScreen> {
  bool get _stopped => widget.faceState == FaceState.killed;

  bool get _isConversation => widget.conversation != null;

  /// The conversation as last read: members and title. Starts as what the
  /// list handed over.
  late Conversation? _conv = widget.conversation;
  DateTime _convAt = DateTime(0);

  /// The conversation's messages held so far, oldest first, and whether
  /// there are older ones than the first.
  final _messages = <ConversationMessage>[];
  bool _older = false;
  bool _loadingOlder = false;

  final _text = TextEditingController();
  final _scroll = ScrollController();
  Thread? _thread;
  Object? _error;
  ThreadEntry? _answering;
  final _files = <Attachment>[];
  bool _sending = false;
  Timer? _poll;
  StreamSubscription<void>? _live;

  @override
  void initState() {
    super.initState();
    _load();
    // No event stream yet: while the thread is open it asks again. The web
    // gets the same news over SSE; a phone that holds a stream open in the
    // background is a battery question for a later slice.
    // This agent's events from the instance (#419): a message answered, a
    // task moved, the triage thinking. The timer is the net under it.
    _live =
        (_isConversation
                ? LiveEvents.instance.of({'chat'}, conversationId: widget.conversation!.id)
                : LiveEvents.instance.of({'chat', 'task'}, agentId: widget.agentId))
            .listen((_) => _load());
    _poll = Timer.periodic(const Duration(minutes: 1), (_) => _load());
    CallSettings.enabled.addListener(_callSettingChanged);
  }

  void _callSettingChanged() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    CallSettings.enabled.removeListener(_callSettingChanged);
    _poll?.cancel();
    _live?.cancel();
    _text.dispose();
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final th = _isConversation ? await _loadConversation() : await widget.api.thread(widget.agentId);
      if (!mounted) return;
      _markRead(th);
      setState(() {
        _thread = th;
        _error = null;
        // The question may have been answered elsewhere in the meantime.
        if (_answering != null && !th.entries.any((e) => e.id == _answering!.id && e.isOpenQuestion)) {
          _answering = null;
        }
      });
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  /// A conversation's read: the members now and then (a group changes
  /// while it is open), the newest page the first time, and afterwards only
  /// what came after the newest message held — page by page while the
  /// instance says there is more.
  Future<Thread> _loadConversation() async {
    final id = widget.conversation!.id;
    if (DateTime.now().difference(_convAt) > const Duration(seconds: 30)) {
      _conv = await widget.api.conversation(id);
      _convAt = DateTime.now();
    }
    var pending = false;
    if (_messages.isEmpty) {
      final page = await widget.api.conversationMessages(id);
      _merge(page.messages);
      _older = page.more;
      pending = page.pending;
    } else {
      for (var i = 0; i < 20; i++) {
        final page = await widget.api.conversationMessages(id, after: _messages.last.id);
        _merge(page.messages);
        pending = page.pending;
        if (!page.more || page.messages.isEmpty) break;
      }
    }
    return _asThread(pending);
  }

  /// Takes messages in by their id: a delta overlaps what is held, and a
  /// message just sent is held before the next read brings it.
  void _merge(Iterable<ConversationMessage> incoming) {
    final known = {for (final m in _messages) m.id};
    for (final m in incoming) {
      if (known.add(m.id)) _messages.add(m);
    }
    _messages.sort((a, b) => (a.createdAt ?? DateTime(0)).compareTo(b.createdAt ?? DateTime(0)));
  }

  Thread _asThread(bool pending) {
    final members = {for (final m in _conv?.members ?? const <ConversationMember>[]) '${m.kind}:${m.id}': m};
    return Thread(
      entries: [
        for (final m in _messages)
          ThreadEntry.fromMessage(m, meId: widget.me.id, slug: members['${m.authorKind}:${m.authorId}']?.slug ?? ''),
      ],
      pending: pending,
    );
  }

  /// The page before the oldest message held.
  Future<void> _loadOlder() async {
    if (_loadingOlder || _messages.isEmpty) return;
    setState(() => _loadingOlder = true);
    try {
      final page = await widget.api.conversationMessages(widget.conversation!.id, before: _messages.first);
      _merge(page.messages);
      if (mounted) {
        setState(() {
          _older = page.more;
          _thread = _asThread(_thread?.pending ?? false);
        });
      }
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e);
    } finally {
      if (mounted) setState(() => _loadingOlder = false);
    }
  }

  DateTime? _readUpTo;

  /// What is on the screen has been read (#378): up to the newest entry
  /// shown, not up to now — an answer arriving meanwhile stays unread.
  void _markRead(Thread th) {
    final newest = th.entries
        .map((e) => e.at)
        .nonNulls
        .fold<DateTime?>(null, (a, b) => a == null || b.isAfter(a) ? b : a);
    if (newest == null || (_readUpTo != null && !newest.isAfter(_readUpTo!))) return;
    _readUpTo = newest;
    final mark = _isConversation
        ? widget.api.markConversationRead(widget.conversation!.id, newest)
        : widget.api.markThreadRead(widget.agentId, newest);
    mark
        .then<void>((_) {
          threadsRead.value++;
        })
        .catchError((Object e) {
          diag('thread', 'not marked read: $e');
        });
  }

  /// Picks photos, videos or files to go with the next message (#340).
  Future<void> _attach() async {
    final t = Strings.of(context).t;
    final type = await showModalBottomSheet<FileType>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.only(bottom: 16),
          child: InsetGroup(
            dividerIndent: 16,
            children: [
              GroupRow(title: t('mobile.anhangFoto'), onTap: () => Navigator.pop(context, FileType.media)),
              GroupRow(title: t('mobile.anhangDatei'), onTap: () => Navigator.pop(context, FileType.any)),
            ],
          ),
        ),
      ),
    );
    if (type == null) return;
    final picked = await (widget.pick ?? _pickFiles)(type);
    if (picked.isEmpty || !mounted) return;
    setState(() => _files.addAll(picked));
  }

  static Future<List<Attachment>> _pickFiles(FileType type) async {
    final files = await FilePicker.pickFiles(type: type);
    return [for (final f in files) Attachment(name: f.name, length: f.lengthSync(), open: () => f.readAsByteStream())];
  }

  Future<void> _send() async {
    var text = _text.text.trim();
    // Files alone are a message too: the line that names them is its text.
    if ((text.isEmpty && _files.isEmpty) || _sending) return;
    final messenger = ScaffoldMessenger.of(context);
    final t = Strings.of(context).t;
    setState(() => _sending = true);
    try {
      // First the files, then the message that names them: a message that
      // points at a file which never arrived would send the agent looking.
      if (_files.isNotEmpty) {
        final paths = await widget.api.uploadToInbox(widget.agentId, _files);
        final line = '(${t('team.anhangZeile', args: {'pfade': paths.join(', ')})})';
        text = text.isEmpty ? line : '$text\n\n$line';
      }
      final q = _answering;
      if (q != null) {
        final woken = await widget.api.reply(q.taskId!, text);
        // Not an error, and not retried: nobody was waiting (spec/27).
        if (!woken) messenger.showSnackBar(SnackBar(content: Text(t('mobile.nichtGeweckt'))));
      } else if (_isConversation) {
        final sent = await widget.api.postConversationMessage(widget.conversation!.id, text);
        _merge([sent]);
      } else {
        await widget.api.send(widget.agentId, text);
      }
      _text.clear();
      setState(() {
        _answering = null;
        _files.clear();
      });
      await _load();
    } on ApiException catch (e) {
      // The instance decides what a role may do; the app says what it heard.
      // A 403 has two causes on this route, and they need different words:
      // the organisation has the team surface off, or the role may not write.
      // In a conversation a 403 says its own reason — the organisation's
      // reach, a group's rules (#440) — and is shown as it is.
      final text = e.status != 403
          ? e.message
          : e.message.contains('team surface')
          ? t('mobile.teamAusKurz')
          : _isConversation
          ? e.message
          : t('chat.readOnly');
      messenger.showSnackBar(SnackBar(content: Text(text)));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  /// Whether this thread offers a call (#494): the direct conversation with
  /// an agent that is not stopped, on the Mac, with the trial switched on.
  bool get _canCall =>
      !_isConversation &&
      !_stopped &&
      widget.me.teamSurface &&
      widget.agentId.isNotEmpty &&
      CallSettings.supported &&
      CallSettings.enabled.value;

  void _call() {
    if (!_canCall) return;
    CallScreen.open(
      context,
      api: widget.api,
      agentId: widget.agentId,
      agentName: widget.agentName,
      agentSlug: widget.agentSlug,
    );
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final th = _thread;
    // Newest at the bottom, where the thumb and the compose box are: the list
    // is drawn reversed.
    final entries = th?.entries.reversed.toList() ?? const <ThreadEntry>[];
    final conv = _conv;
    final meId = widget.me.id;
    final agents = conv?.active.where((m) => m.agent).toList() ?? const <ConversationMember>[];
    // Who is thinking while a message waits for its decision: the agent of
    // the thread, or in a conversation the one agent in it.
    final (thinkerName, thinkerSlug) = !_isConversation
        ? (widget.agentName, widget.agentSlug)
        : agents.length == 1
        ? (agents.single.name, agents.single.slug)
        : ('', '');
    final older = _isConversation && _older && entries.isNotEmpty;
    final scaffold = Scaffold(
      appBar: ChromeAppBar(
        actions: [
          if (_canCall)
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: IconButton(
                key: const ValueKey('call'),
                tooltip: context.t('call.button', args: {'name': widget.agentName}),
                icon: Icon(AppIcons.call.of(context)),
                onPressed: _call,
              ),
            ),
        ],
        // Beside a back control the face follows it directly; without one
        // (the detail pane of a wide window) it keeps the content margin.
        titleSpacing: (ModalRoute.of(context)?.canPop ?? false) ? 0 : 16,
        title: _isConversation
            ? _ConversationHead(api: widget.api, conversation: conv!, meId: meId)
            : Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (widget.agentSlug.isNotEmpty) ...[
                    Face(slug: widget.agentSlug, state: widget.faceState, size: 34),
                    const SizedBox(width: 12),
                  ],
                  Flexible(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(widget.agentName, overflow: TextOverflow.ellipsis, style: context.type.titleMedium),
                        // The state in words under the name, as a messenger
                        // says "online": the face shows it, the word says it.
                        Text(
                          context.t(switch (widget.faceState) {
                            FaceState.killed => 'status.killed',
                            FaceState.sleeping => 'status.sleeping',
                            FaceState.working => 'status.working',
                          }),
                          style: context.type.labelSmall,
                        ),
                      ],
                    ),
                  ),
                ],
              ),
      ),
      body: SafeArea(
        child: Column(
          children: [
            if (_error != null)
              Padding(
                padding: const EdgeInsets.all(12),
                child: Text(
                  context.t('mobile.fehler', args: {'error': '$_error'}),
                  style: context.type.bodyMedium?.copyWith(color: c.textDanger),
                ),
              ),
            Expanded(
              child: th == null
                  ? Center(child: Text(context.t('common.loading')))
                  : entries.isEmpty
                  ? (_isConversation
                        ? _EmptyConversation(hint: conv!.group && agents.isNotEmpty)
                        : _Empty(name: widget.agentName, slug: widget.agentSlug, state: widget.faceState))
                  : LayoutBuilder(
                      // A reading width on a wide pane, centred (#397).
                      builder: (context, box) {
                        final side = ((box.maxWidth - 760) / 2).clamp(14.0, double.infinity);
                        return ListView.builder(
                          controller: _scroll,
                          reverse: true,
                          padding: EdgeInsets.fromLTRB(side, 12, side, 4),
                          itemCount: entries.length + (th.pending ? 1 : 0) + (older ? 1 : 0),
                          itemBuilder: (context, i) {
                            if (th.pending && i == 0) {
                              return Padding(
                                padding: const EdgeInsets.fromLTRB(4, 8, 4, 8),
                                child: Row(
                                  children: [
                                    if (thinkerSlug.isNotEmpty) ...[
                                      Face(slug: thinkerSlug, size: 22),
                                      const SizedBox(width: 8),
                                    ],
                                    Text(
                                      thinkerName.isEmpty ? '…' : '$thinkerName ${context.t('team.arbeitetGerade')}',
                                      style: context.type.bodySmall,
                                    ),
                                  ],
                                ),
                              );
                            }
                            // The oldest end of the list: the page before it,
                            // on request.
                            if (older && i == entries.length + (th.pending ? 1 : 0)) {
                              return Padding(
                                padding: const EdgeInsets.symmetric(vertical: 8),
                                child: Center(
                                  child: TextButton(
                                    onPressed: _loadingOlder ? null : _loadOlder,
                                    child: Text(context.t('conversation.older')),
                                  ),
                                ),
                              );
                            }
                            final k = i - (th.pending ? 1 : 0);
                            final e = entries[k];
                            // entries is newest-first; the line before this one
                            // in time is the next one in the list.
                            final earlier = k + 1 < entries.length ? entries[k + 1] : null;
                            final newDay = earlier == null || !_sameDay(earlier.at, e.at);
                            // A run (#397): the same speaker again within five
                            // minutes, same day, in plain conversation. Only its
                            // first entry carries the head.
                            bool plain(ThreadEntry x) => x.kind == 'message' || x.kind == 'answer' || x.kind == 'note';
                            final continues =
                                earlier != null &&
                                !newDay &&
                                plain(e) &&
                                plain(earlier) &&
                                earlier.author == e.author &&
                                e.at != null &&
                                earlier.at != null &&
                                e.at!.difference(earlier.at!) < const Duration(minutes: 5);
                            final line = _Line(
                              entry: e,
                              first: !continues,
                              agentName: widget.agentName,
                              avatar: _avatarOf(e),
                              showTask: earlier == null || earlier.taskId != e.taskId || earlier.fromPerson,
                              selected: _answering?.id == e.id,
                              onAnswer: e.isOpenQuestion && widget.me.teamSurface && !_stopped
                                  ? () => setState(() => _answering = _answering?.id == e.id ? null : e)
                                  : null,
                            );
                            if (!newDay || e.at == null) return line;
                            return Column(
                              crossAxisAlignment: CrossAxisAlignment.stretch,
                              children: [
                                _DaySeparator(at: e.at!),
                                line,
                              ],
                            );
                          },
                        );
                      },
                    ),
            ),
            // A stopped agent takes no messages (#414): the server refuses
            // them, and the composer gives way to a sentence saying why.
            if (_stopped)
              SafeArea(
                top: false,
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(20, 14, 20, 18),
                  child: Text(
                    context.t('team.gestopptHinweis', args: {'name': widget.agentName}),
                    textAlign: TextAlign.center,
                    style: context.type.bodyMedium?.copyWith(color: context.colors.textDanger),
                  ),
                ),
              )
            else
              _Composer(
                controller: _text,
                mentions: conv == null ? const [] : mentionCandidates(conv, meId),
                hint: !_isConversation
                    ? null
                    : conv!.group && agents.isNotEmpty
                    ? context.t('conversation.placeholderGroup')
                    : context.t('conversation.placeholder'),
                answering: _answering,
                sending: _sending,
                // Off, the instance refuses a new message (#328). A reply to a
                // parked question still goes through — it acts on a task that
                // exists either way — but it is chosen at the question, and the
                // question offers it only while the surface is on.
                enabled: widget.me.teamSurface,
                onCancelAnswer: () => setState(() => _answering = null),
                onSend: _send,
                files: _files,
                // Files go into an agent's home; a conversation has none.
                onAttach: _isConversation ? null : _attach,
                onRemove: (f) => setState(() => _files.remove(f)),
              ),
          ],
        ),
      ),
    );
    if (!_canCall) return scaffold;
    // ⌘⇧C calls, as the button does.
    return CallbackShortcuts(
      bindings: {const SingleActivator(LogicalKeyboardKey.keyC, meta: true, shift: true): _call},
      child: scaffold,
    );
  }

  /// The picture beside the first line of a run: the thread's agent, or in
  /// a conversation whoever spoke — an agent's face, a person's monogram.
  Widget? _avatarOf(ThreadEntry e) {
    if (!_isConversation) {
      return widget.agentSlug.isEmpty ? null : Face(slug: widget.agentSlug, state: widget.faceState, size: 28);
    }
    if (e.speakerHuman) {
      return PersonPhoto(api: widget.api, humanId: e.speakerId, photoId: null, name: e.speaker, size: 28);
    }
    if (e.speakerSlug.isNotEmpty) return Face(slug: e.speakerSlug, size: 28);
    return null;
  }
}

/// A conversation's header (#440): a group's mark, its name and how many
/// are in it; a colleague's monogram, name and address.
class _ConversationHead extends StatelessWidget {
  const _ConversationHead({required this.api, required this.conversation, required this.meId});

  final CoveyApi api;
  final Conversation conversation;
  final String meId;

  @override
  Widget build(BuildContext context) {
    final c = conversation;
    final other = c.other(meId);
    final line = c.group
        ? context.t('conversation.membersCount', args: {'count': c.active.length})
        : (other?.email ?? '');
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (c.group)
          const GroupMark(size: 34)
        else if (other != null && other.agent && other.slug.isNotEmpty)
          Face(slug: other.slug, size: 34)
        else if (other != null)
          PersonPhoto(api: api, humanId: other.id, photoId: null, name: other.name, size: 34),
        const SizedBox(width: 12),
        Flexible(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(c.name(meId), overflow: TextOverflow.ellipsis, style: context.type.titleMedium),
              if (line.isNotEmpty) Text(line, overflow: TextOverflow.ellipsis, style: context.type.labelSmall),
            ],
          ),
        ),
      ],
    );
  }
}

/// A conversation nobody has said anything in yet; in a group with agents,
/// how one addresses them.
class _EmptyConversation extends StatelessWidget {
  const _EmptyConversation({required this.hint});

  final bool hint;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(context.t('conversation.emptyThread'), textAlign: TextAlign.center, style: context.type.titleLarge),
            if (hint) ...[
              const SizedBox(height: 8),
              Text(context.t('conversation.agentHint'), textAlign: TextAlign.center, style: context.type.bodyMedium),
            ],
          ],
        ),
      ),
    );
  }
}

class _Empty extends StatelessWidget {
  const _Empty({required this.name, required this.slug, required this.state});

  final String name;
  final String slug;
  final FaceState state;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (slug.isNotEmpty) ...[Face(slug: slug, state: state, size: 72), const SizedBox(height: 18)],
            Text(
              context.t('team.leerTitel', args: {'name': name}),
              textAlign: TextAlign.center,
              style: context.type.titleLarge,
            ),
            const SizedBox(height: 8),
            Text(context.t('team.leerText'), textAlign: TextAlign.center, style: context.type.bodyMedium),
          ],
        ),
      ),
    );
  }
}

bool _sameDay(DateTime? a, DateTime? b) =>
    a != null && b != null && a.year == b.year && a.month == b.month && a.day == b.day;

/// Where the day changes (#397): today, yesterday, or the date.
class _DaySeparator extends StatelessWidget {
  const _DaySeparator({required this.at});

  final DateTime at;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final now = DateTime.now();
    final label = _sameDay(at, now)
        ? context.t('mobile.heute')
        : _sameDay(at, now.subtract(const Duration(days: 1)))
        ? context.t('team.gestern')
        : DateFormat.yMMMMd(Strings.of(context).language).format(at);
    return Padding(
      padding: const EdgeInsets.only(top: 14, bottom: 6),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 3),
          decoration: BoxDecoration(
            color: c.textPrimary.withValues(alpha: 0.05),
            borderRadius: BorderRadius.circular(99),
          ),
          child: Text(label, style: context.type.labelSmall?.copyWith(color: c.textSecondary)),
        ),
      ),
    );
  }
}

class _Line extends StatelessWidget {
  const _Line({
    required this.entry,
    required this.selected,
    required this.first,
    required this.agentName,
    this.avatar,
    this.onAnswer,
    this.showTask = true,
  });

  final ThreadEntry entry;
  final bool selected;
  final VoidCallback? onAnswer;

  /// Whether this line opens a run (#397): it carries the head and the tail
  /// corner; the lines after it follow closely.
  final bool first;
  final String agentName;

  /// Beside the first line of a run from the other side: the agent's face,
  /// or in a conversation the speaker's (#440). Null draws none.
  final Widget? avatar;

  /// Whether this line opens a run of its task's lines. The task is named
  /// once where its run starts; the lines after it follow bare.
  final bool showTask;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final mine = entry.fromPerson;
    final question = entry.kind == 'question';
    final error = entry.kind == 'error';
    final result = entry.kind == 'result';
    // A configuration change drafted from the chat (#491): the app shows its
    // title and rationale; the card with the diff and the decision is the
    // web's.
    final proposal = entry.kind == 'config_proposal';
    final emoji = emojiOnly(entry.text) && !question;
    // The person's lines are ink on the sheet, the agent's are white paper:
    // two sides told apart by weight, not by a second colour. A question is
    // the one line that asks for something, so it gets the waiting tone and
    // its own answer button.
    final bg = mine ? c.textPrimary : (question ? c.bgWait : c.surface2);
    final fg = mine ? c.surface2 : (error ? c.textDanger : (question ? c.textWait : c.textPrimary));
    const r = Radius.circular(20);
    const tight = Radius.circular(6);
    final time = entry.at == null ? '' : DateFormat.Hm(Strings.of(context).language).format(entry.at!);
    final kindLabel = question
        ? context.t('chat.kind.question')
        // A told result reads as what it is, a message (#411).
        : result && entry.said.isEmpty
        ? context.t('chat.kind.result')
        : error
        ? context.t('chat.kind.error')
        : proposal
        ? context.t('chatProposal.inApp')
        : null;

    // The head of a run: who speaks, the kind where it means something, and
    // when; one's own lines need only the time.
    final head = !first
        ? null
        : Padding(
            padding: EdgeInsets.only(bottom: 4, left: mine ? 0 : 2, right: mine ? 4 : 0),
            child: Row(
              mainAxisAlignment: mine ? MainAxisAlignment.end : MainAxisAlignment.start,
              children: [
                if (!mine) ...[
                  Text(
                    entry.speaker.isNotEmpty ? entry.speaker : agentName,
                    style: context.type.labelMedium?.copyWith(color: c.textSecondary),
                  ),
                  if (kindLabel != null) ...[
                    const SizedBox(width: 6),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 1),
                      decoration: BoxDecoration(
                        color: question || proposal
                            ? c.bgWait
                            : (result ? c.textSuccess.withValues(alpha: 0.12) : c.textDanger.withValues(alpha: 0.1)),
                        borderRadius: BorderRadius.circular(99),
                      ),
                      child: Text(
                        kindLabel,
                        style: context.type.labelSmall?.copyWith(
                          color: question || proposal ? c.textWait : (result ? c.textSuccess : c.textDanger),
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ],
                  const SizedBox(width: 6),
                ],
                Text(
                  time,
                  style: context.type.labelSmall?.copyWith(fontFeatures: const [FontFeature.tabularFigures()]),
                ),
              ],
            ),
          );

    final content = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // The task a line belongs to, in words — its state is said, not
        // only coloured.
        if (showTask && entry.taskTitle.isNotEmpty && entry.kind != 'message')
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Text(
              '${entry.taskTitle} · ${context.t('status.${entry.taskState}')}',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: context.type.labelSmall?.copyWith(color: mine ? c.surface2.withValues(alpha: 0.75) : null),
            ),
          ),
        if (emoji)
          Text(entry.text.trim(), style: const TextStyle(fontSize: 38, height: 1.15))
        else
          ChatText(entry.said.isNotEmpty ? entry.said : entry.text, color: fg),
        // The report the sentence was told from, one tap away (#411).
        if (entry.said.isNotEmpty) _Report(entry.text),
        if (onAnswer != null) ...[
          const SizedBox(height: 10),
          FilledButton(
            onPressed: onAnswer,
            style: FilledButton.styleFrom(
              minimumSize: const Size(44, 40),
              padding: const EdgeInsets.symmetric(horizontal: 18),
              backgroundColor: c.textWait,
              foregroundColor: c.surface2,
              shape: const StadiumBorder(),
            ),
            child: Text(context.t('chat.answer')),
          ),
        ],
      ],
    );

    return LayoutBuilder(
      builder: (context, box) {
        final paneWidth = box.maxWidth;
        final bubble = emoji
            ? Padding(padding: const EdgeInsets.symmetric(horizontal: 4), child: content)
            : Container(
                padding: const EdgeInsets.fromLTRB(15, 10, 15, 10),
                decoration: BoxDecoration(
                  color: bg,
                  // The first bubble of a run points at its speaker: the
                  // corner nearest them tightens — the tail, without one.
                  borderRadius: BorderRadius.only(
                    topLeft: first && !mine ? tight : r,
                    topRight: first && mine ? tight : r,
                    bottomLeft: r,
                    bottomRight: r,
                  ),
                  border: selected ? Border.all(color: c.textWait, width: 1.6) : null,
                ),
                child: SelectionArea(child: content),
              );
        return Padding(
          padding: EdgeInsets.only(top: first ? 12 : 3),
          child: Row(
            mainAxisAlignment: mine ? MainAxisAlignment.end : MainAxisAlignment.start,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // The agent's face beside the first line of a run; the column
              // stays where none is drawn, so a run lines up.
              if (!mine && avatar != null) ...[
                SizedBox(
                  width: 28,
                  child: first ? Padding(padding: const EdgeInsets.only(top: 20), child: avatar) : null,
                ),
                const SizedBox(width: 8),
              ],
              ConstrainedBox(
                // Measured against the pane, not the window, and capped: a
                // bubble wider than ~70 characters is a paragraph nobody
                // reads to the end.
                constraints: BoxConstraints(maxWidth: (paneWidth * 0.78).clamp(0, 560)),
                child: Column(
                  crossAxisAlignment: mine ? CrossAxisAlignment.end : CrossAxisAlignment.start,
                  children: [?head, bubble],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

/// The compose box: a floating capsule like the space capsule, the field and
/// a round send button inside, and above it — when an answer is being
/// written — the question it answers.
class _Composer extends StatelessWidget {
  const _Composer({
    required this.controller,
    required this.answering,
    required this.sending,
    required this.enabled,
    required this.onCancelAnswer,
    required this.onSend,
    required this.files,
    required this.onAttach,
    required this.onRemove,
    this.mentions = const [],
    this.hint,
  });

  final TextEditingController controller;

  /// Who "@" offers (#440): a group's other members; empty elsewhere.
  final List<MentionCandidate> mentions;

  /// The field's hint where it is not the agent thread's.
  final String? hint;
  final ThreadEntry? answering;
  final bool sending;
  final bool enabled;
  final VoidCallback onCancelAnswer;
  final VoidCallback onSend;
  final List<Attachment> files;

  /// Null where nothing can be attached: the button is not drawn.
  final VoidCallback? onAttach;
  final ValueChanged<Attachment> onRemove;

  /// Puts the handle in for the "@…" the caret stands in.
  void _mention(MentionCandidate k) {
    final v = controller.value;
    final open = openMention(v.text, v.selection.baseOffset);
    if (open == null) return;
    final out = insertMention(v.text, v.selection.baseOffset, open.start, k.handle);
    controller.value = TextEditingValue(
      text: out.text,
      selection: TextSelection.collapsed(offset: out.caret),
    );
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 6, 12, 10),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (answering != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 0, 0, 4),
              child: Row(
                children: [
                  Icon(AppIcons.reply.of(context), size: 16, color: c.textWait),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      context.t('chat.answering', args: {'title': answering!.taskTitle}),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: context.type.labelMedium?.copyWith(color: c.textWait),
                    ),
                  ),
                  IconButton(
                    onPressed: onCancelAnswer,
                    icon: Icon(AppIcons.close.of(context), size: 18),
                    tooltip: context.t('team.abbrechen'),
                  ),
                ],
              ),
            ),
          // "@" in a group: the members that match what follows it, a tap
          // puts the handle in.
          if (mentions.isNotEmpty)
            ValueListenableBuilder<TextEditingValue>(
              valueListenable: controller,
              builder: (context, v, _) {
                final open = v.selection.isCollapsed ? openMention(v.text, v.selection.baseOffset) : null;
                final hits = open == null ? const <MentionCandidate>[] : mentionMatches(mentions, open.query);
                if (hits.isEmpty) return const SizedBox.shrink();
                return Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: Semantics(
                    container: true,
                    label: context.t('conversation.mentionList'),
                    child: Material(
                      color: c.surface2,
                      borderRadius: BorderRadius.circular(18),
                      clipBehavior: Clip.antiAlias,
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          for (final k in hits)
                            InkWell(
                              onTap: () => _mention(k),
                              child: ConstrainedBox(
                                constraints: const BoxConstraints(minHeight: 44),
                                child: Padding(
                                  padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
                                  child: Row(
                                    children: [
                                      if (k.member.agent && k.member.slug.isNotEmpty)
                                        Face(slug: k.member.slug, size: 24)
                                      else
                                        PersonPhoto(
                                          api: null,
                                          humanId: k.member.id,
                                          photoId: null,
                                          name: k.name,
                                          size: 24,
                                        ),
                                      const SizedBox(width: 10),
                                      Expanded(
                                        child: Text(
                                          k.name,
                                          maxLines: 1,
                                          overflow: TextOverflow.ellipsis,
                                          style: context.type.bodyLarge,
                                        ),
                                      ),
                                      Text('@${k.handle}', style: context.type.labelSmall),
                                    ],
                                  ),
                                ),
                              ),
                            ),
                        ],
                      ),
                    ),
                  ),
                );
              },
            ),
          // What goes with the message, removable until it is sent.
          if (files.isNotEmpty)
            Padding(
              padding: const EdgeInsets.fromLTRB(6, 0, 6, 8),
              child: Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (final f in files)
                    InputChip(
                      label: Text(f.name, overflow: TextOverflow.ellipsis),
                      avatar: Icon(AppIcons.attach.of(context), size: 16),
                      onDeleted: () => onRemove(f),
                      deleteButtonTooltipMessage: context.t('team.anhangEntfernen', args: {'name': f.name}),
                      backgroundColor: c.surface2,
                      side: BorderSide(color: c.hairline),
                      shape: const StadiumBorder(),
                    ),
                ],
              ),
            ),
          Glass(
            radius: 28,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(6, 5, 5, 5),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  if (onAttach != null)
                    SizedBox.square(
                      dimension: 46,
                      child: IconButton(
                        onPressed: enabled && !sending ? onAttach : null,
                        icon: Icon(AppIcons.attach.of(context), color: c.textSecondary),
                        tooltip: context.t('team.anhaengen'),
                      ),
                    )
                  else
                    const SizedBox(width: 12),
                  Expanded(
                    child: Focus(
                      // On the desktop Enter sends and Shift+Enter breaks the
                      // line, as in every chat there (#374). A word an input
                      // method is still composing keeps its Enter.
                      canRequestFocus: false,
                      skipTraversal: true,
                      onKeyEvent: MacChrome.active
                          ? (_, event) {
                              if (event is! KeyDownEvent ||
                                  (event.logicalKey != LogicalKeyboardKey.enter &&
                                      event.logicalKey != LogicalKeyboardKey.numpadEnter) ||
                                  HardwareKeyboard.instance.isShiftPressed ||
                                  controller.value.composing.isValid) {
                                return KeyEventResult.ignored;
                              }
                              if (enabled && !sending) onSend();
                              return KeyEventResult.handled;
                            }
                          : null,
                      child: TextField(
                        controller: controller,
                        enabled: enabled,
                        minLines: 1,
                        maxLines: 6,
                        textCapitalization: TextCapitalization.sentences,
                        style: context.type.bodyLarge,
                        decoration: InputDecoration(
                          filled: false,
                          border: InputBorder.none,
                          enabledBorder: InputBorder.none,
                          focusedBorder: InputBorder.none,
                          disabledBorder: InputBorder.none,
                          contentPadding: const EdgeInsets.fromLTRB(2, 12, 8, 12),
                          hintText: !enabled
                              ? context.t('mobile.teamAusKurz')
                              : answering != null
                              ? context.t('chat.placeholderAnswer')
                              : hint ?? context.t('chat.placeholder'),
                        ),
                      ),
                    ),
                  ),
                  SizedBox.square(
                    dimension: 46,
                    child: IconButton.filled(
                      onPressed: enabled && !sending ? onSend : null,
                      style: IconButton.styleFrom(backgroundColor: c.textPrimary, foregroundColor: c.surface2),
                      icon: Icon(AppIcons.send.of(context)),
                      tooltip: answering != null ? context.t('chat.answer') : context.t('chat.send'),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// The report under a told result (#411): closed until tapped, so the
/// conversation reads as one, and nobody has to take the sentence on trust.
class _Report extends StatefulWidget {
  const _Report(this.text);
  final String text;

  @override
  State<_Report> createState() => _ReportState();
}

class _ReportState extends State<_Report> {
  var _open = false;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SizedBox(height: 6),
        Semantics(
          button: true,
          expanded: _open,
          child: InkWell(
            onTap: () => setState(() => _open = !_open),
            borderRadius: BorderRadius.circular(6),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  AnimatedRotation(
                    turns: _open ? 0.25 : 0,
                    duration: const Duration(milliseconds: 150),
                    child: Icon(Icons.chevron_right_rounded, size: 18, color: c.textMuted),
                  ),
                  Text(context.t('chat.bericht'), style: context.type.labelMedium?.copyWith(color: c.textMuted)),
                ],
              ),
            ),
          ),
        ),
        if (_open)
          Container(
            margin: const EdgeInsets.only(top: 6),
            padding: const EdgeInsets.only(left: 10),
            decoration: BoxDecoration(
              border: Border(left: BorderSide(color: c.border, width: 2)),
            ),
            child: ChatText(widget.text, color: c.textSecondary),
          ),
      ],
    );
  }
}
