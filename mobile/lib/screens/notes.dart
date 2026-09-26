import 'dart:async';
import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../chrome.dart';
import '../api.dart';
import '../prefs.dart';
import '../diarize.dart';
import '../dictation.dart';
import '../dictation_view.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../rich/bar.dart';
import '../rich/editor.dart';
import '../rich/media_image.dart';
import '../summary_text.dart';
import '../system_audio.dart';
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
    this.onViewChanged,
  });

  final CoveyApi api;

  /// Tells the page which view is shown (#373): a wide window gives the
  /// table and the board its whole width.
  final ValueChanged<NotesView>? onViewChanged;

  /// Opens a note — pushed on a phone, beside the list on a wide window.
  final void Function(Note note, bool canSummarize, VoidCallback changed) onOpen;
  final List<Widget> actions;
  final double bottomClearance;

  /// The list pane of a wide window: no bar row above the title.
  final bool compact;

  @override
  State<NotesScreen> createState() => NotesScreenState();
}

/// How the notes are shown (#373).
enum NotesView { list, board }

class NotesScreenState extends State<NotesScreen> {
  NotesView _view = NotesView.list;
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
    Prefs.instance.read('notes.view').then((v) {
      final view = NotesView.values.where((x) => x.name == v).firstOrNull;
      if (view != null && mounted) {
        setState(() => _view = view);
        widget.onViewChanged?.call(view);
      }
    });
  }

  void _setView(NotesView v) {
    setState(() => _view = v);
    widget.onViewChanged?.call(v);
    Prefs.instance.write('notes.view', v.name);
  }

  /// A card dragged to another column of the board.
  Future<void> _move(Note n, String status) async {
    if (n.status == status) return;
    try {
      await widget.api.updateNote(n.id, status: status);
      await reload();
    } on ApiException catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    }
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
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 16, 0),
            child: Row(
              children: [
                _ViewTab(
                  icon: Icons.view_agenda_outlined,
                  label: context.t('mobile.ansichtListe'),
                  selected: _view == NotesView.list,
                  onTap: () => _setView(NotesView.list),
                ),
                _ViewTab(
                  icon: Icons.view_kanban_outlined,
                  label: context.t('mobile.ansichtBoard'),
                  selected: _view == NotesView.board,
                  onTap: () => _setView(NotesView.board),
                ),
              ],
            ),
          ),
        ),
        if (page != null && page.notes.isNotEmpty && _view == NotesView.board)
          SliverToBoxAdapter(
            child: NotesBoard(
              notes: page.notes,
              onOpen: (n) => widget.onOpen(n, page.summarize, reload),
              onMove: _move,
            ),
          ),
        if (_error != null)
          SliverToBoxAdapter(child: EmptyNote(context.t('mobile.fehler', args: {'error': '$_error'}))),
        if (page == null && _error == null) SliverToBoxAdapter(child: EmptyNote(context.t('common.loading'))),
        if (page != null && page.notes.isEmpty)
          SliverToBoxAdapter(
            child: EmptyNote(
              _query.trim().isEmpty ? context.t('mobile.notizenLeer') : context.t('team.nichtsGefunden'),
            ),
          ),
        if (_view == NotesView.list)
          for (final day in days.entries) ...[
            SliverToBoxAdapter(child: SectionTitle(day.key)),
            SliverToBoxAdapter(
              child: InsetGroup(
                children: [
                  for (final n in day.value)
                    GroupRow(
                      leading: KindMark(kind: n.kind, icon: n.icon),
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
  const KindMark({super.key, required this.kind, this.icon = ''});

  final String kind;

  /// The note's own icon, which stands in for the kind (#372).
  final String icon;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final meeting = kind == 'meeting';
    return Container(
      width: 36,
      height: 36,
      alignment: Alignment.center,
      decoration: BoxDecoration(color: meeting ? c.bgAccent : c.surface0, borderRadius: BorderRadius.circular(11)),
      child: icon.isNotEmpty
          ? Text(icon, style: const TextStyle(fontSize: 20))
          : Icon(kindIcon(context, kind), size: 19, color: meeting ? c.textAccent : c.textSecondary),
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
  late String _icon = widget.note?.icon ?? '';
  late String _cover = widget.note?.cover ?? '';
  late String _status = widget.note?.status ?? '';
  late DateTime? _due = widget.note?.due;
  late List<String> _tags = [...?widget.note?.tags];
  bool _headerHover = false;
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
        var created = await widget.api.createNote(kind: _spoken ? 'voice' : 'text', title: title, body: body);
        // An icon or a cover chosen before the first words go on now.
        if (_icon.isNotEmpty || _cover.isNotEmpty || _hasProperties) {
          created = await widget.api.updateNote(
            created.id,
            icon: _icon,
            cover: _cover,
            status: _status,
            due: _dueText,
            tags: _tags,
          );
        }
        _note = created;
      } else if (n.title != title ||
          n.body != body ||
          n.icon != _icon ||
          n.cover != _cover ||
          n.status != _status ||
          _dayText(n.due) != _dueText ||
          n.tags.join('\u0000') != _tags.join('\u0000')) {
        _note = await widget.api.updateNote(
          n.id,
          title: title,
          body: body,
          icon: _icon,
          cover: _cover,
          status: _status,
          due: _dueText,
          tags: _tags,
        );
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

  // --- Properties (#373). ---

  bool get _hasProperties => _status.isNotEmpty || _due != null || _tags.isNotEmpty;
  String get _dueText => _dayText(_due);

  void _setProperties(void Function() change) {
    setState(change);
    _changed();
  }

  Future<void> _pickStatus() async {
    final picked = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final st in noteStatuses)
              ListTile(
                leading: StatusDot(status: st),
                title: Text(context.t('mobile.nstatus_${st.isEmpty ? 'none' : st}')),
                trailing: st == _status ? const Icon(Icons.check_rounded) : null,
                onTap: () => Navigator.pop(context, st),
              ),
          ],
        ),
      ),
    );
    if (picked != null) _setProperties(() => _status = picked);
  }

  Future<void> _pickDue() async {
    final now = DateTime.now();
    final picked = await showDatePicker(
      context: context,
      initialDate: _due ?? now,
      firstDate: DateTime(now.year - 5),
      lastDate: DateTime(now.year + 10),
    );
    if (picked != null) _setProperties(() => _due = picked);
  }

  Future<void> _addTag() async {
    final field = TextEditingController();
    final tag = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.t('mobile.propTags')),
        content: TextField(
          controller: field,
          autofocus: true,
          maxLength: 30,
          decoration: InputDecoration(hintText: context.t('mobile.tagHinweis')),
          onSubmitted: (v) => Navigator.pop(context, v),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: Text(context.t('team.abbrechen'))),
          FilledButton(
            onPressed: () => Navigator.pop(context, field.text),
            child: Text(context.t('mobile.uebernehmen')),
          ),
        ],
      ),
    );
    field.dispose();
    final t = tag?.trim().replaceFirst(RegExp(r'^#'), '') ?? '';
    if (t.isEmpty || _tags.any((x) => x.toLowerCase() == t.toLowerCase()) || _tags.length >= 10) return;
    _setProperties(() => _tags = [..._tags, t]);
  }

  /// The set properties as chips under the title; tapping one changes it.
  Widget _properties(BuildContext context) {
    if (!_hasProperties) return const SizedBox.shrink();
    final c = context.colors;
    final lang = Strings.of(context).language;
    return Padding(
      padding: const EdgeInsets.only(top: 2, bottom: 8),
      child: Wrap(
        spacing: 6,
        runSpacing: 6,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          if (_status.isNotEmpty)
            PropertyChip(
              leading: StatusDot(status: _status),
              label: context.t('mobile.nstatus_$_status'),
              onTap: _pickStatus,
            ),
          if (_due != null)
            PropertyChip(
              leading: Icon(Icons.event_outlined, size: 15, color: c.textSecondary),
              label: DateFormat.yMMMd(lang).format(_due!),
              onTap: _pickDue,
              onRemove: () => _setProperties(() => _due = null),
            ),
          for (final t in _tags)
            PropertyChip(label: '#$t', onRemove: () => _setProperties(() => _tags = [..._tags]..remove(t))),
          if (_tags.isNotEmpty && _tags.length < 10)
            PropertyChip(
              leading: Icon(Icons.add_rounded, size: 15, color: c.textMuted),
              label: '',
              onTap: _addTag,
            ),
        ],
      ),
    );
  }

  // --- Icon and cover (#372). ---

  void _setIcon(String icon) {
    setState(() => _icon = icon);
    _changed();
  }

  void _setCover(String cover) {
    setState(() => _cover = cover);
    _changed();
  }

  Future<void> _pickIcon() async {
    final picked = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Wrap(
                spacing: 4,
                runSpacing: 4,
                children: [
                  for (final e in pageIcons)
                    InkWell(
                      borderRadius: BorderRadius.circular(8),
                      onTap: () => Navigator.pop(context, e),
                      child: Padding(
                        padding: const EdgeInsets.all(8),
                        child: Text(e, style: const TextStyle(fontSize: 26)),
                      ),
                    ),
                ],
              ),
              if (_icon.isNotEmpty)
                TextButton(onPressed: () => Navigator.pop(context, ''), child: Text(context.t('mobile.iconEntfernen'))),
            ],
          ),
        ),
      ),
    );
    if (picked != null) _setIcon(picked);
  }

  Future<void> _pickCover() async {
    final messenger = ScaffoldMessenger.of(context);
    final choice = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Wrap(
                spacing: 10,
                runSpacing: 10,
                children: [
                  for (final g in coverGradients.keys)
                    InkWell(
                      borderRadius: BorderRadius.circular(10),
                      onTap: () => Navigator.pop(context, 'gradient:$g'),
                      child: Container(
                        width: 96,
                        height: 56,
                        decoration: BoxDecoration(borderRadius: BorderRadius.circular(10), gradient: coverGradients[g]),
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: () => Navigator.pop(context, 'picture'),
                icon: const Icon(Icons.image_outlined),
                label: Text(context.t('mobile.coverBild')),
              ),
              if (_cover.isNotEmpty)
                TextButton(
                  onPressed: () => Navigator.pop(context, ''),
                  child: Text(context.t('mobile.coverEntfernen')),
                ),
            ],
          ),
        ),
      ),
    );
    if (choice == null) return;
    if (choice != 'picture') return _setCover(choice);
    final picked = await (widget.pickImage ?? _pickImage)();
    if (picked == null) return;
    try {
      _setCover(await widget.api.uploadNoteMedia(picked));
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  /// "Add icon" and "Add cover", as Notion shows them above the title: on
  /// hover with a mouse; on touch while the note has neither.
  Widget _headerActions(BuildContext context, bool touch) {
    final c = context.colors;
    final show = _headerHover || (touch && _icon.isEmpty && _cover.isEmpty && !_hasProperties);
    return AnimatedOpacity(
      opacity: show ? 1 : 0,
      duration: const Duration(milliseconds: 120),
      child: IgnorePointer(
        ignoring: !show,
        child: Wrap(
          spacing: 4,
          children: [
            if (_icon.isEmpty)
              TextButton.icon(
                onPressed: _pickIcon,
                style: TextButton.styleFrom(foregroundColor: c.textMuted),
                icon: const Icon(Icons.emoji_emotions_outlined, size: 18),
                label: Text(context.t('mobile.iconHinzu')),
              ),
            if (_cover.isEmpty)
              TextButton.icon(
                onPressed: _pickCover,
                style: TextButton.styleFrom(foregroundColor: c.textMuted),
                icon: const Icon(Icons.image_outlined, size: 18),
                label: Text(context.t('mobile.coverHinzu')),
              ),
            if (_status.isEmpty)
              TextButton.icon(
                onPressed: _pickStatus,
                style: TextButton.styleFrom(foregroundColor: c.textMuted),
                icon: const Icon(Icons.radio_button_unchecked_rounded, size: 18),
                label: Text(context.t('mobile.propStatus')),
              ),
            if (_due == null)
              TextButton.icon(
                onPressed: _pickDue,
                style: TextButton.styleFrom(foregroundColor: c.textMuted),
                icon: const Icon(Icons.event_outlined, size: 18),
                label: Text(context.t('mobile.propDatum')),
              ),
            if (_tags.isEmpty)
              TextButton.icon(
                onPressed: _addTag,
                style: TextButton.styleFrom(foregroundColor: c.textMuted),
                icon: const Icon(Icons.sell_outlined, size: 18),
                label: Text(context.t('mobile.propTags')),
              ),
          ],
        ),
      ),
    );
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
    final touch = switch (Theme.of(context).platform) {
      TargetPlatform.iOS || TargetPlatform.android => true,
      _ => false,
    };
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
                padding: const EdgeInsets.only(bottom: 24),
                children: [
                  if (_cover.isNotEmpty)
                    MouseRegion(
                      onEnter: (_) => setState(() => _headerHover = true),
                      child: GestureDetector(
                        onTap: _pickCover,
                        child: CoverBanner(api: widget.api, cover: _cover),
                      ),
                    ),
                  // A document's column (#372): centred, about 720 points
                  // on a wide window, the whole width on a phone.
                  Center(
                    child: ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 760),
                      child: SizedBox(
                        // The header in the page's margin; the blocks below
                        // use the left margin for their handles (#371).
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            // "Add icon", "Add cover" and the empty properties show while the
                            // mouse is over the header, as in Notion — not anywhere on the page.
                            MouseRegion(
                              onEnter: (_) => setState(() => _headerHover = true),
                              onExit: (_) => setState(() => _headerHover = false),
                              child: Padding(
                                padding: const EdgeInsets.symmetric(horizontal: 20),
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.stretch,
                                  children: [
                                    if (_icon.isNotEmpty)
                                      Padding(
                                        padding: EdgeInsets.only(top: _cover.isEmpty ? 8 : 0),
                                        child: Align(
                                          alignment: Alignment.centerLeft,
                                          child: Transform.translate(
                                            offset: Offset(0, _cover.isEmpty ? 0 : -34),
                                            child: GestureDetector(
                                              onTap: _pickIcon,
                                              child: Text(_icon, style: const TextStyle(fontSize: 60, height: 1.1)),
                                            ),
                                          ),
                                        ),
                                      ),
                                    _headerActions(context, touch),
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
                                      style: _titleStyle(context),
                                      maxLines: null,
                                      textCapitalization: TextCapitalization.sentences,
                                      textInputAction: TextInputAction.next,
                                      onChanged: (_) => _changed(),
                                      decoration: _bare(
                                        context,
                                        context.t('mobile.titelOptional'),
                                        _titleStyle(context),
                                      ),
                                    ),
                                    _properties(context),
                                    if (n != null && n.summary.isNotEmpty) ...[
                                      const SizedBox(height: 6),
                                      Container(
                                        padding: const EdgeInsets.all(18),
                                        decoration: BoxDecoration(
                                          color: c.surface2,
                                          borderRadius: BorderRadius.circular(20),
                                        ),
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
                                  ],
                                ),
                              ),
                            ),
                            Padding(
                              padding: const EdgeInsets.only(left: 20 - BlockEditor.gutter, right: 20),
                              child: BlockEditor(
                                key: _editor,
                                api: widget.api,
                                initial: _body,
                                hint: context.t('mobile.notizHinweis'),
                                autofocus: widget.note == null,
                                onPickImage: _addImage,
                                onChanged: (md) {
                                  _body = md;
                                  _changed();
                                },
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
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
  late final Dictation _dictation =
      widget.dictation ?? Dictation(api: widget.api, diarize: true, onSegment: (t, at, v) => _segment(true, t, at, v));

  /// The Mac's own audio, when the person includes it (#364): the other
  /// side of the call, recognised separately.
  Dictation? _others;
  bool _withMac = false;

  /// Finished segments of both sides, merged by the time their speech began;
  /// [key] is who spoke: `me`, or `s1`, `s2` … for the voices told apart
  /// (#367).
  final _segments = <({DateTime at, bool mine, String key, String text})>[];
  final _voices = Diarizer();
  final _names = <String, String>{};
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

  /// With the Mac's audio the microphone is the person themselves, and the
  /// Mac's side is split into voices; with the microphone only — a room —
  /// the microphone is split.
  void _segment(bool mine, String text, DateTime at, Float32List? voice) {
    final key = mine && _withMac ? 'me' : 's${_voices.assign(voice)}';
    _segments.add((at: at, mine: mine, key: key, text: text));
    _segments.sort((a, b) => a.at.compareTo(b.at));
  }

  String _label(String key) {
    if (key == 'me') return context.t('mobile.sprecherIch');
    if (key == 'others') return context.t('mobile.sprecherAndere');
    return _names[key] ?? context.t('mobile.sprecherN', args: {'n': key.substring(1)});
  }

  /// Whether the transcript names its speakers: always with the Mac's
  /// audio, and in a room once there is more than one voice.
  bool get _labelled => _withMac || _segments.map((s) => s.key).toSet().length > 1;

  /// On the Mac: include the Mac's audio? Only with everybody's knowledge —
  /// the question says so, and the answer that includes it says it too.
  Future<bool> _askForMacAudio() async {
    final t = context.t;
    final yes = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (context) => AlertDialog(
        title: Text(t('mobile.macTonTitel')),
        content: Text(t('mobile.macTonText')),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(t('mobile.macTonNein'))),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(t('mobile.macTonJa'))),
        ],
      ),
    );
    return yes == true;
  }

  Future<void> _start() async {
    _withMac = SystemAudio.supported && widget.dictation == null && await _askForMacAudio();
    if (!mounted) return;
    if (await _dictation.start(continuous: true)) {
      _watch.start();
      _tick = Timer.periodic(const Duration(seconds: 1), (_) => setState(() {}));
      if (_withMac) {
        final others = Dictation(
          api: widget.api,
          source: SystemAudio.stream,
          diarize: true,
          onSegment: (t, at, v) => _segment(false, t, at, v),
        )..addListener(_changed);
        _others = others;
        if (!await others.start(continuous: true) && mounted) {
          // Without the Mac's audio the meeting goes on with the microphone.
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(SnackBar(content: Text(dictationFailure(context, others.failure, others.detail))));
          others.removeListener(_changed);
          others.dispose();
          _others = null;
        }
      }
    }
    if (mounted) setState(() => _started = true);
  }

  @override
  void dispose() {
    _tick?.cancel();
    _dictation.removeListener(_changed);
    if (widget.dictation == null) _dictation.dispose();
    _others?.removeListener(_changed);
    _others?.dispose();
    super.dispose();
  }

  /// What a side says beyond its finished segments: the segment being
  /// spoken now.
  String _live(Dictation d, bool mine) {
    final done = _segments.where((x) => x.mine == mine).map((x) => x.text).join(' ');
    final all = d.text;
    return all.startsWith(done) ? all.substring(done.length).trim() : '';
  }

  /// The side's last speaker, for the words still being spoken.
  String _liveKey(bool mine) {
    for (final s in _segments.reversed) {
      if (s.mine == mine) return s.key;
    }
    return mine ? (_withMac ? 'me' : 's1') : 'others';
  }

  /// The transcript by speaker: consecutive segments of one speaker form one
  /// turn. [live] adds what is being spoken now.
  List<({String key, String text})> _turns({required bool live}) {
    final out = <({String key, String text})>[];
    void add(String key, String text) {
      if (text.isEmpty) return;
      if (out.isNotEmpty && out.last.key == key) {
        out[out.length - 1] = (key: key, text: '${out.last.text} $text');
      } else {
        out.add((key: key, text: text));
      }
    }

    for (final s in _segments) {
      add(s.key, s.text);
    }
    if (live) {
      final others = _others;
      if (others != null) add(_liveKey(false), _live(others, false));
      add(_liveKey(true), _live(_dictation, true));
    }
    return out;
  }

  /// Names for the voices, each shown with their first words; left empty, a
  /// voice keeps its number.
  Future<void> _nameSpeakers() async {
    final keys = {
      for (final s in _segments)
        if (s.key != 'me') s.key,
    }.toList()..sort();
    if (keys.isEmpty) return;
    final fields = {for (final k in keys) k: TextEditingController()};
    final first = {for (final k in keys) k: _segments.firstWhere((s) => s.key == k).text};
    final t = context.t;
    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (context) => AlertDialog(
        title: Text(t('mobile.sprecherBenennen')),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(t('mobile.sprecherBenennenHinweis'), style: context.type.bodySmall),
              for (final k in keys) ...[
                const SizedBox(height: 14),
                TextField(
                  controller: fields[k],
                  textCapitalization: TextCapitalization.words,
                  decoration: InputDecoration(
                    labelText: t('mobile.sprecherN', args: {'n': k.substring(1)}),
                    helperText: '„${first[k]!.length > 70 ? '${first[k]!.substring(0, 70)}…' : first[k]}“',
                    helperMaxLines: 2,
                  ),
                ),
              ],
            ],
          ),
        ),
        actions: [FilledButton(onPressed: () => Navigator.pop(context), child: Text(t('mobile.speichern')))],
      ),
    );
    for (final k in keys) {
      final name = fields[k]!.text.trim();
      if (name.isNotEmpty) _names[k] = name;
      fields[k]!.dispose();
    }
  }

  Future<void> _stop() async {
    _watch.stop();
    _tick?.cancel();
    final others = _others;
    var text = await _dictation.stop();
    await others?.stop();
    if (!mounted) return;
    if (_labelled && _segments.isNotEmpty) {
      await _nameSpeakers();
      if (!mounted) return;
      text = [for (final t in _turns(live: false)) '**${_label(t.key)}:** ${t.text}'].join('\n\n');
    }
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
                    child: !_labelled
                        ? Text(
                            text.isEmpty ? context.t('mobile.nochNichtsGehoert') : text,
                            style: context.type.bodyLarge?.copyWith(color: text.isEmpty ? c.textMuted : c.textPrimary),
                          )
                        : _Turns(turns: _turns(live: true), label: _label),
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

/// A meeting's transcript by speaker (#364, #367): each turn a paragraph
/// that begins with who spoke — the person themselves in the accent.
class _Turns extends StatelessWidget {
  const _Turns({required this.turns, required this.label});

  final List<({String key, String text})> turns;
  final String Function(String key) label;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final style = context.type.bodyLarge;
    if (turns.isEmpty) {
      return Text(context.t('mobile.nochNichtsGehoert'), style: style?.copyWith(color: c.textMuted));
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final t in turns)
          Padding(
            padding: const EdgeInsets.only(bottom: 10),
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(
                    text: '${label(t.key)}: ',
                    style: style?.copyWith(
                      fontWeight: FontWeight.w600,
                      color: t.key == 'me' ? c.textAccent : c.textSecondary,
                    ),
                  ),
                  TextSpan(text: t.text, style: style),
                ],
              ),
            ),
          ),
      ],
    );
  }
}

