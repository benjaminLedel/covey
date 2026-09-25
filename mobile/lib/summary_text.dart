import 'package:flutter/material.dart';

import 'icons.dart';
import 'theme.dart';

/// A summary as the model writes it (internal/notes): Markdown with headings,
/// lists and checklists. Only that much is rendered — headings, bullets,
/// "- [ ]" items and plain paragraphs — because that is all the instruction
/// asks for; anything else stays text rather than being guessed at.
class SummaryText extends StatelessWidget {
  const SummaryText(this.markdown, {super.key});

  final String markdown;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final blocks = <Widget>[];
    for (final raw in markdown.split('\n')) {
      final line = raw.trimRight();
      if (line.trim().isEmpty) {
        if (blocks.isNotEmpty) blocks.add(const SizedBox(height: 8));
        continue;
      }
      final heading = RegExp(r'^#{1,6}\s+(.*)$').firstMatch(line);
      final check = RegExp(r'^\s*[-*]\s+\[( |x|X)\]\s+(.*)$').firstMatch(line);
      final bullet = RegExp(r'^\s*[-*]\s+(.*)$').firstMatch(line);
      if (heading != null) {
        if (blocks.isNotEmpty) blocks.add(const SizedBox(height: 6));
        blocks.add(Text(_plain(heading[1]!), style: context.type.titleMedium));
        blocks.add(const SizedBox(height: 6));
      } else if (check != null) {
        final done = check[1] != ' ';
        blocks.add(
          _Item(
            mark: Icon(
              done ? AppIcons.checkDone.of(context) : AppIcons.checkOpen.of(context),
              size: 20,
              color: done ? c.textSuccess : c.textMuted,
            ),
            text: _plain(check[2]!),
          ),
        );
      } else if (bullet != null) {
        blocks.add(
          _Item(
            mark: Padding(
              padding: const EdgeInsets.only(top: 9, left: 7, right: 7),
              child: Container(
                width: 6,
                height: 6,
                decoration: BoxDecoration(color: c.textMuted, shape: BoxShape.circle),
              ),
            ),
            text: _plain(bullet[1]!),
          ),
        );
      } else {
        blocks.add(Text(_plain(line), style: context.type.bodyLarge));
      }
    }
    return SelectionArea(
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: blocks),
    );
  }

  /// Emphasis markers carry nothing on a phone that the sentence does not.
  static String _plain(String s) => s.replaceAll(RegExp(r'\*\*|__|`'), '');
}

class _Item extends StatelessWidget {
  const _Item({required this.mark, required this.text});

  final Widget mark;
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 3),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 26,
          child: Align(alignment: Alignment.topLeft, child: mark),
        ),
        const SizedBox(width: 6),
        Expanded(child: Text(text, style: context.type.bodyLarge)),
      ],
    ),
  );
}
