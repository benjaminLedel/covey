import 'dart:async';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../chrome.dart';
import '../api.dart';
import '../dictation.dart';
import '../dictation_view.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../rich/bar.dart';
import '../rich/editor.dart';
import '../speech_model.dart';
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

/// What to say when dictation cannot start — with the underlying words
/// where there are any, since "not available" alone cannot be acted on.
String dictationFailure(BuildContext context, DictationFailure? f, [String? detail]) {
  final base = switch (f) {
    DictationFailure.denied => context.t('mobile.mikrofonVerweigert'),
    DictationFailure.modelNotReady => context.t('mobile.keinSprachmodell'),
    DictationFailure.off => context.t('mobile.spracheAus'),
    _ => context.t('mobile.keineSprache'),
  };
  final plain = detail == null || detail.isEmpty || f == DictationFailure.modelNotReady || f == DictationFailure.off;
  return plain ? base : '$base ($detail)';
}

/// A note, new or existing, edited in place the way Apple Notes edits (#343):
/// no edit mode, no save button. What is typed is saved as it is typed — a
/// new note comes into being with its first words, later changes are written
/// a moment after the last key and once more when the note is left. "Fertig"
/// puts the keyboard away. A note left completely empty is removed; it was
/// never a note.
///
/// Dictating into a new note makes it a voice note — the kind says how it
/// was captured. A meeting's summary stands above its transcript as a card;
/// the transcript is editable like any text, so a misheard word can be
/// corrected before summarising again.
class NotePage extends StatefulWidget {
  const NotePage({
    super.key,
    required this.api,
    this.note,
    this.canSummarize = false,
    this.onChanged,
    this.dictation,
    this.saveDelay = const Duration(milliseconds: 700),
    this.pickImage,
  });

  final CoveyApi api;

  /// Null for a new note.
  final Note? note;
  final bool canSummarize;

  /// Called whenever the stored note changed — created, written, deleted —
  /// so the list behind it can follow.
  final VoidCallback? onChanged;
  final Dictation? dictation;
  final Duration saveDelay;

  /// Swapped in tests, so inserting a picture needs no file dialog.
  final Future<Attachment?> Function()? pickImage;

  @override
  State<NotePage> createState() => _NotePageState();
}

class _NotePageState extends State<NotePage> {
  late Note? _note = widget.note;
  late final _title = TextEditingController(text: widget.note?.title ?? '');
  late String _body = widget.note?.body ?? '';
  final _titleFocus = FocusNode();
  final _editor = GlobalKey<BlockEditorState>();
  late final Dictation _dictation = widget.dictation ?? Dictation(api: widget.api);
  ({int index, String base})? _dictatingAt;
  Timer? _debounce;
  bool _dirty = false;
  bool _spoken = false;
  bool _busy = false;
  bool _deleted = false;

  @override
  void initState() {
    super.initState();
    _dictation.addListener(_onDictation);
    _titleFocus.addListener(_redraw);
    WidgetsBinding.instance.addPostFrameCallback((_) => _editor.currentState?.changes.addListener(_redraw));
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _dictation.removeListener(_onDictation);
    if (widget.dictation == null) _dictation.dispose();
    // Leaving: an emptied note goes, anything unsaved is written. Neither
    // is awaited — the page is gone, the requests are not.
    if (!_deleted) {
      if (_title.text.trim().isEmpty && _body.trim().isEmpty) {
        final n = _note;
        if (n != null) widget.api.deleteNote(n.id).then((_) => widget.onChanged?.call(), onError: (_) {});
      } else if (_dirty) {
        _flush();
      }
    }
    _titleFocus.dispose();
    _title.dispose();
    super.dispose();
  }

  void _redraw() {
    if (mounted) setState(() {});
  }

  bool get _editing => _titleFocus.hasFocus || (_editor.currentState?.hasFocus ?? false);

  void _changed() {
    _dirty = true;
    _debounce?.cancel();
    _debounce = Timer(widget.saveDelay, _flush);
  }

