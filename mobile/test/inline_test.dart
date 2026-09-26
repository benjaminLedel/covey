import 'package:covey_mobile/rich/inline.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The spans a controller draws for its text, flattened.
List<TextSpan> spans(WidgetTester tester, MarkdownController c) {
  final root = c.buildTextSpan(
    context: tester.element(find.byType(Placeholder)),
    style: const TextStyle(fontSize: 17),
    withComposing: false,
  );
  return [for (final s in root.children!) s as TextSpan];
}

void main() {
  testWidgets('markers fold away where the caret is not, and show where it is (#365)', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: Placeholder()));
    final c = MarkdownController(text: '**10:46** Mail an Ada');

    // Nothing selected — the note is being read: the asterisks take no room.
    var s = spans(tester, c);
    expect(s.first.text, '**');
    expect(s.first.style!.fontSize, lessThan(1));
    expect(s[1].text, '10:46');
    expect(s[1].style!.fontWeight, FontWeight.w700);

    // The caret inside the bold word: the asterisks are there to edit.
    c.selection = const TextSelection.collapsed(offset: 4);
    s = spans(tester, c);
    expect(s.first.style!.fontSize, 17);

    // The caret elsewhere in the line: folded again.
    c.selection = const TextSelection.collapsed(offset: 15);
    s = spans(tester, c);
    expect(s.first.style!.fontSize, lessThan(1));
  });
}
