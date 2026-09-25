import 'package:flutter/material.dart';

/// A block's text with its inline Markdown shown as what it means: **bold**
/// bold, _italic_ italic, [links](…) in the accent — the markers stay in the
/// text, drawn faint, so the cursor, selection and what is stored are the
/// same string. Hiding them would make the caret jump over characters the
/// person cannot see.
class MarkdownController extends TextEditingController {
  MarkdownController({super.text});

  Color markerColor = const Color(0x66000000);
  Color linkColor = const Color(0xFF8F3F18);

  static final _inline = RegExp(r'(\*\*[^*\n]+\*\*)|(\*[^*\n]+\*|_[^_\n]+_)|(\[[^\]\n]+\]\([^)\n]+\))');

  @override
  TextSpan buildTextSpan({required BuildContext context, TextStyle? style, required bool withComposing}) {
    final base = style ?? const TextStyle();
    final spans = <InlineSpan>[];
    var last = 0;
    final marker = base.copyWith(color: markerColor, fontWeight: FontWeight.w400, fontStyle: FontStyle.normal);
    for (final m in _inline.allMatches(text)) {
      if (m.start > last) spans.add(TextSpan(text: text.substring(last, m.start), style: base));
      final s = m[0]!;
      if (m[1] != null) {
        spans.add(TextSpan(text: '**', style: marker));
        spans.add(
          TextSpan(
            text: s.substring(2, s.length - 2),
            style: base.copyWith(fontWeight: FontWeight.w700),
          ),
        );
        spans.add(TextSpan(text: '**', style: marker));
      } else if (m[2] != null) {
        final mark = s[0];
        spans.add(TextSpan(text: mark, style: marker));
        spans.add(
          TextSpan(
            text: s.substring(1, s.length - 1),
            style: base.copyWith(fontStyle: FontStyle.italic),
          ),
        );
        spans.add(TextSpan(text: mark, style: marker));
      } else {
        final close = s.indexOf('](');
        spans.add(TextSpan(text: '[', style: marker));
        spans.add(
          TextSpan(
            text: s.substring(1, close),
            style: base.copyWith(color: linkColor, decoration: TextDecoration.underline, decorationColor: linkColor),
          ),
        );
        spans.add(TextSpan(text: s.substring(close), style: marker));
      }
      last = m.end;
    }
    if (last < text.length) spans.add(TextSpan(text: text.substring(last), style: base));
    return TextSpan(style: base, children: spans);
  }

  /// Wraps the selection in [mark] — or, with nothing selected, inserts the
  /// pair and puts the caret between them, ready to type.
  void wrapSelection(String mark) {
    final sel = selection;
    if (!sel.isValid) return;
    final before = text.substring(0, sel.start);
    final inner = text.substring(sel.start, sel.end);
    final after = text.substring(sel.end);
    value = TextEditingValue(
      text: '$before$mark$inner$mark$after',
      selection: inner.isEmpty
          ? TextSelection.collapsed(offset: sel.start + mark.length)
          : TextSelection(baseOffset: sel.start + mark.length, extentOffset: sel.end + mark.length),
    );
  }
}