  /// Writes what is on the page. A body is what makes a note: until there is
  /// text, a title alone is kept on the page and not sent.
  Future<void> _flush() async {
    if (!_dirty) return;
    final title = _title.text.trim();
    final body = _body.trim();
    if (body.isEmpty) return;
    _dirty = false;
    final messenger = mounted ? ScaffoldMessenger.maybeOf(context) : null;
    try {
      final n = _note;
      if (n == null) {
        _note = await widget.api.createNote(kind: _spoken ? 'voice' : 'text', title: title, body: body);
      } else if (n.title != title || n.body != body) {
        _note = await widget.api.updateNote(n.id, title: title, body: body);
      } else {
        return;
      }
      widget.onChanged?.call();
      if (mounted) setState(() {});
    } on ApiException catch (e) {
      // Not saved: it stays dirty and is tried again with the next change.
      _dirty = true;
      messenger?.showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  void _done() {
    FocusScope.of(context).unfocus();
    _debounce?.cancel();
    _flush();
  }

  // What is dictated goes into the block the caret was in (editor.dart).
  void _onDictation() {
    final at = _dictatingAt;
    if (at != null && _dictation.text.isNotEmpty) _editor.currentState?.dictate(at, _dictation.text);
    if (!_dictation.running) _dictatingAt = null;
    setState(() {});
  }

  Future<void> _toggleDictation() async {
    if (_dictation.preparing) return;
    if (_dictation.running) {
      await _dictation.stop();
      _changed();
      return;
    }
    final messenger = ScaffoldMessenger.of(context);
    _dictatingAt = _editor.currentState?.beginDictation();
    _dictation.language = SpeechModel.instance.language ?? Strings.of(context).language;
    // The preview above the bar says what happens meanwhile — the model's
    // download on the first dictation, then the level and the words.
    final ok = await _dictation.start();
    if (ok) {
      if (_note == null) _spoken = true;
    } else {
      _dictatingAt = null;
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(
          duration: const Duration(seconds: 8),
          content: Text(dictationFailure(context, _dictation.failure, _dictation.detail)),
        ),
      );
    }
  }

  /// A picture into the note (#344): picked, uploaded to the media store,
  /// placed after the caret's block.
  Future<void> _addImage() async {
    final messenger = ScaffoldMessenger.of(context);
    final picked = await (widget.pickImage ?? _pickImage)();
    if (picked == null || !mounted) return;
    try {
      final ref = await widget.api.uploadNoteMedia(picked);
      _editor.currentState?.insertImage(ref);
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  static Future<Attachment?> _pickImage() async {
    final files = await FilePicker.pickFiles(type: FileType.image);
    if (files.isEmpty) return null;
    final f = files.first;
    return Attachment(name: f.name, length: f.lengthSync(), open: () => f.readAsByteStream());
  }

  Future<void> _summarize() async {
    final n = _note;
    if (n == null) return;
    final messenger = ScaffoldMessenger.of(context);
    // What is on the page is what gets summarised.
    await _flush();
    if (!mounted) return;
    setState(() => _busy = true);
    try {
      final s = await widget.api.summarizeNote(n.id);
      setState(() => _note = s);
      widget.onChanged?.call();
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _delete() async {
    final n = _note;
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
    _deleted = true;
    _debounce?.cancel();
    if (n != null) {
      await widget.api.deleteNote(n.id);
      widget.onChanged?.call();
    }
    if (nav.canPop()) nav.pop();
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final n = _note;
    final kind = n?.kind ?? (_spoken ? 'voice' : 'text');
    final listening = _dictation.running;
    return Scaffold(
      appBar: ChromeAppBar(
        actions: [
          if (_editing)
            // Apple Notes' "Fertig": the keyboard goes, the note is written.
            TextButton(onPressed: _done, child: Text(context.t('mobile.fertig')))
          else if (n != null)
            IconButton(
              onPressed: _delete,
              icon: Icon(AppIcons.delete.of(context)),
              tooltip: context.t('mobile.loeschen'),
            ),
          const SizedBox(width: 8),
        ],
      ),
      body: SafeArea(
        bottom: false,
        child: Column(
          children: [
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(20, 0, 20, 24),
                children: [
                  if (n != null)
                    Row(
                      children: [
                        KindMark(kind: kind),
                        const SizedBox(width: 12),
                        Expanded(child: Text(noteMeta(context, n), style: context.type.bodySmall)),
                      ],
                    ),
                  TextField(
                    controller: _title,
                    focusNode: _titleFocus,
                    style: context.type.headlineSmall,
                    maxLines: null,
                    textCapitalization: TextCapitalization.sentences,
                    textInputAction: TextInputAction.next,
                    onChanged: (_) => _changed(),
                    decoration: _bare(context, context.t('mobile.titelOptional'), context.type.headlineSmall),
                  ),
                  if (n != null && n.summary.isNotEmpty) ...[
                    const SizedBox(height: 6),
                    Container(
                      padding: const EdgeInsets.all(18),
                      decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(20)),
                      child: SummaryText(n.summary),
                    ),
                    const SizedBox(height: 12),
                  ],
                  // Summaries are for what was spoken; a typed note is its own summary.
                  if (widget.canSummarize && n != null && kind != 'text')
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
                  if (n != null && kind != 'text') ...[
                    const SizedBox(height: 22),
                    Text(context.t('mobile.transkript'), style: context.type.titleLarge),
                    const SizedBox(height: 4),
                  ],
                  BlockEditor(
                    key: _editor,
                    api: widget.api,
                    initial: _body,
                    hint: context.t('mobile.notizHinweis'),
                    autofocus: widget.note == null,
                    onChanged: (md) {
                      _body = md;
                      _changed();
                    },
                  ),
                ],
              ),
            ),
            if (listening || _dictation.preparing)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
                child: DictationPreview(dictation: _dictation, onStop: listening ? _toggleDictation : null),
              ),
            // With the keyboard up the formatting bar, otherwise the one
            // thing the page offers without typing: dictation.
            if (_editing && !_titleFocus.hasFocus)
              EditorBar(editor: _editor, onImage: _addImage, onDictate: _toggleDictation, dictating: listening)
            else
              SafeArea(
                top: false,
                child: Padding(
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
  late final Dictation _dictation = widget.dictation ?? Dictation(api: widget.api);
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
    _dictation.language = SpeechModel.instance.language ?? Strings.of(context).language;
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
      appBar: ChromeAppBar(title: Text(context.t('mobile.meetingNeu'))),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (failed)
                Text(
                  dictationFailure(context, _dictation.failure, _dictation.detail),
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
                      _dictation.running
                          ? context.t('mobile.meetingLaeuft')
                          : _dictation.preparing
                          ? modelLoadingText(context, _dictation)
                          : context.t('common.loading'),
                      style: context.type.labelLarge?.copyWith(color: c.textSecondary),
                    ),
                  ],
                ),
                const SizedBox(height: 14),
                Waveform(levels: _dictation.levels, height: 44, active: _dictation.running),
                const SizedBox(height: 14),
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
