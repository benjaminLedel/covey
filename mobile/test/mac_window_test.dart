import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/chrome.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 8; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

void main() {
  testWidgets('on the Mac Enter sends and Shift+Enter breaks the line (#374)', (tester) async {
    final sent = <String>[];
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.url.path.endsWith('/messages') && req.method == 'POST') {
          sent.add((jsonDecode(req.body) as Map<String, Object?>)['text'] as String);
          return _json({'message': {}, 'pending': true});
        }
        return _json({'entries': [], 'pending': false});
      }),
    );
    final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
    await tester.pumpWidget(
      MaterialApp(
        theme: coveyTheme(Brightness.light),
        builder: (context, child) => StringsScope(strings: strings!, child: child!),
        home: ThreadScreen(
          api: api,
          agentId: 'a1',
          agentName: 'Bea',
          me: Me(email: 'a@example.org', displayName: 'Ada', role: 'org_admin', teamSurface: true),
        ),
      ),
    );
    await _settle(tester);

    await tester.tap(find.byType(TextField));
    await tester.enterText(find.byType(TextField), 'erste Zeile');
    await tester.sendKeyDownEvent(LogicalKeyboardKey.shiftLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.shiftLeft);
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await _settle(tester);
    expect(sent, isEmpty, reason: 'Shift+Enter is a line break');

    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await _settle(tester);
    expect(sent, hasLength(1), reason: 'Enter sends');
    expect(sent.single, contains('erste Zeile'));
  });

  testWidgets('the Mac window is drawn at a zoom that ⌘− ⌘+ ⌘0 change (#375)', (tester) async {
    WindowZoom.scale.value = WindowZoom.standard;
    late Size inner;
    late double text;
    await tester.pumpWidget(
      MaterialApp(
        builder: (context, child) => WindowZoom(child: child!),
        home: Builder(
          builder: (context) {
            inner = MediaQuery.sizeOf(context);
            text = MediaQuery.textScalerOf(context).scale(10);
            return const Scaffold(body: TextField(autofocus: true));
          },
        ),
      ),
    );
    await tester.pump();
    final outer = tester.view.physicalSize / tester.view.devicePixelRatio;
    expect(
      inner.width,
      closeTo(outer.width / 0.85, 0.01),
      reason: 'the app lays out on more points than the window has',
    );
    expect(text, closeTo(10 * WindowZoom.text, 0.001), reason: 'text a step smaller than the rest (#376)');
    final bar = MacChrome.height;

    await tester.sendKeyDownEvent(LogicalKeyboardKey.metaLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.minus);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.metaLeft);
    await tester.pump();
    expect(WindowZoom.scale.value, 0.8);
    expect(inner.width, closeTo(outer.width / 0.8, 0.01));
    expect(MacChrome.height, greaterThan(bar), reason: 'the title bar keeps its height on the screen');

    await tester.sendKeyDownEvent(LogicalKeyboardKey.metaLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.equal);
    await tester.sendKeyEvent(LogicalKeyboardKey.equal);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.metaLeft);
    await tester.pump();
    expect(WindowZoom.scale.value, 0.9);

    await tester.sendKeyDownEvent(LogicalKeyboardKey.metaLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.digit0);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.metaLeft);
    await tester.pump();
    expect(WindowZoom.scale.value, WindowZoom.standard);
  });
}
