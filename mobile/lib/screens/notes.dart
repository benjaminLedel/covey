import 'dart:async';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../api.dart';
import '../dictation.dart';
import '../i18n.dart';
import '../models.dart';
import '../theme.dart';

/// The notetaker (#336, spec/14): what the person captures for themselves —
/// typed, spoken, or a whole meeting. Always there, whatever the
/// organisation has switched on, and private: nobody else reads these notes.
class NotesScreen extends StatefulWidget {
  const NotesScreen({super.key, required this.api, required this.onOpen});

  final CoveyApi api;

  /// Opens a note — pushed on a phone, beside the list on a wide window.
  final void Function(Note note, bool canSummarize, VoidCallback changed) onOpen;

  @override
  State<NotesScreen> createState() => NotesScreenState();
}

class NotesScreenState extends State<NotesScreen> {
  NotesPage? _page;
  Object? _error;

  @override
  void initState() {
    super.initState();
    reload();
  }

  Future<void> reload() async {
    try {
      final p = await widget.api.notes();
      if (mounted) {
        setState(() {
          _page = p;
          _error = null;
        });
      }
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _new(Widget screen) async {
    final saved = await Navigator.of(context).push<Note>(MaterialPageRoute(builder: (_) => screen));
    if (saved == null || !mounted) return;
    await reload();
    widget.onOpen(saved, _page?.summarize ?? false, reload);
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final page = _page;
    return Stack(
      children: [
        RefreshIndicator(
          onRefresh: reload,
          child: ListView(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.only(bottom: 120),
            children: [
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: Text(
                    context.t('mobile.fehler', args: {'error': '$_error'}),
                    style: TextStyle(color: c.textDanger),
                  ),
                ),
              if (page == null && _error == null)
                Padding(padding: const EdgeInsets.all(16), child: Text(context.t('common.loading'))),
              if (page != null && page.notes.isEmpty)
                Padding(
                  padding: const EdgeInsets.fromLTRB(24, 32, 24, 16),
                  child: Text(context.t('mobile.notizenLeer'), style: TextStyle(color: c.textMuted, height: 1.4)),
                ),
              if (page != null)
                for (final n in page.notes)
                  ListTile(
                    leading: Icon(kindIcon(n.kind), color: c.textMuted),
                    title: Text(n.heading, maxLines: 1, overflow: TextOverflow.ellipsis),
                    subtitle: Text(noteMeta(context, n), style: TextStyle(color: c.textMuted, fontSize: 12.5)),
                    onTap: () => widget.onOpen(n, page.summarize, reload),
                  ),
            ],
          ),
        ),
        // Both ways in sit where the thumb is (spec/27).
        Positioned(
          left: 16,
          right: 16,
          bottom: 16,
          child: SafeArea(
            child: Row(
              children: [
                Expanded(
                  child: FilledButton.icon(
                    onPressed: () => _new(NoteEditor(api: widget.api)),
                    icon: const Icon(Icons.edit_note),
                    label: Text(context.t('mobile.notizNeu')),
                    style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(48)),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: OutlinedButton.icon(
                    onPressed: () => _new(MeetingScreen(api: widget.api)),
                    icon: const Icon(Icons.mic_none),
                    // Short: "Meeting aufnehmen" wraps on a phone in German.
                    label: Text(context.t('mobile.meetingKnopf')),
                    style: OutlinedButton.styleFrom(
                      minimumSize: const Size.fromHeight(48),
                      backgroundColor: c.surface2,
                    ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

IconData kindIcon(String kind) => switch (kind) {
  'voice' => Icons.mic_none,
  'meeting' => Icons.groups_outlined,
  _ => Icons.notes,
};

String _duration(int seconds) {
  final h = seconds ~/ 3600, m = (seconds % 3600) ~/ 60, s = seconds % 60;
  String two(int v) => v.toString().padLeft(2, '0');
  return h > 0 ? '$h:${two(m)}:${two(s)}' : '$m:${two(s)}';
}

/// The line under a note: its kind, when, and for a meeting how long.
String noteMeta(BuildContext context, Note n) {
  final lang = Strings.of(context).language;
  final when = n.createdAt == null ? '' : DateFormat.yMMMd(lang).add_Hm().format(n.createdAt!);
  return [
    context.t('mobile.art_${n.kind}'),
    when,
    if (n.kind == 'meeting' && n.durationSeconds > 0) _duration(n.durationSeconds),
  ].where((s) => s.isNotEmpty).join(' · ');
}

/// What to say when dictation cannot start.
String dictationFailure(BuildContext context, DictationFailure? f) =>
    f == DictationFailure.denied ? context.t('mobile.mikrofonVerweigert') : context.t('mobile.keineSprache');

/// A new note, typed or dictated. Dictating makes it a voice note — the kind
/// says how it was captured, not what it is about.
class NoteEditor extends StatefulWidget {
  const NoteEditor({super.key, required this.api, this.dictation});

  final CoveyApi api;
  final Dictation? dictation;

  @override
  State<NoteEditor> createState() => _NoteEditorState();
}

class _NoteEditorState extends State<NoteEditor> {
  final _title = TextEditingController();
  final _body = TextEditingController();
  late final Dictation _dictation = widget.dictation ?? Dictation();
  bool _spoken = false;
  bool _saving = false;
  String _before = '';

  @override
  void initState() {
    super.initState();
    _dictation.addListener(_onDictation);
  }

  @override
  void dispose() {
    _dictation.removeListener(_onDictation);
    if (widget.dictation == null) _dictation.dispose();
    _title.dispose();
    _body.dispose();
    super.dispose();
  }

  // What is dictated is appended to what was typed before.
  void _onDictation() {
    if (!_dictation.running && _dictation.text.isEmpty) {
      setState(() {});
      return;
    }
    final joined = [_before, _dictation.text].where((s) => s.trim().isNotEmpty).join(_before.isEmpty ? '' : ' ');
    _body.value = TextEditingValue(
      text: joined,
      selection: TextSelection.collapsed(offset: joined.length),
    );
    setState(() {});
  }

  Future<void> _toggleDictation() async {
    if (_dictation.running) {
      await _dictation.stop();
      return;
    }
    _before = _body.text.trim();
    final messenger = ScaffoldMessenger.of(context);
    final failed = dictationFailure(context, DictationFailure.unavailable);
    final denied = dictationFailure(context, DictationFailure.denied);
    if (await _dictation.start()) {
      _spoken = true;
    } else {
      messenger.showSnackBar(SnackBar(content: Text(_dictation.failure == DictationFailure.denied ? denied : failed)));
    }
  }

  Future<void> _save() async {
    final messenger = ScaffoldMessenger.of(context);
    final nav = Navigator.of(context);
    if (_dictation.running) await _dictation.stop();
    final body = _body.text.trim();
    if (body.isEmpty || !mounted) return;
    setState(() => _saving = true);
    try {
      final n = await widget.api.createNote(kind: _spoken ? 'voice' : 'text', title: _title.text.trim(), body: body);
      nav.pop(n);
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
      setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final listening = _dictation.running;
    return Scaffold(
      appBar: AppBar(
        title: Text(context.t('mobile.notizNeu')),
        actions: [
          TextButton(
            onPressed: _saving || _body.text.trim().isEmpty ? null : _save,
            child: Text(context.t('mobile.speichern')),
          ),
        ],
      ),
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
              child: TextField(
                controller: _title,
                decoration: InputDecoration(labelText: context.t('mobile.titelOptional')),
              ),
            ),
            Expanded(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: TextField(
                  controller: _body,
                  autofocus: true,
                  maxLines: null,
                  expands: true,
                  textAlignVertical: TextAlignVertical.top,
                  textCapitalization: TextCapitalization.sentences,
                  onChanged: (_) => setState(() {}),
                  decoration: InputDecoration(hintText: context.t('mobile.notizHinweis')),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
              child: FilledButton.tonalIcon(
                onPressed: _toggleDictation,
                icon: Icon(listening ? Icons.stop : Icons.mic_none),
                label: Text(listening ? context.t('mobile.diktatStop') : context.t('mobile.diktieren')),
                style: FilledButton.styleFrom(
                  minimumSize: const Size.fromHeight(48),
                  backgroundColor: listening ? c.bgAccent : null,
                  foregroundColor: listening ? c.textAccent : null,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A meeting: recognition runs until it is stopped, the transcript grows on
/// screen, and stopping saves it as one note with its length.
class MeetingScreen extends StatefulWidget {
  const MeetingScreen({super.key, required this.api, this.dictation});

  final CoveyApi api;
  final Dictation? dictation;

  @override
  State<MeetingScreen> createState() => _MeetingScreenState();
}

class _MeetingScreenState extends State<MeetingScreen> {
  late final Dictation _dictation = widget.dictation ?? Dictation();
  final _watch = Stopwatch();
  Timer? _tick;
  bool _saving = false;
  bool _started = false;

  @override
  void initState() {
    super.initState();
    _dictation.addListener(_changed);
    WidgetsBinding.instance.addPostFrameCallback((_) => _start());
  }

  void _changed() => setState(() {});

  Future<void> _start() async {
    if (await _dictation.start(continuous: true)) {
      _watch.start();
      _tick = Timer.periodic(const Duration(seconds: 1), (_) => setState(() {}));
    }
    if (mounted) setState(() => _started = true);
  }

  @override
  void dispose() {
    _tick?.cancel();
    _dictation.removeListener(_changed);
    if (widget.dictation == null) _dictation.dispose();
    super.dispose();
  }

  Future<void> _stop() async {
    _watch.stop();
    _tick?.cancel();
    final text = await _dictation.stop();
    if (!mounted) return;
    final nav = Navigator.of(context);
    final messenger = ScaffoldMessenger.of(context);
    if (text.trim().isEmpty) {
      messenger.showSnackBar(SnackBar(content: Text(context.t('mobile.nichtsErkannt'))));
      nav.pop();
      return;
    }
    setState(() => _saving = true);
    try {
      final n = await widget.api.createNote(kind: 'meeting', body: text, durationSeconds: _watch.elapsed.inSeconds);
      nav.pop(n);
    } on ApiException catch (e) {
      // The transcript is not lost: it stays on screen and can be tried again.
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
      setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final failed = _started && !_dictation.running && _dictation.failure != null;
    final text = _dictation.text;
    return Scaffold(
      appBar: AppBar(title: Text(context.t('mobile.meetingNeu'))),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (failed)
                Text(dictationFailure(context, _dictation.failure), style: TextStyle(color: c.textDanger))
              else ...[
                Row(
                  children: [
                    Icon(Icons.fiber_manual_record, size: 14, color: _dictation.running ? c.textDanger : c.textMuted),
                    const SizedBox(width: 8),
                    Text(
                      _dictation.running ? context.t('mobile.meetingLaeuft') : context.t('common.loading'),
                      style: TextStyle(color: c.textMuted),
                    ),
                    const Spacer(),
                    Text(
                      _duration(_watch.elapsed.inSeconds),
                      style: const TextStyle(
                        fontSize: 22,
                        fontWeight: FontWeight.w600,
                        fontFeatures: [FontFeature.tabularFigures()],
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(context.t('mobile.meetingHinweis'), style: TextStyle(color: c.textMuted, fontSize: 12.5)),
              ],
              const SizedBox(height: 16),
              Expanded(
                child: Container(
                  padding: const EdgeInsets.all(14),
                  decoration: BoxDecoration(
                    color: c.surface2,
                    borderRadius: BorderRadius.circular(12),
                    border: Border.all(color: c.border),
                  ),
                  child: SingleChildScrollView(
                    reverse: true,
                    child: Text(
                      text.isEmpty ? context.t('mobile.nochNichtsGehoert') : text,
                      style: TextStyle(height: 1.45, color: text.isEmpty ? c.textMuted : c.textPrimary),
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              FilledButton.icon(
                onPressed: _saving || failed ? null : _stop,
                icon: const Icon(Icons.stop),
                label: Text(context.t('mobile.meetingStop')),
                style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(52)),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// One note: its text, and for a meeting the summary with the action items.
class NoteScreen extends StatefulWidget {
  const NoteScreen({super.key, required this.api, required this.note, required this.canSummarize, this.onChanged});

  final CoveyApi api;
  final Note note;
  final bool canSummarize;
  final VoidCallback? onChanged;

  @override
  State<NoteScreen> createState() => _NoteScreenState();
}

class _NoteScreenState extends State<NoteScreen> {
  late Note _note = widget.note;
  bool _busy = false;

  Future<void> _summarize() async {
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      final n = await widget.api.summarizeNote(_note.id);
      setState(() => _note = n);
      widget.onChanged?.call();
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _delete() async {
    final t = Strings.of(context).t;
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(t('mobile.loeschenFrage')),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(t('team.abbrechen'))),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(t('mobile.loeschen'))),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    final nav = Navigator.of(context);
    await widget.api.deleteNote(_note.id);
    widget.onChanged?.call();
    if (nav.canPop()) nav.pop();
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final n = _note;
    return Scaffold(
      appBar: AppBar(
        title: Text(n.heading, maxLines: 1, overflow: TextOverflow.ellipsis),
        actions: [
          IconButton(onPressed: _delete, icon: const Icon(Icons.delete_outline), tooltip: context.t('mobile.loeschen')),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
        children: [
          Text(noteMeta(context, n), style: TextStyle(color: c.textMuted, fontSize: 12.5)),
          const SizedBox(height: 16),
          if (n.summary.isNotEmpty) ...[
            Container(
              padding: const EdgeInsets.all(14),
              decoration: BoxDecoration(color: c.bgAccent, borderRadius: BorderRadius.circular(12)),
              child: SelectableText(n.summary, style: TextStyle(height: 1.45, color: c.textPrimary)),
            ),
            const SizedBox(height: 16),
          ],
          // Summaries are for what was spoken; a typed note is its own summary.
          if (widget.canSummarize && n.kind != 'text')
            Align(
              alignment: Alignment.centerLeft,
              child: OutlinedButton.icon(
                onPressed: _busy ? null : _summarize,
                icon: const Icon(Icons.auto_awesome_outlined, size: 18),
                label: Text(
                  _busy
                      ? context.t('common.loading')
                      : n.summary.isEmpty
                      ? context.t('mobile.zusammenfassen')
                      : context.t('mobile.zusammenfassenNeu'),
                ),
              ),
            ),
          if (n.kind != 'text') ...[
            const SizedBox(height: 20),
            Text(
              context.t('mobile.transkript').toUpperCase(),
              style: TextStyle(fontSize: 11.5, letterSpacing: 0.6, fontWeight: FontWeight.w600, color: c.textMuted),
            ),
            const SizedBox(height: 6),
          ],
          SelectableText(n.body, style: const TextStyle(height: 1.45)),
        ],
      ),
    );
  }
}
