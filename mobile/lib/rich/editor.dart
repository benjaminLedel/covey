import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../icons.dart';
import '../theme.dart';
import 'blocks.dart';
import 'inline.dart';
import 'media_image.dart';

/// The start of every text block: a zero-width space the person cannot see.
/// A TextField does not report a backspace at offset 0; with this in front,
/// that backspace deletes the sentinel, and its absence is the signal to
/// join the block with the one before (or to lift a list item to a
/// paragraph) — the way Apple Notes' backspace behaves.
const _sentinel = '​';

/// One block on the page with the controllers that edit it.
class _Entry {
  _Entry(this.block) {
    if (block.isText) {
      text = MarkdownController(text: '$_sentinel${block.text}');
    }
    if (block.kind == BlockKind.table) _buildCells();
  }

  final Block block;
  MarkdownController? text;
  final focus = FocusNode();
  List<List<TextEditingController>> cells = [];
  List<List<FocusNode>> cellFocus = [];

  void _buildCells() {
    for (final r in cells) {
      for (final c in r) {
        c.dispose();
      }
    }
    for (final r in cellFocus) {
      for (final f in r) {
        f.dispose();
      }
    }
    final rows = block.rows ?? [];
    cells = [
      for (final r in rows) [for (final c in r) TextEditingController(text: c)],
    ];
    cellFocus = [
      for (final r in rows) [for (final _ in r) FocusNode()],
    ];
  }

  void dispose() {
    text?.dispose();
    focus.dispose();
    for (final r in cells) {
      for (final c in r) {
        c.dispose();
      }
    }
    for (final r in cellFocus) {
      for (final f in r) {
        f.dispose();
      }
    }
  }
}

/// A note's body as blocks, edited the way Apple Notes edits (#344): a list
/// continues on Enter and ends on Enter in an empty item, a checklist item is
/// ticked at its circle, a table's cells are edited in place, pictures stand
/// in the text. What is stored is Markdown (blocks.dart); [onChanged] gets it
/// after every edit.
///
/// The formatting bar is not here: [EditorBar] reads and drives this editor
/// through its [BlockEditorState], so the page decides where the bar stands.
class BlockEditor extends StatefulWidget {
  const BlockEditor({
    super.key,
    required this.api,
    required this.initial,
    required this.onChanged,
    this.hint = '',
    this.autofocus = false,
  });

  final CoveyApi api;
  final String initial;
  final ValueChanged<String> onChanged;
  final String hint;
  final bool autofocus;

  @override
  State<BlockEditor> createState() => BlockEditorState();
}

class BlockEditorState extends State<BlockEditor> {
  late final List<_Entry> _entries = [for (final b in parseBlocks(widget.initial)) _Entry(b)];

  /// The block the caret is in (a table: the cell's table), for the bar.
  int? focused;
  (int, int)? focusedCell;

  /// Tells listeners — the bar — that focus or the focused block's kind
  /// changed.
  final changes = EditorSignal();

