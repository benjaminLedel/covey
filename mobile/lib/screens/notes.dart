import 'dart:async';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../api.dart';
import '../dictation.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../summary_text.dart';
import '../theme.dart';
import '../ui.dart';

/// The Notes space (#336, spec/14): what the person captures for themselves —
/// typed, spoken, or a whole meeting. Always there, whatever the
/// organisation has switched on, and private: nobody else reads these notes.
/// Capturing starts at the + beside the capsule, not here.
class NotesScreen extends StatefulWidget {
  const NotesScreen({
    super.key,
    required this.api,
    required this.onOpen,
    this.actions = const [],
    this.bottomClearance = capsuleClearance,
    this.compact = false,
  });

  final CoveyApi api;

  /// Opens a note — pushed on a phone, beside the list on a wide window.
  final void Function(Note note, bool canSummarize, VoidCallback changed) onOpen;
  final List<Widget> actions;
  final double bottomClearance;

  /// The list pane of a wide window: no bar row above the title.
  final bool compact;

  @override
  State<NotesScreen> createState() => NotesScreenState();
}

class NotesScreenState extends State<NotesScreen> {
  NotesPage? _page;
  Object? _error;
  String _query = '';
  Timer? _debounce;

  @override
  void dispose() {
    _debounce?.cancel();
    super.dispose();
  }

  /// The instance searches title, text and summary; a quarter second after
  /// the last key, so a word is one request, not five.
  void _search(String q) {
    _query = q;
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 250), reload);
  }

  bool get canSummarize => _page?.summarize ?? false;

  @override
  void initState() {
    super.initState();
    reload();
  }

  Future<void> reload() async {
    try {
      final p = await widget.api.notes(q: _query);
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

  /// The day a note belongs to, as a heading: today, yesterday, or the date.
  String _day(BuildContext context, DateTime? at) {
    if (at == null) return '';
    final now = DateTime.now();
    final d = DateTime(at.year, at.month, at.day);
    final today = DateTime(now.year, now.month, now.day);
    if (d == today) return context.t('mobile.heute');
    if (d == today.subtract(const Duration(days: 1))) return context.t('team.gestern');
    return DateFormat.yMMMMd(Strings.of(context).language).format(at);
  }

  @override
  Widget build(BuildContext context) {
    final page = _page;
    final days = <String, List<Note>>{};
    for (final n in page?.notes ?? const <Note>[]) {
      days.putIfAbsent(_day(context, n.createdAt), () => []).add(n);
    }
    return SpaceScroll(
      title: context.t('mobile.notizen'),
      actions: widget.actions,
      bottomClearance: widget.bottomClearance,
      compact: widget.compact,
      onRefresh: reload,
      search: SearchField(hint: context.t('mobile.notizenSuchen'), onChanged: _search),
      slivers: [
        if (_error != null)
          SliverToBoxAdapter(child: EmptyNote(context.t('mobile.fehler', args: {'error': '$_error'}))),
        if (page == null && _error == null) SliverToBoxAdapter(child: EmptyNote(context.t('common.loading'))),
        if (page != null && page.notes.isEmpty)
          SliverToBoxAdapter(
            child: EmptyNote(
              _query.trim().isEmpty ? context.t('mobile.notizenLeer') : context.t('team.nichtsGefunden'),
            ),
          ),
        for (final day in days.entries) ...[
          SliverToBoxAdapter(child: SectionTitle(day.key)),
          SliverToBoxAdapter(
            child: InsetGroup(
              children: [
                for (final n in day.value)
                  GroupRow(
                    leading: KindMark(kind: n.kind),
                    title: n.heading,
                    subtitle: noteMeta(context, n, withDate: false),
                    // State, not action: muted, and said for a screen reader.
                    trailing: n.summary.isEmpty
                        ? null
                        : Icon(
                            AppIcons.summary.of(context),
                            size: 18,
                            color: context.colors.textMuted,
                            semanticLabel: context.t('mobile.hatZusammenfassung'),
                          ),
                    tabularSubtitle: true,
                    onTap: () => widget.onOpen(n, page!.summarize, reload),
                  ),
              ],
            ),
          ),
        ],
      ],
    );
  }
}

/// A note's kind as a drawn mark on a small tile — the shape says what it
/// is, the word beside it says it again.
class KindMark extends StatelessWidget {
  const KindMark({super.key, required this.kind});

  final String kind;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final meeting = kind == 'meeting';
    return Container(
      width: 36,
      height: 36,
      decoration: BoxDecoration(color: meeting ? c.bgAccent : c.surface0, borderRadius: BorderRadius.circular(11)),
      child: Icon(kindIcon(context, kind), size: 19, color: meeting ? c.textAccent : c.textSecondary),
    );
  }
}

IconData kindIcon(BuildContext context, String kind) => switch (kind) {
  'voice' => AppIcons.kindVoice,
  'meeting' => AppIcons.kindMeeting,
  _ => AppIcons.kindText,
}.of(context);

String _duration(int seconds) {
  final h = seconds ~/ 3600, m = (seconds % 3600) ~/ 60, s = seconds % 60;
  String two(int v) => v.toString().padLeft(2, '0');
  return h > 0 ? '$h:${two(m)}:${two(s)}' : '$m:${two(s)}';
}

