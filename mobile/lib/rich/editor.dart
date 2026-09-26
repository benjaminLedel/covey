import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

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

/// The width of the column the block handles stand in (#371).
const _gutter = BlockEditor.gutter;

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

  /// A toggle's content (#371), made when the block first becomes one.
  MarkdownController? _body;
  final bodyFocus = FocusNode();
  MarkdownController get body => _body ??= MarkdownController(text: block.body);
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
    _body?.dispose();
    bodyFocus.dispose();
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
    this.onPickImage,
  });

  final CoveyApi api;
  final String initial;
  final ValueChanged<String> onChanged;
  final String hint;
  final bool autofocus;

  /// Asks the page for a picture — "/picture" in the block menu (#371).
  final VoidCallback? onPickImage;

  /// The column at the left where the blocks' handles stand (#371). It is
  /// part of the editor: the page puts the editor that much further left, so
  /// the text lines up with the title and the handles stand in the margin —
  /// within the editor's area, where the mouse reaches them.
  static const gutter = 20.0;

  @override
  State<BlockEditor> createState() => BlockEditorState();
}

class BlockEditorState extends State<BlockEditor> {
  late final List<_Entry> _entries = [
    for (final b in parseBlocks(widget.initial)) _Entry(b),
    // After a table, divider or picture at the end, a line to go on writing.
    if (!(parseBlocks(widget.initial).last.isText)) _Entry(Block(BlockKind.paragraph)),
  ];

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
    e.focus.onKeyEvent = (_, event) => _slashKey(e, event);
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
      if (e.block.kind == BlockKind.toggle) e.block.body = e.body.text;
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
        // A list item, heading or quote first becomes a plain line; a
        // toggle's content comes out as lines after it.
        if (e.block.kind == BlockKind.toggle) {
          final content = e.body.text;
          if (content.trim().isNotEmpty) {
            var at = i + 1;
            for (final b in parseBlocks(content)) {
              _insert(at++, b);
            }
          }
        }
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
    _updateSlash(i);
    _emit();
  }

  // --- The block menu (#371). ---

  /// The block whose "/" opened the menu, what was typed after it, and
  /// where the "/" stands in the block's text (sentinel included).
  ({int index, String query, int at})? slash;
  int _pick = 0;

  List<_SlashOption> get _slashOptions {
    final q = slash?.query.toLowerCase() ?? '';
    final t = Strings.of(context).t;
    return [
      for (final o in _SlashOption.all)
        if (q.isEmpty || t(o.label).toLowerCase().contains(q) || o.aliases.any((a) => a.startsWith(q))) o,
    ];
  }

  /// A "/" at the start of a block or after a space, with a word after it
  /// up to the caret, opens the menu — anywhere in a line, as in Notion;
  /// anything else closes it.
  void _updateSlash(int i) {
    final t = _entries[i].text!;
    final s = t.text;
    final caret = t.selection.baseOffset;
    ({int index, String query, int at})? next;
    if (t.selection.isCollapsed && caret > 0 && caret <= s.length) {
      final at = s.lastIndexOf('/', caret - 1);
      if (at >= 0) {
        final before = at == 0 ? '' : s[at - 1];
        final query = s.substring(at + 1, caret);
        final starts = before.isEmpty || before == _sentinel || before.trim().isEmpty;
        if (starts && query.length <= 24 && !query.contains(RegExp(r'\s'))) {
          next = (index: i, query: query, at: at);
        }
      }
    }
    if (next != slash) {
      setState(() {
        slash = next;
        _pick = 0;
      });
    }
  }

  void closeSlash() {
    if (slash == null) return;
    setState(() => slash = null);
  }

  KeyEventResult _slashKey(_Entry e, KeyEvent event) {
    final sl = slash;
    if (sl == null || _entries.indexOf(e) != sl.index || event is KeyUpEvent) return KeyEventResult.ignored;
    final options = _slashOptions;
    switch (event.logicalKey) {
      case LogicalKeyboardKey.arrowDown:
        setState(() => _pick = options.isEmpty ? 0 : (_pick + 1) % options.length);
        return KeyEventResult.handled;
      case LogicalKeyboardKey.arrowUp:
        setState(() => _pick = options.isEmpty ? 0 : (_pick - 1 + options.length) % options.length);
        return KeyEventResult.handled;
      case LogicalKeyboardKey.enter:
      case LogicalKeyboardKey.numpadEnter:
        if (options.isEmpty) return KeyEventResult.ignored;
        _applySlash(options[_pick.clamp(0, options.length - 1)]);
        return KeyEventResult.handled;
      case LogicalKeyboardKey.escape:
        closeSlash();
        return KeyEventResult.handled;
      default:
        return KeyEventResult.ignored;
    }
  }

  /// Turns the block the menu was opened in into what was chosen — its "/"
  /// and word removed. A divider, table or picture takes the empty block's
  /// place, with a line after it to go on writing.
  /// Applies what was chosen, its "/" and word removed. In an otherwise
  /// empty block the block turns into it — a divider, table or picture
  /// takes the empty block's place; in a line with text, it comes as a new
  /// block after the line, as Notion does.
  void _applySlash(_SlashOption o) {
    final sl = slash;
    if (sl == null || sl.index >= _entries.length) return;
    final i = sl.index;
    final e = _entries[i];
    final s = e.text!.text;
    final end = (sl.at + 1 + sl.query.length).clamp(0, s.length);
    final rest = (s.substring(0, sl.at) + s.substring(end)).replaceAll(_sentinel, '');
    slash = null;
    final kind = o.kind;
    if (rest.trim().isEmpty) {
      e.text!.value = const TextEditingValue(text: _sentinel, selection: TextSelection.collapsed(offset: 1));
      if (kind != null) {
        e.block.kind = kind;
        e.block.checked = false;
        if (kind == BlockKind.toggle) e.block.open = true;
        setState(() {});
        changes.ping();
        _emit();
        WidgetsBinding.instance.addPostFrameCallback((_) => _focusText(i));
        return;
      }
      focused = i;
      switch (o.id) {
        case 'divider':
          _replaceEmpty(i, Block(BlockKind.divider));
        case 'table':
          _remove(i);
          focused = i - 1;
          insertTable();
        case 'image':
          setState(() {});
          widget.onPickImage?.call();
      }
      return;
    }
    // The line keeps its text; what was chosen comes after it.
    e.text!.value = TextEditingValue(
      text: '$_sentinel${rest.trimRight()}',
      selection: TextSelection.collapsed(offset: rest.trimRight().length + 1),
    );
    focused = i;
    if (kind != null) {
      _insert(i + 1, Block(kind, open: kind == BlockKind.toggle));
      setState(() {});
      changes.ping();
      _emit();
      WidgetsBinding.instance.addPostFrameCallback((_) => _focusText(i + 1));
      return;
    }
    switch (o.id) {
      case 'divider':
        _insertAfterFocus(Block(BlockKind.divider));
      case 'table':
        insertTable();
      case 'image':
        setState(() {});
        widget.onPickImage?.call();
    }
  }

  void _replaceEmpty(int i, Block b) {
    _remove(i);
    _insert(i, b);
    if (i == _entries.length - 1) _insert(i + 1, Block(BlockKind.paragraph));
    setState(() {});
    _emit();
    WidgetsBinding.instance.addPostFrameCallback((_) => _focusText(i + 1));
  }

  // --- Moving blocks (#371). ---

  int? _hover;

  void _move(int from, int to) {
    if (to == from) return;
    final e = _entries.removeAt(from);
    _entries.insert(to, e);
    focused = null;
    slash = null;
    setState(() {});
    changes.ping();
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

  /// The block's menu (#371), from its handle: delete, duplicate, or turn
  /// a text block into another kind. A menu at the handle with a mouse, a
  /// sheet from below on touch.
  Future<void> _blockMenu(BuildContext handle, int i) async {
    if (i >= _entries.length) return;
    final t = Strings.of(context).t;
    final isText = _entries[i].block.isText;
    final touch = switch (Theme.of(context).platform) {
      TargetPlatform.iOS || TargetPlatform.android => true,
      _ => false,
    };
    String? choice;
    if (touch) {
      choice = await showModalBottomSheet<String>(
        context: context,
        builder: (context) => SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (isText)
                ListTile(
                  leading: const Icon(Icons.swap_horiz_rounded),
                  title: Text(t('mobile.blockUmwandeln')),
                  onTap: () => Navigator.pop(context, 'turn'),
                ),
              ListTile(
                leading: const Icon(Icons.copy_rounded),
                title: Text(t('mobile.blockDuplizieren')),
                onTap: () => Navigator.pop(context, 'duplicate'),
              ),
              ListTile(
                leading: Icon(Icons.delete_outline_rounded, color: context.colors.textDanger),
                title: Text(t('mobile.blockLoeschen'), style: TextStyle(color: context.colors.textDanger)),
                onTap: () => Navigator.pop(context, 'delete'),
              ),
            ],
          ),
        ),
      );
    } else {
      final box = handle.findRenderObject()! as RenderBox;
      final at = box.localToGlobal(Offset(0, box.size.height));
      Widget entry(IconData icon, String label, {Color? color}) => Row(
        children: [
          Icon(icon, size: 18, color: color),
          const SizedBox(width: 12),
          Text(label, style: TextStyle(color: color)),
        ],
      );
      final danger = context.colors.textDanger;
      choice = await showMenu<String>(
        context: context,
        position: RelativeRect.fromLTRB(at.dx, at.dy + 2, at.dx + 1, at.dy + 1),
        items: [
          if (isText) PopupMenuItem(value: 'turn', child: entry(Icons.swap_horiz_rounded, t('mobile.blockUmwandeln'))),
          PopupMenuItem(value: 'duplicate', child: entry(Icons.copy_rounded, t('mobile.blockDuplizieren'))),
          PopupMenuItem(
            value: 'delete',
            child: entry(Icons.delete_outline_rounded, t('mobile.blockLoeschen'), color: danger),
          ),
        ],
      );
    }
    if (!mounted || choice == null || i >= _entries.length) return;
    switch (choice) {
      case 'delete':
        removeBlock(i);
      case 'duplicate':
        markdown; // the block's text written back first
        _insert(i + 1, _entries[i].block.copy());
        setState(() {});
        _emit();
      case 'turn':
        await _turnInto(i);
    }
  }

  Future<void> _turnInto(int i) async {
    final t = Strings.of(context).t;
    final kind = await showModalBottomSheet<BlockKind>(
      context: context,
      builder: (context) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          children: [
            for (final o in _SlashOption.all)
              if (o.kind != null)
                ListTile(
                  leading: Icon(o.icon),
                  title: Text(t(o.label)),
                  selected: _entries[i].block.kind == o.kind,
                  onTap: () => Navigator.pop(context, o.kind),
                ),
          ],
        ),
      ),
    );
    if (kind == null || !mounted || i >= _entries.length) return;
    final b = _entries[i].block;
    b.kind = kind;
    b.checked = false;
    if (kind == BlockKind.toggle) b.open = true;
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
    final touch = switch (Theme.of(context).platform) {
      TargetPlatform.iOS || TargetPlatform.android => true,
      _ => false,
    };
    // The handle's gutter belongs to each block: the mouse goes from the
    // text to the handle without leaving the block.
    return ReorderableListView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      buildDefaultDragHandles: false,
      padding: EdgeInsets.zero,
      itemCount: _entries.length,
      onReorderItem: _move,
      proxyDecorator: (child, _, _) => Material(color: c.surface2, elevation: 4, child: child),
      itemBuilder: (context, i) {
        final e = _entries[i];
        final handle = _hover == i || (touch && focused == i);
        return MouseRegion(
          key: ObjectKey(e),
          onEnter: (_) => setState(() => _hover = i),
          onExit: (_) {
            if (_hover == i) setState(() => _hover = null);
          },
          child: Stack(
            clipBehavior: Clip.none,
            children: [
              Padding(
                padding: const EdgeInsets.only(left: _gutter),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _blockView(context, c, i, e, numbers[i]),
                    if (slash?.index == i) _SlashMenu(editor: this, options: _slashOptions, pick: _pick),
                  ],
                ),
              ),
              // The handle, in the gutter beside the block's first line.
              Positioned(
                left: 0,
                top: 4,
                child: AnimatedOpacity(
                  opacity: handle ? 1 : 0,
                  duration: const Duration(milliseconds: 120),
                  child: IgnorePointer(
                    ignoring: !handle,
                    // A click opens the block's menu; a drag moves the block —
                    // the drag only wins once the pointer has moved.
                    child: Builder(
                      builder: (handleContext) => GestureDetector(
                        onTap: () => _blockMenu(handleContext, i),
                        child: ReorderableDragStartListener(
                          index: i,
                          child: MouseRegion(
                            cursor: SystemMouseCursors.grab,
                            child: Semantics(
                              button: true,
                              label: Strings.of(context).t('mobile.blockMenue'),
                              child: Icon(Icons.drag_indicator_rounded, size: 18, color: c.textMuted),
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ],
          ),
        );
      },
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
    if (e.block.kind == BlockKind.toggle && e._body == null) e.body.text = e.block.body;

    e.text!
      ..markerColor = c.textMuted.withValues(alpha: 0.55)
      ..linkColor = c.textAccent;
    final style = switch (e.block.kind) {
      BlockKind.heading1 => type.headlineSmall,
      BlockKind.heading2 => type.titleLarge,
      BlockKind.heading3 => type.titleMedium,
      BlockKind.quote => type.bodyLarge?.copyWith(color: c.textSecondary, fontStyle: FontStyle.italic),
      BlockKind.toggle => type.bodyLarge?.copyWith(fontWeight: FontWeight.w600),
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
      case BlockKind.toggle:
        // The triangle folds the content away, as in Notion.
        lead = Semantics(
          button: true,
          expanded: e.block.open,
          label: Strings.of(context).t('mobile.block_toggle'),
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: () => setState(() => e.block.open = !e.block.open),
            child: Padding(
              padding: const EdgeInsets.fromLTRB(0, 5, 6, 5),
              child: AnimatedRotation(
                turns: e.block.open ? 0.25 : 0,
                duration: const Duration(milliseconds: 150),
                child: Icon(Icons.play_arrow_rounded, size: 20, color: c.textSecondary),
              ),
            ),
          ),
        );
      case BlockKind.callout:
        lead = GestureDetector(
          onTap: () => _pickEmoji(e),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(0, 3, 10, 0),
            child: Text(e.block.emoji, style: const TextStyle(fontSize: 20)),
          ),
        );
      default:
        break;
    }
    if (e.block.kind == BlockKind.callout) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: Container(
          padding: const EdgeInsets.fromLTRB(14, 8, 14, 8),
          decoration: BoxDecoration(
            color: c.textPrimary.withValues(alpha: 0.045),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              lead!,
              Expanded(child: field),
            ],
          ),
        ),
      );
    }
    if (e.block.kind == BlockKind.toggle) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              lead!,
              Expanded(child: field),
            ],
          ),
          if (e.block.open)
            Padding(
              padding: const EdgeInsets.only(left: 26),
              child: TextField(
                controller: e.body..markerColor = c.textMuted.withValues(alpha: 0.55),
                focusNode: e.bodyFocus,
                maxLines: null,
                style: type.bodyLarge,
                cursorColor: c.textAccent,
                textCapitalization: TextCapitalization.sentences,
                keyboardType: TextInputType.multiline,
                onChanged: (_) => _emit(),
                decoration: InputDecoration(
                  isDense: true,
                  filled: false,
                  border: InputBorder.none,
                  enabledBorder: InputBorder.none,
                  focusedBorder: InputBorder.none,
                  contentPadding: const EdgeInsets.symmetric(vertical: 5),
                  hintText: Strings.of(context).t('mobile.toggleLeer'),
                  hintStyle: type.bodyLarge?.copyWith(color: c.textMuted),
                ),
              ),
            ),
        ],
      );
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

  /// A table as Notion draws one: thin lines, the header row tinted, the
  /// body in the page's own colour — which reads the same in light and dark.
  /// A "+" at the right adds a column and one below adds a row; they show on
  /// hover with a mouse, and on touch while a cell has the caret.
  Widget _table(BuildContext context, CoveyColors c, int i, _Entry e) {
    final type = context.type;
    final tint = c.textPrimary.withValues(alpha: 0.045);
    final line = BorderSide(color: c.border, width: 0.8);
    final touch = switch (Theme.of(context).platform) {
      TargetPlatform.iOS || TargetPlatform.android => true,
      _ => false,
    };
    final active = _hover == i || (touch && focused == i && focusedCell != null);
    Widget plus({required bool row}) => AnimatedOpacity(
      opacity: active ? 1 : 0,
      duration: const Duration(milliseconds: 120),
      child: IgnorePointer(
        ignoring: !active,
        child: Tooltip(
          message: Strings.of(context).t(row ? 'mobile.zeileHinzu' : 'mobile.spalteHinzu'),
          child: InkWell(
            borderRadius: BorderRadius.circular(6),
            onTap: () {
              focused = i;
              tableAdd(row: row);
            },
            child: Container(
              width: row ? double.infinity : 22,
              height: row ? 22 : null,
              alignment: Alignment.center,
              decoration: BoxDecoration(color: tint, borderRadius: BorderRadius.circular(6)),
              child: Icon(Icons.add_rounded, size: 16, color: c.textMuted),
            ),
          ),
        ),
      ),
    );
    final table = Table(
      defaultColumnWidth: const IntrinsicColumnWidth(),
      border: TableBorder(
        top: line,
        bottom: line,
        left: line,
        right: line,
        horizontalInside: line,
        verticalInside: line,
      ),
      children: [
        for (var r = 0; r < e.cells.length; r++)
          TableRow(
            decoration: BoxDecoration(color: r == 0 ? tint : null),
            children: [
              for (var col = 0; col < e.cells[r].length; col++)
                ConstrainedBox(
                  constraints: const BoxConstraints(minWidth: 120, maxWidth: 260),
                  child: TextField(
                    controller: e.cells[r][col],
                    focusNode: e.cellFocus[r][col],
                    maxLines: null,
                    style: r == 0
                        ? type.bodyMedium?.copyWith(fontWeight: FontWeight.w600, color: c.textPrimary)
                        : type.bodyMedium?.copyWith(color: c.textPrimary),
                    cursorColor: c.textAccent,
                    onChanged: (_) => _emit(),
                    decoration: const InputDecoration(
                      isDense: true,
                      filled: false,
                      border: InputBorder.none,
                      enabledBorder: InputBorder.none,
                      focusedBorder: InputBorder.none,
                      contentPadding: EdgeInsets.symmetric(horizontal: 10, vertical: 8),
                    ),
                  ),
                ),
            ],
          ),
      ],
    );
    // The "+" below is as wide as the table and its "+" at the right: both
    // scroll with the table when it is wider than the page.
    return Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 4),
      child: Align(
        alignment: Alignment.centerLeft,
        child: SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          child: IntrinsicWidth(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                IntrinsicHeight(
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [table, const SizedBox(width: 4), plus(row: false)],
                  ),
                ),
                const SizedBox(height: 4),
                Padding(padding: const EdgeInsets.only(right: 26), child: plus(row: true)),
              ],
            ),
          ),
        ),
      ),
    );
  }

  static const _emojis = ['💡', '⚠️', '✅', '❗', '📌', 'ℹ️', '🔥', '📝', '🎯', '❓'];

  /// A callout's icon: the next one of a few, or a chosen one.
  Future<void> _pickEmoji(_Entry e) async {
    final picked = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final em in _emojis)
                InkWell(
                  borderRadius: BorderRadius.circular(10),
                  onTap: () => Navigator.pop(context, em),
                  child: Padding(
                    padding: const EdgeInsets.all(10),
                    child: Text(em, style: const TextStyle(fontSize: 26)),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
    if (picked == null) return;
    setState(() => e.block.emoji = picked);
    _emit();
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

/// One entry of the block menu: what it makes, how it is called, and the
/// English words that find it in any language.
class _SlashOption {
  const _SlashOption(this.id, this.label, this.icon, this.aliases, [this.kind]);

  final String id;
  final String label;
  final IconData icon;
  final List<String> aliases;
  final BlockKind? kind;

  static const all = [
    _SlashOption('text', 'mobile.block_text', Icons.notes_rounded, ['text', 'paragraph', 'p'], BlockKind.paragraph),
    _SlashOption('h1', 'mobile.block_h1', Icons.title_rounded, ['h1', 'heading', 'title'], BlockKind.heading1),
    _SlashOption('h2', 'mobile.block_h2', Icons.title_rounded, ['h2', 'heading'], BlockKind.heading2),
    _SlashOption('h3', 'mobile.block_h3', Icons.title_rounded, ['h3', 'heading'], BlockKind.heading3),
    _SlashOption('bullet', 'mobile.block_bullet', Icons.format_list_bulleted_rounded, [
      'bullet',
      'list',
      'ul',
    ], BlockKind.bullet),
    _SlashOption('numbered', 'mobile.block_numbered', Icons.format_list_numbered_rounded, [
      'numbered',
      'ol',
      '1.',
    ], BlockKind.numbered),
    _SlashOption('todo', 'mobile.block_todo', Icons.check_circle_outline_rounded, [
      'todo',
      'check',
      'task',
    ], BlockKind.todo),
    _SlashOption('toggle', 'mobile.block_toggle', Icons.arrow_right_rounded, [
      'toggle',
      'details',
      'fold',
    ], BlockKind.toggle),
    _SlashOption('callout', 'mobile.block_callout', Icons.lightbulb_outline_rounded, [
      'callout',
      'note',
      'info',
    ], BlockKind.callout),
    _SlashOption('quote', 'mobile.block_quote', Icons.format_quote_rounded, ['quote', 'cite'], BlockKind.quote),
    _SlashOption('divider', 'mobile.block_divider', Icons.horizontal_rule_rounded, ['divider', 'line', 'hr', '---']),
    _SlashOption('table', 'mobile.block_table', Icons.table_chart_outlined, ['table', 'grid']),
    _SlashOption('image', 'mobile.block_image', Icons.image_outlined, ['image', 'picture', 'photo', 'img']),
  ];
}

/// The block menu under the block that opened it (#371).
class _SlashMenu extends StatelessWidget {
  const _SlashMenu({required this.editor, required this.options, required this.pick});

  final BlockEditorState editor;
  final List<_SlashOption> options;
  final int pick;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final t = Strings.of(context).t;
    return Padding(
      padding: const EdgeInsets.only(top: 4, bottom: 8),
      child: Align(
        alignment: Alignment.centerLeft,
        child: Container(
          constraints: const BoxConstraints(maxWidth: 320, maxHeight: 340),
          decoration: BoxDecoration(
            color: c.surface2,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: c.border),
            boxShadow: [BoxShadow(color: c.shadow, blurRadius: 18, offset: const Offset(0, 6))],
          ),
          child: options.isEmpty
              ? Padding(
                  padding: const EdgeInsets.all(14),
                  child: Text(t('mobile.blockKeiner'), style: context.type.bodyMedium?.copyWith(color: c.textMuted)),
                )
              : ListView(
                  shrinkWrap: true,
                  padding: const EdgeInsets.symmetric(vertical: 6),
                  children: [
                    for (var k = 0; k < options.length; k++)
                      InkWell(
                        onTap: () => editor._applySlash(options[k]),
                        child: Container(
                          color: k == pick ? c.surface1 : null,
                          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
                          child: Row(
                            children: [
                              Container(
                                width: 30,
                                height: 30,
                                decoration: BoxDecoration(
                                  color: c.surface0,
                                  borderRadius: BorderRadius.circular(6),
                                  border: Border.all(color: c.border),
                                ),
                                child: Icon(options[k].icon, size: 18, color: c.textSecondary),
                              ),
                              const SizedBox(width: 12),
                              Expanded(child: Text(t(options[k].label), style: context.type.bodyMedium)),
                            ],
                          ),
                        ),
                      ),
                  ],
                ),
        ),
      ),
    );
  }
}