/// A note's title, set as a document's (#372): large and bold.
TextStyle? _titleStyle(BuildContext context) =>
    context.type.headlineMedium?.copyWith(fontSize: 34, fontWeight: FontWeight.w700, letterSpacing: -0.8, height: 1.15);

/// Emojis offered as a page icon (#372).
const pageIcons = [
  '📝', '📌', '📎', '📚', '📅', '🗓️', '✅', '💡', '🎯', '🚀', '⭐', '🔥', //
  '📈', '📊', '💼', '🏢', '🤝', '💬', '📣', '🧭', '🛠️', '⚙️', '🧪', '🔒', //
  '🏠', '🌱', '🌍', '✈️', '🎓', '❤️', '🙂', '🎉', '🍀', '☕', '🐞', '🧾',
];

/// The built-in covers (#372), by the names the instance knows.
const coverGradients = {
  'clay': LinearGradient(colors: [Color(0xFFCC7A5B), Color(0xFFEDC4A3)]),
  'dusk': LinearGradient(colors: [Color(0xFF3B2E5A), Color(0xFFC77D8A)]),
  'sea': LinearGradient(colors: [Color(0xFF1F4E6B), Color(0xFF6FB3B8)]),
  'moss': LinearGradient(colors: [Color(0xFF3C5A3A), Color(0xFFA3B86C)]),
  'sand': LinearGradient(colors: [Color(0xFFD9C3A0), Color(0xFFF3E7D3)]),
  'night': LinearGradient(colors: [Color(0xFF0F1A2B), Color(0xFF34495E)]),
};

