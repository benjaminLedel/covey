import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/rich/editor.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

const z = '​';

final _api = CoveyApi(Uri.parse('https://c.example'), 'k', client: MockClient((_) async => http.Response('', 404)));

Future<(GlobalKey<BlockEditorState>, List<String>)> _editor(WidgetTester tester, String initial) async {
  final key = GlobalKey<BlockEditorState>();
  final out = <String>[];
  final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
  await tester.pumpWidget(
    MaterialApp(
      theme: coveyTheme(Brightness.light),
      builder: (context, child) => StringsScope(strings: strings!, child: child!),
      home: Scaffold(
        body: SingleChildScrollView(
          child: BlockEditor(key: key, api: _api, initial: initial, onChanged: out.add),
        ),
      ),
    ),
  );
  return (key, out);
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 4; i++) {
    await tester.pump(const Duration(milliseconds: 30));
  }
}

void main() {
  testWidgets('Enter continues a checklist, and in an empty item ends it (#344)', (tester) async {
    final (key, out) = await _editor(tester, '- [ ] Milch');
    await tester.enterText(find.byType(TextField).first, '${z}Milch\n');
    await _settle(tester);
    expect(out.last, '- [ ] Milch\n- [ ] ');

    await tester.enterText(find.byType(TextField).last, '$z\n');
    await _settle(tester);
    expect(out.last, '- [ ] Milch\n', reason: 'the empty item became a plain line');
    expect(key.currentState, isNotNull);
  });

  testWidgets('Backspace at the start lifts a list item, then joins the lines', (tester) async {
    final (_, out) = await _editor(tester, 'Erste\n- Zweite');
    await tester.enterText(find.byType(TextField).last, 'Zweite');
    await _settle(tester);
    expect(out.last, 'Erste\nZweite', reason: 'first the bullet goes');

    await tester.enterText(find.byType(TextField).last, 'Zweite');
    await _settle(tester);
    expect(out.last, 'ErsteZweite', reason: 'then the line joins the one before');
  });

  testWidgets('the circle ticks a checklist item', (tester) async {
    final (_, out) = await _editor(tester, '- [ ] Angebot schicken');
    await tester.tap(find.bySemanticsLabel('Checkliste'));
    await _settle(tester);
    expect(out.last, '- [x] Angebot schicken');
  });

  testWidgets('a table is edited in its cells and grows by a row', (tester) async {
    final (key, out) = await _editor(tester, '| A | B |\n| --- | --- |\n| 1 | 2 |');
    final cells = find.descendant(of: find.byType(Table), matching: find.byType(TextField));
    expect(cells, findsNWidgets(4));
    expect(find.byType(TextField), findsNWidgets(5), reason: 'a line after the table, to go on writing');
    await tester.enterText(cells.at(3), '3');
    await _settle(tester);
    expect(out.last, '| A | B |\n| --- | --- |\n| 1 | 3 |\n\n', reason: 'the blank line after the table, then the line');

    await tester.tap(cells.at(3));
    await _settle(tester);
    key.currentState!.tableAdd(row: true);
    await _settle(tester);
    expect(out.last, '| A | B |\n| --- | --- |\n| 1 | 3 |\n|  |  |\n\n');
  });

  testWidgets('bold wraps the selection in Markdown', (tester) async {
    final (key, out) = await _editor(tester, 'wichtig');
    final field = find.byType(TextField).first;
    await tester.tap(field);
    await _settle(tester);
    final c = tester.widget<TextField>(field).controller!;
    c.selection = const TextSelection(baseOffset: 1, extentOffset: 8);
    key.currentState!.wrap('**');
    await _settle(tester);
    expect(out.last, '**wichtig**');
  });

  testWidgets('"/" opens the block menu, typing filters it, a tap turns the block (#371)', (tester) async {
    final (_, out) = await _editor(tester, 'Einkauf');
    final field = find.byType(TextField).first;
    await tester.enterText(field, '$z/');
    await _settle(tester);
    expect(find.text('Toggle'), findsOneWidget, reason: 'the whole menu under the block');
    expect(find.text('Überschrift 1'), findsOneWidget);

    await tester.enterText(field, '$z/tog');
    await _settle(tester);
    expect(find.text('Überschrift 1'), findsNothing, reason: 'filtered by what is typed');
    await tester.tap(find.text('Toggle'));
    await _settle(tester);
    expect(
      out.last,
      '<details>\n<summary></summary>\n\n</details>',
      reason: 'the "/tog" is gone, the block is a toggle',
    );
    expect(find.text('Toggle'), findsNothing, reason: 'the menu closed');
  });

  testWidgets('a space closes the block menu, and English names find blocks in German (#371)', (tester) async {
    await _editor(tester, '');
    final field = find.byType(TextField).first;
    await tester.enterText(field, '$z/h2');
    await _settle(tester);
    expect(find.text('Überschrift 2'), findsOneWidget);
    await tester.enterText(field, '$z/h2 x');
    await _settle(tester);
    expect(find.text('Überschrift 2'), findsNothing);
  });

  testWidgets('"/callout" makes a callout, "/table" puts a table in place of the line (#371)', (tester) async {
    final (_, out) = await _editor(tester, '');
    await tester.enterText(find.byType(TextField).first, '$z/callout');
    await _settle(tester);
    await tester.tap(find.text('Callout'));
    await _settle(tester);
    expect(out.last, '> 💡 ');

    final (_, out2) = await _editor(tester, 'Oben\n');
    await tester.enterText(find.byType(TextField).last, '$z/tabelle');
    await _settle(tester);
    await tester.tap(find.text('Tabelle'));
    await _settle(tester);
    expect(out2.last, startsWith('Oben\n|  |  |\n| --- | --- |'));
  });

  testWidgets('a toggle folds its content away and opens it again (#371)', (tester) async {
    final (_, out) = await _editor(tester, '<details>\n<summary>Mehr</summary>\n\nDetails\n\n</details>');
    expect(find.text('Details'), findsNothing, reason: 'closed on opening the note');
    await tester.tap(find.bySemanticsLabel('Toggle'));
    await _settle(tester);
    expect(find.text('Details'), findsOneWidget);
    await tester.enterText(find.byType(TextField).last, 'Details und mehr');
    await _settle(tester);
    expect(out.last, '<details>\n<summary>Mehr</summary>\n\nDetails und mehr\n\n</details>');
  });
}
