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
    final cells = find.byType(TextField);
    expect(cells, findsNWidgets(4));
    await tester.enterText(cells.at(3), '3');
    await _settle(tester);
    expect(out.last, '| A | B |\n| --- | --- |\n| 1 | 3 |');

    await tester.tap(cells.at(3));
    await _settle(tester);
    key.currentState!.tableAdd(row: true);
    await _settle(tester);
    expect(out.last, '| A | B |\n| --- | --- |\n| 1 | 3 |\n|  |  |');
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
}