/// A note's cover across the page (#372): a gradient or a picture.
class CoverBanner extends StatelessWidget {
  const CoverBanner({super.key, required this.api, required this.cover, this.height = 180});

  final CoveyApi api;
  final String cover;
  final double height;

  @override
  Widget build(BuildContext context) {
    final g = cover.startsWith('gradient:') ? coverGradients[cover.substring('gradient:'.length)] : null;
    return SizedBox(
      height: height,
      width: double.infinity,
      child: g != null
          ? DecoratedBox(decoration: BoxDecoration(gradient: g))
          : ClipRect(
              child: MediaImage(api: api, ref: cover, fit: BoxFit.cover, radius: 0),
            ),
    );
  }
}

/// The statuses a note can have (#373), the empty one first.
const noteStatuses = ['', 'todo', 'doing', 'done'];

String _dayText(DateTime? d) => d == null
    ? ''
    : '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

/// A status as a dot: grey for open, the accent while in progress, green
/// when done — and always with its word beside it.
class StatusDot extends StatelessWidget {
  const StatusDot({super.key, required this.status});

  final String status;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final color = switch (status) {
      'doing' => c.textAccent,
      'done' => c.textSuccess,
      'todo' => c.textSecondary,
      _ => c.border,
    };
    return Container(
      width: 10,
      height: 10,
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        color: status == 'done' || status == 'doing' ? color : Colors.transparent,
        border: Border.all(color: color, width: 1.6),
      ),
    );
  }
}

