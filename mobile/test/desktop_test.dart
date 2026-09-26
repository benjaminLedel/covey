import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/screens/connect.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/theme.dart';
import 'package:covey_mobile/ui.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

Future<Widget> _app(Widget home) async {
  final strings = await Strings.load(const Locale('de'));
  return MaterialApp(
    theme: coveyTheme(Brightness.light),
    builder: (context, child) => StringsScope(strings: strings, child: child!),
    home: home,
  );
}

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 5; i++) {
    await tester.pump(const Duration(milliseconds: 50));
  }
}

const _agent = '11111111-2222-3333-4444-555555555555';

void main() {
  testWidgets('on a desktop the connect screen offers no camera (#334)', (tester) async {
    await tester.pumpWidget(
      await tester.runAsync(() => _app(ConnectScreen(desktop: true, onConnected: (_, _) async {}))) as Widget,
    );
    expect(find.text('QR-Code scannen'), findsNothing);
    expect(find.textContaining('„In der App öffnen“'), findsOneWidget);
    expect(find.text('app.covey.work'), findsOneWidget, reason: 'the address fields stand open on a desktop');
  });

  testWidgets('a wide window shows the list and the thread side by side (#334)', (tester) async {
    tester.view.physicalSize = const Size(1400, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/auth/me')) return _json({'Email': 'a@b.c', 'DisplayName': 'Ada', 'Role': 'org_admin'});
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/agents')) {
          return _json([
            {'id': _agent, 'slug': 'bea', 'display_name': 'Bea', 'status': 'sleeping'},
          ]);
        }
        if (p.endsWith('/thread')) {
          return _json({
            'entries': [
              {
                'kind': 'result',
                'id': 't1',
                'author': 'agent',
                'text': 'Die Rechnung ist geprüft.',
                'task_state': 'done',
              },
            ],
          });
        }
        return http.Response('', 404);
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);

    // A sidebar instead of the floating capsule.
    expect(find.byType(SpaceCapsule), findsNothing);
    // A rail of icons with their names as tooltips (#393).
    expect(find.byTooltip('Notizen'), findsOneWidget);
    expect(find.textContaining('Wählen Sie links'), findsOneWidget);

    await tester.tap(find.text('Bea'));
    await _settle(tester);

    // Beside the list, not on a pushed route: the list is still there.
    expect(find.text('Die Rechnung ist geprüft.'), findsOneWidget);
    expect(find.text('Bea'), findsWidgets);
    expect(find.textContaining('Wählen Sie links'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });
}
