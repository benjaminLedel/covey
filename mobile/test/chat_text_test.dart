import 'package:covey_mobile/chat_text.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('a few emoji and nothing else count as emoji only (#397)', () {
    expect(emojiOnly('👍'), isTrue);
    expect(emojiOnly(' 👍🏽 🎉 '), isTrue);
    expect(emojiOnly('👍 danke'), isFalse);
    expect(emojiOnly('12'), isFalse);
    expect(emojiOnly(''), isFalse);
  });

  testWidgets('agents\' Markdown is drawn, not shown with its marks (#397)', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: coveyTheme(Brightness.light),
        home: const Scaffold(
          body: ChatText('## Stand\n- **Rechnung** geprüft\n- [x] Beleg abgelegt\n> Hinweis', color: Colors.black),
        ),
      ),
    );
    final all = tester.widgetList<RichText>(find.byType(RichText)).map((r) => r.text.toPlainText()).join('\n');
    expect(all, contains('Stand'));
    expect(all, isNot(contains('##')));
    expect(all, contains('Rechnung geprüft'));
    expect(all, isNot(contains('**')));
    expect(all, contains('Beleg abgelegt'));
    expect(all, isNot(contains('[x]')));
    expect(all, contains('Hinweis'));
    expect(all, isNot(contains('>')));
  });
}