/// A property as a chip: a small pill that changes it on a tap, and removes
/// it with its cross.
class PropertyChip extends StatelessWidget {
  const PropertyChip({super.key, this.leading, required this.label, this.onTap, this.onRemove});

  final Widget? leading;
  final String label;
  final VoidCallback? onTap;
  final VoidCallback? onRemove;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Material(
      color: c.surface1,
      shape: const StadiumBorder(),
      child: InkWell(
        customBorder: const StadiumBorder(),
        onTap: onTap,
        child: Padding(
          padding: EdgeInsets.fromLTRB(10, 5, onRemove == null ? 10 : 4, 5),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              ?leading,
              if (leading != null && label.isNotEmpty) const SizedBox(width: 6),
              if (label.isNotEmpty) Text(label, style: context.type.labelMedium),
              if (onRemove != null)
                InkResponse(
                  onTap: onRemove,
                  radius: 14,
                  child: Padding(
                    padding: const EdgeInsets.only(left: 4),
                    child: Icon(Icons.close_rounded, size: 14, color: c.textMuted),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A view's tab above the notes, as Notion draws them over a database:
/// a word with its mark, the chosen one in the text colour and underlined,
/// the other quiet. No frame — it is a way of looking, not a control.
class _ViewTab extends StatelessWidget {
  const _ViewTab({required this.icon, required this.label, required this.selected, required this.onTap});

  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final color = selected ? c.textPrimary : c.textMuted;
    return Semantics(
      selected: selected,
      button: true,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          padding: const EdgeInsets.fromLTRB(6, 6, 6, 5),
          margin: const EdgeInsets.only(right: 6),
          decoration: BoxDecoration(
            border: Border(bottom: BorderSide(color: selected ? c.textPrimary : Colors.transparent, width: 1.5)),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 16, color: color),
              const SizedBox(width: 6),
              Text(
                label,
                style: context.type.labelLarge?.copyWith(
                  color: color,
                  fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The notes as a board by status (#373): a column per status, a card per
/// note; a card dragged to another column takes its status. With a mouse
/// a card is dragged at once, on touch after a long press.
class NotesBoard extends StatelessWidget {
  const NotesBoard({super.key, required this.notes, required this.onOpen, required this.onMove});

  final List<Note> notes;
  final ValueChanged<Note> onOpen;
  final Future<void> Function(Note note, String status) onMove;

  @override
  Widget build(BuildContext context) {
    final touch = switch (Theme.of(context).platform) {
      TargetPlatform.iOS || TargetPlatform.android => true,
      _ => false,
    };
    return Padding(
      padding: const EdgeInsets.only(top: 16),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        // Every column as tall as the tallest, and at least a hand's
        // height: an empty column is a place to drop a card, not a strip.
        child: IntrinsicHeight(
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final st in noteStatuses)
                _BoardColumn(
                  status: st,
                  notes: [
                    for (final n in notes)
                      if (n.status == st) n,
                  ],
                  onOpen: onOpen,
                  onMove: onMove,
                  touch: touch,
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _BoardColumn extends StatelessWidget {
  const _BoardColumn({
    required this.status,
    required this.notes,
    required this.onOpen,
    required this.onMove,
    required this.touch,
  });

  final String status;
  final List<Note> notes;
  final ValueChanged<Note> onOpen;
  final Future<void> Function(Note note, String status) onMove;
  final bool touch;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return DragTarget<Note>(
      onWillAcceptWithDetails: (d) => d.data.status != status,
      onAcceptWithDetails: (d) => onMove(d.data, status),
      builder: (context, candidates, _) => Container(
        width: 260,
        constraints: const BoxConstraints(minHeight: 200),
        margin: const EdgeInsets.only(right: 12),
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: candidates.isNotEmpty ? c.bgAccent : c.surface1.withValues(alpha: 0.5),
          borderRadius: BorderRadius.circular(12),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(6, 4, 6, 10),
              child: Row(
                children: [
                  StatusDot(status: status),
                  const SizedBox(width: 8),
                  Text(context.t('mobile.nstatus_${status.isEmpty ? 'none' : status}'), style: context.type.titleSmall),
                  const SizedBox(width: 6),
                  Text('${notes.length}', style: context.type.bodySmall?.copyWith(color: c.textMuted)),
                ],
              ),
            ),
            for (final n in notes)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: touch
                    ? LongPressDraggable<Note>(
                        data: n,
                        feedback: _cardFeedback(context, n),
                        childWhenDragging: Opacity(
                          opacity: 0.4,
                          child: _BoardCard(note: n, onOpen: onOpen),
                        ),
                        child: _BoardCard(note: n, onOpen: onOpen),
                      )
                    : Draggable<Note>(
                        data: n,
                        feedback: _cardFeedback(context, n),
                        childWhenDragging: Opacity(
                          opacity: 0.4,
                          child: _BoardCard(note: n, onOpen: onOpen),
                        ),
                        child: _BoardCard(note: n, onOpen: onOpen),
                      ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _cardFeedback(BuildContext context, Note n) => Material(
    color: Colors.transparent,
    child: SizedBox(
      width: 244,
      child: _BoardCard(note: n, onOpen: (_) {}),
    ),
  );
}

class _BoardCard extends StatelessWidget {
  const _BoardCard({required this.note, required this.onOpen});

  final Note note;
  final ValueChanged<Note> onOpen;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final lang = Strings.of(context).language;
    return Material(
      color: c.surface2,
      borderRadius: BorderRadius.circular(10),
      elevation: 0.5,
      child: InkWell(
        borderRadius: BorderRadius.circular(10),
        onTap: () => onOpen(note),
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (note.icon.isNotEmpty) ...[Text(note.icon), const SizedBox(width: 6)],
                  Expanded(
                    child: Text(
                      note.heading,
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                      style: context.type.titleSmall,
                    ),
                  ),
                ],
              ),
              if (note.due != null || note.tags.isNotEmpty) ...[
                const SizedBox(height: 6),
                Text(
                  [
                    if (note.due != null) DateFormat.MMMd(lang).format(note.due!),
                    ...note.tags.map((t) => '#$t'),
                  ].join(' · '),
                  style: context.type.bodySmall?.copyWith(color: c.textMuted),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
