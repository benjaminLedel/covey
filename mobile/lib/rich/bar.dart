import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../i18n.dart';
import '../icons.dart';
import '../theme.dart';
import 'blocks.dart';
import 'editor.dart';

/// The formatting bar above the keyboard (#344), as Apple Notes has it:
/// the text style, the checklist, lists, bold and italic, a table, a
/// picture, dictation. Inside a table it becomes the table's bar — a row, a
/// column, and their removal.
class EditorBar extends StatelessWidget {
  const EditorBar({
    super.key,
    required this.editor,
    required this.onImage,
    required this.onDictate,
    required this.dictating,
  });

  final GlobalKey<BlockEditorState> editor;
  final VoidCallback onImage;
  final VoidCallback onDictate;
  final bool dictating;

  @override
  Widget build(BuildContext context) {
    final state = editor.currentState;
    if (state == null) return const SizedBox.shrink();
    return ListenableBuilder(
      listenable: state.changes,
      builder: (context, _) {
        final c = context.colors;
        final kind = state.focusedKind;
        final inTable = kind == BlockKind.table && state.focusedCell != null;
        final apple = isApple(context);
        Widget button(IconData icon, String label, VoidCallback? onTap, {bool on = false}) => Semantics(
          button: true,
          selected: on,
          child: IconButton(
            onPressed: onTap,
            tooltip: label,
            icon: Icon(icon, size: 22),
            color: on ? c.textAccent : c.textPrimary,
            style: IconButton.styleFrom(
              backgroundColor: on ? c.bgAccent : Colors.transparent,
              minimumSize: const Size(44, 44),
            ),
          ),
        );
        final t = Strings.of(context).t;
        final items = inTable
            ? [
                button(
                  apple ? CupertinoIcons.table : Icons.table_rows_outlined,
                  t('mobile.zeileNeu'),
                  () => state.tableAdd(row: true),
                ),
                button(
                  apple ? CupertinoIcons.table_badge_more : Icons.view_column_outlined,
                  t('mobile.spalteNeu'),
                  () => state.tableAdd(row: false),
                ),
                button(
                  apple ? CupertinoIcons.minus_rectangle : Icons.remove_circle_outline,
                  t('mobile.zeileWeg'),
                  () => state.tableRemove(row: true),
                ),
                button(
                  apple ? CupertinoIcons.minus_square : Icons.remove_road_outlined,
                  t('mobile.spalteWeg'),
                  () => state.tableRemove(row: false),
                ),
              ]
            : [
                button(
                  apple ? CupertinoIcons.textformat : Icons.text_fields_rounded,
                  t('mobile.stil'),
                  kind == null ? null : () => _pickStyle(context, state),
                ),
                button(
                  apple ? CupertinoIcons.checkmark_circle : Icons.check_circle_outline,
                  t('mobile.checkliste'),
                  kind == null ? null : () => state.setKind(BlockKind.todo),
                  on: kind == BlockKind.todo,
                ),
                button(
                  apple ? CupertinoIcons.list_bullet : Icons.format_list_bulleted_rounded,
                  t('mobile.liste'),
                  kind == null ? null : () => state.setKind(BlockKind.bullet),
                  on: kind == BlockKind.bullet,
                ),
                button(
                  apple ? CupertinoIcons.bold : Icons.format_bold_rounded,
                  t('mobile.fett'),
                  kind == null ? null : () => state.wrap('**'),
                ),
                button(
                  apple ? CupertinoIcons.italic : Icons.format_italic_rounded,
                  t('mobile.kursiv'),
                  kind == null ? null : () => state.wrap('_'),
                ),
                button(
                  apple ? CupertinoIcons.table : Icons.table_chart_outlined,
                  t('mobile.tabelle'),
                  state.insertTable,
                ),
                button(apple ? CupertinoIcons.photo : Icons.image_outlined, t('mobile.bild'), onImage),
                button(
                  dictating ? AppIcons.stop.of(context) : AppIcons.mic.of(context),
                  dictating ? t('mobile.diktatStop') : t('mobile.diktieren'),
                  onDictate,
                  on: dictating,
                ),
              ];
        return DecoratedBox(
          decoration: BoxDecoration(
            color: c.surface2,
            border: Border(top: BorderSide(color: c.hairline)),
          ),
          child: SafeArea(
            top: false,
            child: SizedBox(
              height: 52,
              child: ListView(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                children: [
                  for (final b in items) Padding(padding: const EdgeInsets.symmetric(horizontal: 2), child: b),
                ],
              ),
            ),
          ),
        );
      },
    );
  }

  Future<void> _pickStyle(BuildContext context, BlockEditorState state) async {
    final t = Strings.of(context).t;
    final type = context.type;
    final choice = await showModalBottomSheet<BlockKind>(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final (kind, label, style) in [
              (BlockKind.heading1, t('mobile.stilTitel'), type.headlineSmall),
              (BlockKind.heading2, t('mobile.stilUeberschrift'), type.titleLarge),
              (BlockKind.heading3, t('mobile.stilUnterueberschrift'), type.titleMedium),
              (BlockKind.paragraph, t('mobile.stilText'), type.bodyLarge),
              (BlockKind.numbered, t('mobile.stilNummeriert'), type.bodyLarge),
              (BlockKind.quote, t('mobile.stilZitat'), type.bodyLarge?.copyWith(fontStyle: FontStyle.italic)),
            ])
              ListTile(
                title: Text(label, style: style),
                selected: state.focusedKind == kind,
                onTap: () => Navigator.pop(context, kind),
              ),
          ],
        ),
      ),
    );
    if (choice != null) state.setKind(choice);
  }
}
