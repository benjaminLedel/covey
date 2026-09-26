import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
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

http.Response _json(Object body, [int status = 200]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 6; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

const _bea = '11111111-2222-3333-4444-555555555555';

void main() {
  testWidgets('a seat that may only read sees the team and opens no conversation (#339)', (tester) async {
    var threadAsked = false;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/auth/me')) {
          return _json({
            'Email': 'a@b.c',
            'DisplayName': 'Aud',
            'Role': 'auditor',
            'TeamSurface': true,
            'CanWrite': false,
          });
        }
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/agents')) {
          return _json([
            {
              'id': _bea,
              'slug': 'bea',
              'display_name': 'Bea',
              'hired_at': '2026-09-01T00:00:00Z',
              'status': 'sleeping',
            },
          ]);
        }
        if (p.endsWith('/thread')) threadAsked = true;
        return _json({'notes': [], 'summarize': false});
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);

    expect(find.text('Bea'), findsOneWidget, reason: 'the colleague is shown');
    await tester.tap(find.text('Bea'));
    await _settle(tester);
    expect(find.byType(ThreadScreen), findsNothing);
    expect(threadAsked, isFalse);
  });

  testWidgets('a 403 because the team surface is off says that, not "your role" (#339)', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.method == 'POST') {
          return _json({'error': 'the team surface is not enabled for this organisation'}, 403);
        }
        return _json({'entries': [], 'pending': false});
      }),
    );
    final me = Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true);
    await tester.pumpWidget(
      await tester.runAsync(() => _app(ThreadScreen(api: api, agentId: _bea, agentName: 'Bea', me: me))) as Widget,
    );
    await _settle(tester);
    await tester.enterText(find.byType(TextField), 'Hallo');
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await _settle(tester);
    expect(find.text('Schreiben ist noch nicht freigeschaltet'), findsOneWidget);
    expect(find.textContaining('Ihre Rolle'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });
}
