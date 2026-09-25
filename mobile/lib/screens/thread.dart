import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../models.dart';
import '../theme.dart';

/// One agent, the conversation with it.
///
/// The compose box does what the web's does: a new message hands work over,
/// and an answer goes to the question it answers — chosen at the question,
/// not guessed from whichever task happens to be parked. Two intentions, two
/// places, the same rule as the web shell (#298).
class ThreadScreen extends StatefulWidget {
  const ThreadScreen({super.key, required this.api, required this.agentId, required this.agentName, required this.me});

  final CoveyApi api;
  final String agentId;
  final String agentName;
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
      appBar: AppBar(title: Text(widget.agentName)),
      body: SafeArea(
        child: Column(
          children: [
            if (_error != null)
              Padding(
                padding: const EdgeInsets.all(12),
                child: Text(context.t('mobile.fehler', args: {'error': '$_error'}), style: TextStyle(color: c.textDanger)),
              ),
            Expanded(
              child: th == null
                  ? Center(child: Text(context.t('common.loading')))
                  : entries.isEmpty
                      ? _Empty(name: widget.agentName)
                      : ListView.builder(
                          controller: _scroll,
                          reverse: true,
                          padding: const EdgeInsets.fromLTRB(12, 12, 12, 4),
                          itemCount: entries.length + (th.pending ? 1 : 0),
                          itemBuilder: (context, i) {
                            if (th.pending && i == 0) {
                              return Padding(
                                padding: const EdgeInsets.all(8),
                                child: Text('${widget.agentName} ${context.t('team.arbeitetGerade')}',
                                    style: TextStyle(color: c.textMuted, fontStyle: FontStyle.italic)),
                              );
                            }
                            final e = entries[i - (th.pending ? 1 : 0)];
                            return _Line(
                              entry: e,
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
  const _Empty({required this.name});

  final String name;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(context.t('team.leerTitel', args: {'name': name}),
                textAlign: TextAlign.center, style: const TextStyle(fontWeight: FontWeight.w600)),
            const SizedBox(height: 8),
            Text(context.t('team.leerText'), textAlign: TextAlign.center, style: TextStyle(color: context.colors.textMuted)),
          ],
        ),
      ),
    );
  }
}

class _Line extends StatelessWidget {
  const _Line({required this.entry, required this.selected, this.onAnswer});

  final ThreadEntry entry;
  final bool selected;
  final VoidCallback? onAnswer;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final mine = entry.fromPerson;
    final question = entry.kind == 'question';
    final error = entry.kind == 'error';
    final bg = mine ? c.bgAccent : (question ? c.bgWait : c.surface2);
    final fg = error ? c.textDanger : (question ? c.textWait : c.textPrimary);
    return Align(
      alignment: mine ? Alignment.centerRight : Alignment.centerLeft,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: MediaQuery.sizeOf(context).width * 0.82),
        child: Container(
          margin: const EdgeInsets.symmetric(vertical: 4),
          padding: const EdgeInsets.fromLTRB(12, 9, 12, 9),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: selected ? c.textWait : c.border, width: selected ? 1.5 : 1),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // The task a line belongs to, in words — its state is said, not
              // only coloured.
              if (entry.taskTitle.isNotEmpty && entry.kind != 'message')
                Padding(
                  padding: const EdgeInsets.only(bottom: 4),
                  child: Text(
                    '${entry.taskTitle} · ${context.t('status.${entry.taskState}')}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(fontSize: 11.5, color: c.textMuted),
                  ),
                ),
              SelectableText(entry.text, style: TextStyle(color: fg, height: 1.35)),
              if (onAnswer != null)
                Align(
                  alignment: Alignment.centerRight,
                  child: TextButton(onPressed: onAnswer, child: Text(context.t('chat.answer'))),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

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
    return Container(
      decoration: BoxDecoration(color: c.surface2, border: Border(top: BorderSide(color: c.border))),
      padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (answering != null)
            Row(
              children: [
                Expanded(
                  child: Text(context.t('chat.answering', args: {'title': answering!.taskTitle}),
                      maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(color: c.textWait, fontSize: 12.5)),
                ),
                IconButton(
                  onPressed: onCancelAnswer,
                  icon: const Icon(Icons.close, size: 18),
                  tooltip: context.t('team.abbrechen'),
                ),
              ],
            ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Expanded(
                child: TextField(
                  controller: controller,
                  enabled: enabled,
                  minLines: 1,
                  maxLines: 6,
                  textCapitalization: TextCapitalization.sentences,
                  decoration: InputDecoration(
                    isDense: true,
                    hintText: !enabled
                        ? context.t('mobile.teamAusKurz')
                        : answering != null
                            ? context.t('chat.placeholderAnswer')
                            : context.t('chat.placeholder'),
                  ),
                ),
              ),
              const SizedBox(width: 6),
              IconButton.filled(
                onPressed: enabled && !sending ? onSend : null,
                icon: const Icon(Icons.arrow_upward),
                tooltip: answering != null ? context.t('chat.answer') : context.t('chat.send'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
