import 'package:flutter/material.dart';

import 'icons.dart';
import 'theme.dart';

/// The text of a thread entry (#397): the Markdown agents answer in, drawn
/// rather than shown with its marks. Headings, bullets, numbered and
/// checklist items, quotes and code blocks per line; **bold**, *italic*,
/// `code` and [links](…) inline. Anything else stays text — nothing is
/// guessed at, and no HTML is read.
class ChatText extends StatelessWidget {
  const ChatText(this.markdown, {super.key, required this.color});

  final String markdown;
  final Color color;

  static final _heading = RegExp(r'^(#{1,6})\s+(.*)$');
  static final _check = RegExp(r'^\s*[-*]\s+\[( |x|X)\]\s+(.*)$');
  static final _bullet = RegExp(r'^\s*[-*•]\s+(.*)$');
  static final _numbered = RegExp(r'^\s*(\d+)[.)]\s+(.*)$');
  static final _quote = RegExp(r'^>\s?(.*)$');
  static final _inline = RegExp(
    r'(\*\*[^*\n]+\*\*|__[^_\n]+__)|(`[^`\n]+`)|(\[[^\]\n]+\]\([^)\s]+\))|((?<!\w)\*[^*\s][^*\n]*\*|(?<!\w)_[^_\n]+_(?!\w))',
  );

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final body = context.type.bodyLarge!.copyWith(color: color);
    final blocks = <Widget>[];
    final lines = markdown.replaceAll('\r\n', '\n').split('\n');
    var gap = false;
    for (var i = 0; i < lines.length; i++) {
      final line = lines[i].trimRight();
      if (line.trim().isEmpty) {
        gap = blocks.isNotEmpty;
        continue;
      }
      if (gap) blocks.add(const SizedBox(height: 8));
      gap = false;
      // A fenced code block, kept as it is.
      if (line.trimLeft().startsWith('```')) {
        final code = <String>[];
        while (++i < lines.length && !lines[i].trimLeft().startsWith('```')) {
          code.add(lines[i]);
        }
        blocks.add(
          Container(
            width: double.infinity,
            margin: const EdgeInsets.symmetric(vertical: 4),
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(color: color.withValues(alpha: 0.07), borderRadius: BorderRadius.circular(8)),
            child: Text(code.join('\n'), style: body.copyWith(fontFamily: 'Menlo', fontSize: 13, height: 1.4)),
          ),
        );
        continue;
      }
      RegExpMatch? m;
      if ((m = _heading.firstMatch(line)) != null) {
        blocks.add(
          Padding(
            padding: EdgeInsets.only(top: blocks.isEmpty ? 0 : 6, bottom: 2),
            child: Text.rich(_spans(m![2]!, body.copyWith(fontWeight: FontWeight.w700), c)),
          ),
        );
      } else if ((m = _check.firstMatch(line)) != null) {
        final done = m![1] != ' ';
        blocks.add(
          _item(
            Icon(done ? AppIcons.checkDone.of(context) : AppIcons.checkOpen.of(context), size: 18, color: color),
            _spans(m[2]!, done ? body.copyWith(decoration: TextDecoration.lineThrough) : body, c),
          ),
        );
      } else if ((m = _bullet.firstMatch(line)) != null) {
        blocks.add(_item(Text('•', style: body), _spans(m![1]!, body, c)));
      } else if ((m = _numbered.firstMatch(line)) != null) {
        blocks.add(
          _item(
            Text('${m![1]}.', style: body.copyWith(fontFeatures: const [FontFeature.tabularFigures()])),
            _spans(m[2]!, body, c),
          ),
        );
      } else if ((m = _quote.firstMatch(line)) != null) {
        blocks.add(
          Container(
            padding: const EdgeInsets.only(left: 10),
            margin: const EdgeInsets.symmetric(vertical: 2),
            decoration: BoxDecoration(
              border: Border(left: BorderSide(color: color.withValues(alpha: 0.4), width: 2.5)),
            ),
            child: Text.rich(_spans(m![1]!, body.copyWith(fontStyle: FontStyle.italic), c)),
          ),
        );
      } else {
        blocks.add(Text.rich(_spans(line, body, c)));
      }
    }
    return Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: blocks);
  }

  Widget _item(Widget mark, TextSpan text) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 1),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 22,
          child: Align(alignment: Alignment.topLeft, child: mark),
        ),
        Expanded(child: Text.rich(text)),
      ],
    ),
  );

  TextSpan _spans(String text, TextStyle base, CoveyColors c) {
    final spans = <InlineSpan>[];
    var last = 0;
    for (final m in _inline.allMatches(text)) {
      if (m.start > last) spans.add(TextSpan(text: text.substring(last, m.start)));
      final s = m[0]!;
      if (m[1] != null) {
        spans.add(
          TextSpan(
            text: s.substring(2, s.length - 2),
            style: const TextStyle(fontWeight: FontWeight.w700),
          ),
        );
      } else if (m[2] != null) {
        spans.add(
          TextSpan(
            text: s.substring(1, s.length - 1),
            style: TextStyle(
              fontFamily: 'Menlo',
              fontSize: (base.fontSize ?? 16) * 0.88,
              backgroundColor: color.withValues(alpha: 0.08),
            ),
          ),
        );
      } else if (m[3] != null) {
        final label = s.substring(1, s.indexOf(']('));
        spans.add(
          TextSpan(
            text: label,
            style: const TextStyle(decoration: TextDecoration.underline),
          ),
        );
      } else {
        spans.add(
          TextSpan(
            text: s.substring(1, s.length - 1),
            style: const TextStyle(fontStyle: FontStyle.italic),
          ),
        );
      }
      last = m.end;
    }
    if (last < text.length) spans.add(TextSpan(text: text.substring(last)));
    return TextSpan(style: base, children: spans);
  }
}

/// A message of a few emoji and nothing else, shown large (#397).
bool emojiOnly(String text) {
  final t = text.trim();
  if (t.isEmpty || t.runes.length > 12) return false;
  // The analyser does not know these Unicode properties; the VM does
  // (test/chat_text_test.dart), as in rich/blocks.dart.
  return RegExp(
        // ignore: valid_regexps
        r'^(?:\p{Extended_Pictographic}|\p{Emoji_Modifier}|\p{Regional_Indicator}|‍|️|\s)+$',
        unicode: true,
      ).hasMatch(t) &&
      // ignore: valid_regexps
      RegExp(r'\p{Extended_Pictographic}|\p{Regional_Indicator}', unicode: true).hasMatch(t);
}
