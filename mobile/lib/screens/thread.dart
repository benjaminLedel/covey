import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../theme.dart';
import '../ui.dart';

/// One agent, the conversation with it.
///
/// The compose box does what the web's does: a new message hands work over,
/// and an answer goes to the question it answers — chosen at the question,
/// not guessed from whichever task happens to be parked. Two intentions, two
/// places, the same rule as the web shell (#298).
class ThreadScreen extends StatefulWidget {
  const ThreadScreen({
    super.key,
    required this.api,
    required this.agentId,
    required this.agentName,
    required this.me,
    this.agentSlug = '',
    this.faceState = FaceState.working,
  });

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
  final _text = TextEditingController();
  final _scroll = ScrollController();
  Thread? _thread;
  Object? _error;
  ThreadEntry? _answering;
  bool _sending = false;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _load();
    // No event stream yet: while the thread is open it asks again. The web
    // gets the same news over SSE; a phone that holds a stream open in the
    // background is a battery question for a later slice.
    _poll = Timer.periodic(const Duration(seconds: 8), (_) => _load());
  }

  @override
  void dispose() {
    _poll?.cancel();
    _text.dispose();
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final th = await widget.api.thread(widget.agentId);
      if (!mounted) return;
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

  Future<void> _send() async {
    final text = _text.text.trim();
    if (text.isEmpty || _sending) return;
    final messenger = ScaffoldMessenger.of(context);
    final t = Strings.of(context).t;
    setState(() => _sending = true);
    try {
      final q = _answering;
      if (q != null) {
        final woken = await widget.api.reply(q.taskId!, text);
        // Not an error, and not retried: nobody was waiting (spec/27).
        if (!woken) messenger.showSnackBar(SnackBar(content: Text(t('mobile.nichtGeweckt'))));
      } else {
        await widget.api.send(widget.agentId, text);
      }
      _text.clear();
      setState(() => _answering = null);
      await _load();
    } on ApiException catch (e) {
      // The instance decides what a role may do; the app says what it heard.
      messenger.showSnackBar(SnackBar(content: Text(e.status == 403 ? t('chat.readOnly') : e.message)));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final th = _thread;
    // Newest at the bottom, where the thumb and the compose box are: the list
    // is drawn reversed.
    final entries = th?.entries.reversed.toList() ?? const <ThreadEntry>[];
    return Scaffold(
      appBar: AppBar(
        // Beside a back control the face follows it directly; without one
        // (the detail pane of a wide window) it keeps the content margin.
        titleSpacing: (ModalRoute.of(context)?.canPop ?? false) ? 0 : 16,
        title: Row(
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
                  // The state in words under the name, as a messenger says
                  // "online": the face shows it, the word says it.
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
                  ? _Empty(name: widget.agentName, slug: widget.agentSlug, state: widget.faceState)
                  : ListView.builder(
                      controller: _scroll,
                      reverse: true,
                      padding: const EdgeInsets.fromLTRB(14, 12, 14, 4),
                      itemCount: entries.length + (th.pending ? 1 : 0),
                      itemBuilder: (context, i) {
                        if (th.pending && i == 0) {
                          return Padding(
                            padding: const EdgeInsets.fromLTRB(4, 8, 4, 8),
                            child: Row(
                              children: [
                                if (widget.agentSlug.isNotEmpty) ...[
                                  Face(slug: widget.agentSlug, size: 22),
                                  const SizedBox(width: 8),
                                ],
                                Text(
                                  '${widget.agentName} ${context.t('team.arbeitetGerade')}',
                                  style: context.type.bodySmall,
                                ),
                              ],
                            ),
                          );
                        }
                        final k = i - (th.pending ? 1 : 0);
                        final e = entries[k];
                        // entries is newest-first; the line before this one
                        // in time is the next one in the list.
                        final earlier = k + 1 < entries.length ? entries[k + 1] : null;
                        return _Line(
                          entry: e,
                          showTask: earlier == null || earlier.taskId != e.taskId || earlier.fromPerson,
                          selected: _answering?.id == e.id,
                          onAnswer: e.isOpenQuestion && widget.me.teamSurface
                              ? () => setState(() => _answering = _answering?.id == e.id ? null : e)
                              : null,
                        );
                      },
                    ),
            ),
            _Composer(
              controller: _text,
              answering: _answering,
              sending: _sending,
              // Off, the instance refuses a new message (#328). A reply to a
              // parked question still goes through — it acts on a task that
              // exists either way — but it is chosen at the question, and the
              // question offers it only while the surface is on.
              enabled: widget.me.teamSurface,
              onCancelAnswer: () => setState(() => _answering = null),
              onSend: _send,
            ),
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

class _Line extends StatelessWidget {
  const _Line({required this.entry, required this.selected, this.onAnswer, this.showTask = true});

  final ThreadEntry entry;
  final bool selected;
  final VoidCallback? onAnswer;

  /// Whether this line opens a run of its task's lines. The task is named
  /// once where its run starts; the lines after it follow bare.
  final bool showTask;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final mine = entry.fromPerson;
    final question = entry.kind == 'question';
    final error = entry.kind == 'error';
    // The person's lines are ink on the sheet, the agent's are white paper:
    // two sides told apart by weight, not by a second colour. A question is
    // the one line that asks for something, so it gets the waiting tone and
    // its own answer button.
    final bg = mine ? c.textPrimary : (question ? c.bgWait : c.surface2);
    final fg = mine ? c.surface2 : (error ? c.textDanger : (question ? c.textWait : c.textPrimary));
    const r = Radius.circular(22);
    const tight = Radius.circular(8);
    return LayoutBuilder(
      builder: (context, box) {
        final paneWidth = box.maxWidth;
        return Align(
          alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
          child: ConstrainedBox(
            // Measured against the pane, not the window, and capped: a bubble
            // wider than ~70 characters is a paragraph nobody reads to the end.
            constraints: BoxConstraints(maxWidth: (paneWidth * 0.8).clamp(0, 560)),
            child: Container(
              margin: const EdgeInsets.symmetric(vertical: 3),
              padding: const EdgeInsets.fromLTRB(16, 11, 16, 11),
              decoration: BoxDecoration(
                color: bg,
                // The corner nearest the speaker tightens — the tail, without one.
                borderRadius: BorderRadius.only(
                  topLeft: r,
                  topRight: r,
                  bottomLeft: mine ? r : tight,
                  bottomRight: mine ? tight : r,
                ),
                border: selected ? Border.all(color: c.textWait, width: 1.6) : null,
              ),
              child: Column(
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
                        style: context.type.labelSmall?.copyWith(
                          color: mine ? c.surface2.withValues(alpha: 0.75) : null,
                        ),
                      ),
                    ),
                  SelectableText(entry.text, style: context.type.bodyLarge?.copyWith(color: fg)),
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
              ),
            ),
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
  });

  final TextEditingController controller;
  final ThreadEntry? answering;
  final bool sending;
  final bool enabled;
  final VoidCallback onCancelAnswer;
  final VoidCallback onSend;

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
          Glass(
            radius: 28,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(6, 5, 5, 5),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  Expanded(
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
                        contentPadding: const EdgeInsets.fromLTRB(14, 12, 8, 12),
                        hintText: !enabled
                            ? context.t('mobile.teamAusKurz')
                            : answering != null
                            ? context.t('chat.placeholderAnswer')
                            : context.t('chat.placeholder'),
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
