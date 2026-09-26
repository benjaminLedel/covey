import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/rich/editor.dart';
import 'package:covey_mobile/screens/notes.dart';
import 'package:intl/date_symbol_data_local.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

const z = '​';

final _api = CoveyApi(Uri.parse('https://c.example'), 'k', client: MockClient((_) async => http.Response('', 404)));

Future<List<String>> _editor(WidgetTester tester, String initial) async {
  final out = <String>[];
  final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
  await tester.pumpWidget(
    MaterialApp(
      theme: coveyTheme(Brightness.light).copyWith(platform: TargetPlatform.macOS),
      builder: (context, child) => StringsScope(strings: strings!, child: child!),
      home: Scaffold(
        body: SingleChildScrollView(
          padding: const EdgeInsets.symmetric(horizontal: 20),
          child: BlockEditor(api: _api, initial: initial, onChanged: out.add),
        ),
      ),
    ),
  );
  return out;
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 4; i++) {
    await tester.pump(const Duration(milliseconds: 30));
  }
}

void main() {
  testWidgets(
    'on a Mac, clicking "Tabelle" in the block menu makes a table',
    (tester) async {
      final out = await _editor(tester, 'Oben\n');
      await tester.tap(find.byType(TextField).last, kind: PointerDeviceKind.mouse);
      await tester.enterText(find.byType(TextField).last, '$z/tab');
      await _settle(tester);
      await tester.tap(find.text('Tabelle'), kind: PointerDeviceKind.mouse);
      await _settle(tester);
      expect(out.last, startsWith('Oben\n|  |  |'));
    },
    variant: TargetPlatformVariant.only(TargetPlatform.macOS),
  );

  testWidgets('on a Mac, Enter in the block menu makes a table', (tester) async {
    final out = await _editor(tester, 'Oben\n');
    await tester.tap(find.byType(TextField).last, kind: PointerDeviceKind.mouse);
    await tester.enterText(find.byType(TextField).last, '$z/tab');
    await _settle(tester);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await _settle(tester);
    expect(out.last, startsWith('Oben\n|  |  |'));
  }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

  testWidgets(
    'the handle stays while the mouse moves from the block to it',
    (tester) async {
      await _editor(tester, 'Erste\nZweite');
      final line = tester.getTopLeft(find.byType(TextField).last);
      final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
      await mouse.addPointer(location: line + const Offset(30, 10));
      await tester.pump();
      await mouse.moveTo(line + const Offset(-12, 10));
      await _settle(tester);
      final handle = find.byIcon(Icons.drag_indicator_rounded);
      final shown = tester.widgetList<AnimatedOpacity>(
        find.ancestor(of: handle, matching: find.byType(AnimatedOpacity)),
      );
      expect(shown.any((o) => o.opacity == 1), isTrue, reason: 'the handle of the line under the mouse is shown');
      await mouse.removePointer();
    },
    variant: TargetPlatformVariant.only(TargetPlatform.macOS),
  );

  testWidgets(
    'on a Mac, in the note page, "/tab" and a click make a table',
    (tester) async {
      String? sent;
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async {
          if (req.method == 'PATCH') sent = req.body;
          return http.Response(
            '{"id":"n1","kind":"text","title":"","body":"x","summary":"","duration_seconds":0,"created_at":"2026-09-26T10:00:00Z","tags":[]}',
            200,
          );
        }),
      );
      await tester.binding.setSurfaceSize(const Size(1200, 900));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final strings = await tester.runAsync(() async {
        await initializeDateFormatting();
        return Strings.load(const Locale('de'));
      });
      final note = Note(
        id: 'n1',
        kind: 'text',
        title: 'T',
        body: 'Oben',
        summary: '',
        durationSeconds: 0,
        createdAt: DateTime(2026, 9, 26),
      );
      await tester.pumpWidget(
        MaterialApp(
          theme: coveyTheme(Brightness.light),
          builder: (context, child) => StringsScope(strings: strings!, child: child!),
          home: NotePage(api: api, note: note, saveDelay: const Duration(milliseconds: 50)),
        ),
      );
      await _settle(tester);
      final field = find.byType(TextField).last;
      await tester.tap(field, kind: PointerDeviceKind.mouse);
      await _settle(tester);
      await tester.enterText(field, '${z}Oben\n');
      await _settle(tester);
      await tester.enterText(find.byType(TextField).last, '$z/tab');
      await _settle(tester);
      expect(find.text('Tabelle'), findsOneWidget, reason: 'the menu is open');
      await tester.tap(find.text('Tabelle'), kind: PointerDeviceKind.mouse);
      await _settle(tester);
      await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 100)));
      await _settle(tester);
      expect(find.byType(Table), findsOneWidget, reason: 'the table is on the page');
      expect(sent, contains('| --- |'));
    },
    variant: TargetPlatformVariant.only(TargetPlatform.macOS),
  );

  testWidgets(
    '"/" in the middle of a line opens the menu; the block comes after the line',
    (tester) async {
      final out = await _editor(tester, 'Termine');
      final field = find.byType(TextField).first;
      await tester.tap(field, kind: PointerDeviceKind.mouse);
      await tester.enterText(field, '${z}Termine /tab');
      await _settle(tester);
      await tester.tap(find.text('Tabelle'), kind: PointerDeviceKind.mouse);
      await _settle(tester);
      expect(out.last, startsWith('Termine\n|  |  |'), reason: 'the line keeps its text, the table follows');

      await tester.enterText(find.byType(TextField).first, '${z}Einkauf /todo');
      await _settle(tester);
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await _settle(tester);
      expect(out.last, startsWith('Einkauf\n- [ ] '));
    },
    variant: TargetPlatformVariant.only(TargetPlatform.macOS),
  );

  testWidgets('a "/" inside a word is just a character', (tester) async {
    await _editor(tester, '');
    await tester.enterText(find.byType(TextField).first, '${z}und/oder');
    await _settle(tester);
    expect(find.text('Tabelle'), findsNothing);
    expect(find.text('Toggle'), findsNothing);
  }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));
}