  @override
  void initState() {
    super.initState();
    for (var i = 0; i < _entries.length; i++) {
      _wire(_entries[i]);
    }
    if (widget.autofocus) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _focusText(_entries.length - 1, end: true));
    }
  }

  @override
  void dispose() {
    for (final e in _entries) {
      e.dispose();
    }
    changes.dispose();
    super.dispose();
  }

  void _wire(_Entry e) {
    e.focus.addListener(() => _focusChanged(e));
    for (var r = 0; r < e.cellFocus.length; r++) {
      for (var c = 0; c < e.cellFocus[r].length; c++) {
        final (rr, cc) = (r, c);
        e.cellFocus[r][c].addListener(() {
          if (e.cellFocus.length > rr && e.cellFocus[rr].length > cc && e.cellFocus[rr][cc].hasFocus) {
            focused = _entries.indexOf(e);
            focusedCell = (rr, cc);
            changes.ping();
          }
        });
      }
    }
  }

  void _focusChanged(_Entry e) {
    if (e.focus.hasFocus) {
      focused = _entries.indexOf(e);
      focusedCell = null;
      // The caret never sits before the sentinel.
      final t = e.text;
      if (t != null && t.selection.baseOffset == 0 && t.text.startsWith(_sentinel)) {
        t.selection = const TextSelection.collapsed(offset: 1);
      }
    }
    changes.ping();
  }

  /// Whether the caret is in this editor — a block or a table cell.
  bool get hasFocus => _entries.any((e) => e.focus.hasFocus || e.cellFocus.any((r) => r.any((f) => f.hasFocus)));

  /// The body as Markdown.
  String get markdown {
    for (final e in _entries) {
      if (e.text != null) e.block.text = e.text!.text.replaceAll(_sentinel, '');
      if (e.block.kind == BlockKind.table) {
        e.block.rows = [
          for (final r in e.cells) [for (final c in r) c.text],
        ];
      }
    }
    return writeBlocks([for (final e in _entries) e.block]);
  }

  void _emit() => widget.onChanged(markdown);

  BlockKind? get focusedKind => focused == null || focused! >= _entries.length ? null : _entries[focused!].block.kind;

  void _focusText(int i, {bool end = false, int? at}) {
    if (i < 0 || i >= _entries.length) return;
    final e = _entries[i];
    if (e.text == null) return;
    e.focus.requestFocus();
    final len = e.text!.text.length;
    e.text!.selection = TextSelection.collapsed(offset: at ?? (end ? len : 1));
  }

  void _insert(int at, Block b) {
    final e = _Entry(b);
    _wire(e);
    _entries.insert(at, e);
  }

  void _remove(int i) {
    final e = _entries.removeAt(i);
    WidgetsBinding.instance.addPostFrameCallback((_) => e.dispose());
  }

  /// Handles what a text block's edit means for the blocks: Enter splits,
  /// a deleted sentinel joins.
  void _textChanged(int i) {
    final e = _entries[i];
    final t = e.text!;
    var s = t.text;

    // Backspace at the start: the sentinel is gone.
    if (!s.startsWith(_sentinel)) {
      final rest = s.replaceAll(_sentinel, '');
      if (e.block.kind != BlockKind.paragraph) {
        // A list item, heading or quote first becomes a plain line.
        e.block.kind = BlockKind.paragraph;
        t.value = TextEditingValue(text: '$_sentinel$rest', selection: const TextSelection.collapsed(offset: 1));
      } else if (i > 0 && _entries[i - 1].text != null) {
        final prev = _entries[i - 1];
        final join = prev.text!.text.length;
        prev.text!.text = '${prev.text!.text}$rest';
        _remove(i);
        setState(() {});
        WidgetsBinding.instance.addPostFrameCallback((_) => _focusText(i - 1, at: join));
      } else if (i > 0 && rest.isEmpty) {
        // An empty line after a picture, table or divider: the line goes.
        _remove(i);
        setState(() {});
      } else {
        t.value = TextEditingValue(text: '$_sentinel$rest', selection: const TextSelection.collapsed(offset: 1));
      }
      setState(() {});
      changes.ping();
      _emit();
      return;
    }

    // Enter (or a paste with lines): split.
    final nl = s.indexOf('\n');
    if (nl >= 0) {
      final before = s.substring(0, nl);
      final after = s.substring(nl + 1);
      final beforeText = before.replaceAll(_sentinel, '');
      if (e.block.isListItem && beforeText.isEmpty && after.isEmpty) {
        // Enter in an empty list item ends the list.
        e.block.kind = BlockKind.paragraph;
        t.value = const TextEditingValue(text: _sentinel, selection: TextSelection.collapsed(offset: 1));
        setState(() {});
        changes.ping();
        _emit();
        return;
      }
      t.value = TextEditingValue(
        text: before,
        selection: TextSelection.collapsed(offset: before.length),
      );
      final lines = after.split('\n');
      final nextKind = e.block.isListItem ? e.block.kind : BlockKind.paragraph;
      var at = i + 1;
      for (var k = 0; k < lines.length; k++) {
        // The first new line continues the list; pasted lines after it are
        // read as Markdown, so a pasted checklist arrives as one.
        final b = k == 0 ? Block(nextKind, text: lines[k]) : parseBlocks(lines[k]).first;
        _insert(at++, b);
      }
      setState(() {});
      WidgetsBinding.instance.addPostFrameCallback((_) => _focusText(i + 1, at: 1));
      _emit();
      return;
    }
    _emit();
  }

  // --- What the bar can do. ---

  /// Turns the focused block into [kind] — or back into a paragraph when it
  /// already is one (the checklist button toggles).
  void setKind(BlockKind kind) {
    final i = focused;
    if (i == null || _entries[i].text == null) return;
    final b = _entries[i].block;
    b.kind = b.kind == kind && kind != BlockKind.paragraph ? BlockKind.paragraph : kind;
    if (b.kind != BlockKind.todo) b.checked = false;
    setState(() {});
    changes.ping();
    _emit();
  }

  void wrap(String mark) {
    final i = focused;
    if (i == null) return;
    _entries[i].text?.wrapSelection(mark);
    _emit();
  }

  /// A new block after the focused one (or at the end), with an empty line
  /// after it when it is the last, so there is somewhere to keep typing.
  void _insertAfterFocus(Block b) {
    final at = (focused ?? _entries.length - 1) + 1;
    _insert(at, b);
    if (at == _entries.length - 1) _insert(at + 1, Block(BlockKind.paragraph));
    setState(() {});
    _emit();
  }

  void insertTable() {
    final t = Block(
      BlockKind.table,
      rows: [
        ['', ''],
        ['', ''],
      ],
    );
    _insertAfterFocus(t);
    final i = _entries.indexWhere((e) => e.block == t);
    WidgetsBinding.instance.addPostFrameCallback((_) => _entries[i].cellFocus[0][0].requestFocus());
  }

  void insertImage(String ref) => _insertAfterFocus(Block(BlockKind.image, ref: ref));

  /// Adds a row or a column to the focused table.
  void tableAdd({required bool row}) {
    final i = focused;
    if (i == null || _entries[i].block.kind != BlockKind.table) return;
    final e = _entries[i];
    markdown; // write the cells back first
    final rows = e.block.rows!;
    if (row) {
      rows.add(List.filled(rows.first.length, ''));
    } else {
      for (final r in rows) {
        r.add('');
      }
    }
    e._buildCells();
    _wire(e);
    setState(() {});
    _emit();
  }

  /// Removes the focused table's row or column — the table itself when it is
  /// the last one.
  void tableRemove({required bool row}) {
    final i = focused;
    final cell = focusedCell;
    if (i == null || cell == null || _entries[i].block.kind != BlockKind.table) return;
    final e = _entries[i];
    markdown;
    final rows = e.block.rows!;
    if (row) {
      if (rows.length > 1) rows.removeAt(cell.$1);
    } else {
      if (rows.first.length > 1) {
        for (final r in rows) {
          r.removeAt(cell.$2);
        }
      }
    }
    if (rows.length == 1 && rows.first.length == 1 && rows.first.first.isEmpty) {
      removeBlock(i);
      return;
    }
    e._buildCells();
    _wire(e);
    focusedCell = null;
    setState(() {});
    changes.ping();
    _emit();
  }

  void removeBlock(int i) {
    _remove(i);
    if (_entries.isEmpty) _insert(0, Block(BlockKind.paragraph));
    focused = null;
    focusedCell = null;
    setState(() {});
    changes.ping();
    _emit();
  }

  /// Dictation writes into the block the caret is in, or a new line at the
  /// end. [base] is that block's text when dictation started.
  ({int index, String base}) beginDictation() {
    var i = focused;
    if (i == null || _entries[i].text == null) {
      _insert(_entries.length, Block(BlockKind.paragraph));
      i = _entries.length - 1;
      setState(() {});
    }
    return (index: i, base: _entries[i].text!.text.replaceAll(_sentinel, ''));
  }

  void dictate(({int index, String base}) at, String heard) {
    if (at.index >= _entries.length) return;
    final t = _entries[at.index].text;
    if (t == null) return;
    final joined = [at.base, heard].where((s) => s.trim().isNotEmpty).join(at.base.isEmpty ? '' : ' ');
    t.value = TextEditingValue(
      text: '$_sentinel$joined',
      selection: TextSelection.collapsed(offset: joined.length + 1),
    );
    _emit();
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    // A numbered item's number counts its run, as the Markdown will.
    final numbers = <int>[];
    var n = 0;
    for (final e in _entries) {
      n = e.block.kind == BlockKind.numbered ? n + 1 : 0;
      numbers.add(n);
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < _entries.length; i++)
          KeyedSubtree(key: ObjectKey(_entries[i]), child: _blockView(context, c, i, _entries[i], numbers[i])),
      ],
    );
  }

  Widget _blockView(BuildContext context, CoveyColors c, int i, _Entry e, int number) {
    final type = context.type;
    switch (e.block.kind) {
      case BlockKind.divider:
        return GestureDetector(
          onLongPress: () => _confirmRemove(i),
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 12),
            child: Divider(color: c.border),
          ),
        );
      case BlockKind.image:
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: GestureDetector(
            onLongPress: () => _confirmRemove(i),
            child: MediaImage(api: widget.api, ref: e.block.ref),
          ),
        );
      case BlockKind.table:
        return _table(context, c, i, e);
      default:
        break;
    }

    e.text!
      ..markerColor = c.textMuted.withValues(alpha: 0.55)
      ..linkColor = c.textAccent;
    final style = switch (e.block.kind) {
      BlockKind.heading1 => type.headlineSmall,
      BlockKind.heading2 => type.titleLarge,
      BlockKind.heading3 => type.titleMedium,
      BlockKind.quote => type.bodyLarge?.copyWith(color: c.textSecondary, fontStyle: FontStyle.italic),
      BlockKind.todo when e.block.checked => type.bodyLarge?.copyWith(
        color: c.textMuted,
        decoration: TextDecoration.lineThrough,
        decorationColor: c.textMuted,
      ),
      _ => type.bodyLarge,
    };
    final field = TextField(
      controller: e.text,
      focusNode: e.focus,
      maxLines: null,
      style: style,
      cursorColor: c.textAccent,
      textCapitalization: TextCapitalization.sentences,
      keyboardType: TextInputType.multiline,
      onChanged: (_) => _textChanged(i),
      decoration: InputDecoration(
        isDense: true,
        filled: false,
        border: InputBorder.none,
        enabledBorder: InputBorder.none,
        focusedBorder: InputBorder.none,
        contentPadding: const EdgeInsets.symmetric(vertical: 5),
        hintText: _entries.length == 1 && i == 0 ? widget.hint : null,
        hintStyle: style?.copyWith(color: c.textMuted),
      ),
    );

    Widget? lead;
    switch (e.block.kind) {
      case BlockKind.bullet:
        lead = Padding(
          padding: const EdgeInsets.only(top: 13, right: 12, left: 6),
          child: Container(
            width: 6,
            height: 6,
            decoration: BoxDecoration(color: c.textPrimary, shape: BoxShape.circle),
          ),
        );
      case BlockKind.numbered:
        lead = Padding(
          padding: const EdgeInsets.only(top: 5, right: 8),
          child: SizedBox(
            width: 22,
            child: Text(
              '$number.',
              textAlign: TextAlign.right,
              style: type.bodyLarge?.copyWith(fontFeatures: const [FontFeature.tabularFigures()]),
            ),
          ),
        );
      case BlockKind.todo:
        // A circle, as Apple Notes draws it; ticked, it fills with the accent.
        lead = Semantics(
          checked: e.block.checked,
          button: true,
          label: Strings.of(context).t('mobile.checkliste'),
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: () {
              setState(() => e.block.checked = !e.block.checked);
              _emit();
            },
            child: Padding(
              padding: const EdgeInsets.fromLTRB(2, 4, 10, 4),
              child: Container(
                width: 22,
                height: 22,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: e.block.checked ? c.textAccent : Colors.transparent,
                  border: Border.all(color: e.block.checked ? c.textAccent : c.textMuted, width: 1.6),
                ),
                child: e.block.checked ? Icon(Icons.check_rounded, size: 15, color: c.surface2) : null,
              ),
            ),
          ),
        );
      case BlockKind.quote:
        lead = Padding(
          padding: const EdgeInsets.only(right: 12),
          child: Container(width: 1, height: 26, color: c.border),
        );
      default:
        break;
    }
    final top = switch (e.block.kind) {
      BlockKind.heading1 => 14.0,
      BlockKind.heading2 => 12.0,
      BlockKind.heading3 => 8.0,
      _ => 0.0,
    };
    return Padding(
      padding: EdgeInsets.only(top: top),
      child: lead == null
          ? field
          : Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                lead,
                Expanded(child: field),
              ],
            ),
    );
  }

  Widget _table(BuildContext context, CoveyColors c, int i, _Entry e) {
    final type = context.type;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Container(
          decoration: BoxDecoration(
            border: Border.all(color: c.border),
            borderRadius: BorderRadius.circular(10),
          ),
          clipBehavior: Clip.antiAlias,
          child: Table(
            defaultColumnWidth: const IntrinsicColumnWidth(),
            border: TableBorder.symmetric(inside: BorderSide(color: c.border, width: 0.6)),
            children: [
              for (var r = 0; r < e.cells.length; r++)
                TableRow(
                  decoration: BoxDecoration(color: r == 0 ? c.surface1.withValues(alpha: 0.6) : c.surface2),
                  children: [
                    for (var col = 0; col < e.cells[r].length; col++)
                      ConstrainedBox(
                        constraints: const BoxConstraints(minWidth: 96, maxWidth: 220),
                        child: TextField(
                          controller: e.cells[r][col],
                          focusNode: e.cellFocus[r][col],
                          maxLines: null,
                          style: r == 0 ? type.titleSmall : type.bodyMedium?.copyWith(color: c.textPrimary),
                          cursorColor: c.textAccent,
                          onChanged: (_) => _emit(),
                          decoration: const InputDecoration(
                            isDense: true,
                            filled: false,
                            border: InputBorder.none,
                            enabledBorder: InputBorder.none,
                            focusedBorder: InputBorder.none,
                            contentPadding: EdgeInsets.symmetric(horizontal: 10, vertical: 9),
                          ),
                        ),
                      ),
                  ],
                ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _confirmRemove(int i) async {
    final t = Strings.of(context).t;
    final ok = await showModalBottomSheet<bool>(
      context: context,
      builder: (context) => SafeArea(
        child: ListTile(
          leading: Icon(AppIcons.delete.of(context), color: context.colors.textDanger),
          title: Text(t('mobile.entfernen'), style: TextStyle(color: context.colors.textDanger)),
          onTap: () => Navigator.pop(context, true),
        ),
      ),
    );
    if (ok == true) removeBlock(i);
  }
}

/// Focus moved or a block changed kind — what the bar redraws on.
class EditorSignal extends ChangeNotifier {
  void ping() => notifyListeners();
}