/// The line under a note: its kind, when, and for a meeting how long.
String noteMeta(BuildContext context, Note n, {bool withDate = true}) {
  final lang = Strings.of(context).language;
  final when = n.createdAt == null
      ? ''
      : withDate
      ? DateFormat.yMMMd(lang).add_Hm().format(n.createdAt!)
      : DateFormat.Hm(lang).format(n.createdAt!);
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
        actions: [
          Padding(
            padding: const EdgeInsets.only(right: 12),
            child: FilledButton(
              onPressed: _saving || _body.text.trim().isEmpty ? null : _save,
              style: FilledButton.styleFrom(minimumSize: const Size(44, 38), shape: const StadiumBorder()),
              child: Text(context.t('mobile.speichern')),
            ),
          ),
        ],
      ),
      // Title and text sit on the sheet itself, as on a page — not in two
      // form fields.
      body: SafeArea(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 4, 20, 0),
              child: TextField(
                controller: _title,
                style: context.type.headlineSmall,
                textCapitalization: TextCapitalization.sentences,
                decoration: _bare(context, context.t('mobile.titelOptional'), context.type.headlineSmall),
              ),
            ),
            Expanded(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(20, 4, 20, 8),
                child: TextField(
                  controller: _body,
                  autofocus: true,
                  maxLines: null,
                  expands: true,
                  style: context.type.bodyLarge,
                  textAlignVertical: TextAlignVertical.top,
                  textCapitalization: TextCapitalization.sentences,
                  onChanged: (_) => setState(() {}),
                  decoration: _bare(context, context.t('mobile.notizHinweis'), context.type.bodyLarge),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
              child: Glass(
                child: SizedBox(
                  height: 56,
                  child: TextButton.icon(
                    onPressed: _toggleDictation,
                    style: TextButton.styleFrom(
                      foregroundColor: listening ? c.textAccent : c.textPrimary,
                      shape: const StadiumBorder(),
                    ),
                    icon: Icon(listening ? AppIcons.stop.of(context) : AppIcons.mic.of(context)),
                    label: Text(listening ? context.t('mobile.diktatStop') : context.t('mobile.diktieren')),
                  ),
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
          padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (failed)
                Text(
                  dictationFailure(context, _dictation.failure),
                  style: context.type.bodyLarge?.copyWith(color: c.textDanger),
                )
              else ...[
                // The clock is the screen's one large thing: it says the
                // recording runs, and for how long.
                Text(
                  _duration(_watch.elapsed.inSeconds),
                  style: context.type.headlineMedium?.copyWith(
                    fontSize: 56,
                    letterSpacing: -2,
                    fontFeatures: const [FontFeature.tabularFigures()],
                  ),
                ),
                Row(
                  children: [
                    Icon(
                      AppIcons.record.of(context),
                      size: 10, // Activity, not failure: red means "ended against its purpose" (PRODUCT.md).
                      color: _dictation.running ? c.textAccent : c.textMuted,
                    ),
                    const SizedBox(width: 8),
                    Text(
                      _dictation.running ? context.t('mobile.meetingLaeuft') : context.t('common.loading'),
                      style: context.type.labelLarge?.copyWith(color: c.textSecondary),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(context.t('mobile.meetingHinweis'), style: context.type.bodySmall),
              ],
              const SizedBox(height: 20),
              Expanded(
                child: Container(
                  padding: const EdgeInsets.all(18),
                  decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(20)),
                  child: SingleChildScrollView(
                    reverse: true,
                    child: Text(
                      text.isEmpty ? context.t('mobile.nochNichtsGehoert') : text,
                      style: context.type.bodyLarge?.copyWith(color: text.isEmpty ? c.textMuted : c.textPrimary),
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              FilledButton.icon(
                onPressed: _saving || failed ? null : _stop,
                icon: Icon(AppIcons.stop.of(context)),
                label: Text(context.t('mobile.meetingStop')),
                style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(56), shape: const StadiumBorder()),
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
        actions: [
          IconButton(
            onPressed: _delete,
            icon: Icon(AppIcons.delete.of(context)),
            tooltip: context.t('mobile.loeschen'),
          ),
          const SizedBox(width: 4),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(20, 0, 20, 40),
        children: [
          Row(
            children: [
              KindMark(kind: n.kind),
              const SizedBox(width: 12),
              Expanded(child: Text(noteMeta(context, n), style: context.type.bodySmall)),
            ],
          ),
          const SizedBox(height: 14),
          SelectableText(n.heading, style: context.type.headlineSmall),
          const SizedBox(height: 20),
          if (n.summary.isNotEmpty) ...[
            Container(
              padding: const EdgeInsets.all(18),
              decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(20)),
              child: SummaryText(n.summary),
            ),
            const SizedBox(height: 16),
          ],
          // Summaries are for what was spoken; a typed note is its own summary.
          if (widget.canSummarize && n.kind != 'text')
            Align(
              alignment: Alignment.centerLeft,
              child: OutlinedButton.icon(
                onPressed: _busy ? null : _summarize,
                style: OutlinedButton.styleFrom(shape: const StadiumBorder()),
                icon: Icon(AppIcons.summary.of(context), size: 18, color: c.textAccent),
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
            const SizedBox(height: 28),
            Text(context.t('mobile.transkript'), style: context.type.titleLarge),
            const SizedBox(height: 10),
          ],
          SelectableText(n.body, style: context.type.bodyLarge),
        ],
      ),
    );
  }
}

/// A text field that is only text: no box, no border — the page is the field.
InputDecoration _bare(BuildContext context, String hint, TextStyle? style) => InputDecoration(
  hintText: hint,
  hintStyle: style?.copyWith(color: context.colors.textMuted),
  filled: false,
  border: InputBorder.none,
  enabledBorder: InputBorder.none,
  focusedBorder: InputBorder.none,
  contentPadding: const EdgeInsets.symmetric(vertical: 10),
);
