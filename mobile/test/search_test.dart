import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/theme.dart';
import 'package:covey_mobile/ui.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

Future<Widget> _app(Widget home) async {
  await initializeDateFormatting();
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
  for (var i = 0; i < 8; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

void main() {
  testWidgets('the team search narrows colleagues by name, role and department (#341)', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/auth/me')) {
          return _json({'Email': 'a@b.c', 'DisplayName': 'Ada', 'TeamSurface': true, 'CanWrite': true});
        }
        if (p.endsWith('/inbox')) {
          return _json({'items': [], 'pending': 0});
        }
        if (p.endsWith('/departments')) {
          return _json([
            {'id': 'd1', 'name': 'Qualität'},
          ]);
        }
        if (p.endsWith('/agents')) {
          return _json([
            {
              'id': 'a1',
              'slug': 'bea',
              'display_name': 'Bea',
              'hired_at': '2026-09-01T00:00:00Z',
              'job_title': 'Support',
              'status': 'sleeping',
            },
            {
              'id': 'a2',
              'slug': 'otto',
              'display_name': 'Otto',
              'hired_at': '2026-09-01T00:00:00Z',
              'job_title': 'Lasttests',
              'status': 'sleeping',
              'department_id': 'd1',
            },
          ]);
        }
        return _json({'notes': [], 'summarize': false});
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);
    expect(find.text('Bea'), findsOneWidget);

    await tester.enterText(find.byType(SearchField).first, 'qualit');
    await _settle(tester);
    expect(find.text('Otto'), findsOneWidget, reason: 'found by department');
    expect(find.text('Bea'), findsNothing);

    await tester.enterText(find.byType(SearchField).first, 'niemand');
    await _settle(tester);
    expect(find.text('Nichts gefunden.'), findsOneWidget);
  });

  testWidgets('the notes search asks the instance with q (#341)', (tester) async {
    final asked = <String>[];
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.url.path.endsWith('/auth/me')) {
          return _json({'Email': 'a@b.c', 'DisplayName': 'Ada', 'TeamSurface': false});
        }
        asked.add(req.url.queryParameters['q'] ?? '');
        return _json({'notes': [], 'summarize': false});
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);
    await tester.enterText(find.byType(SearchField).first, 'Globex');
    await tester.pump(const Duration(milliseconds: 400));
    await _settle(tester);
    expect(asked.last, 'Globex');
    expect(asked.where((q) => q.startsWith('G')).length, 1, reason: 'one request for the word, not one per key');
  });
}
