import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/face.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/team_space.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

// #414: drafts are not colleagues yet, and a stopped colleague takes no
// messages — the team list leaves the first out, the thread says so for the
// second instead of offering a composer.

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
  for (var i = 0; i < 6; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

Me _me() => Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true);

void main() {
  testWidgets('a draft is not in the team list, a hired colleague is', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/agents')) {
          return _json([
            {'id': 'a1', 'slug': 'bea', 'display_name': 'Bea', 'status': 'idle', 'hired_at': '2026-09-01T00:00:00Z'},
            {'id': 'a2', 'slug': 'entwurf', 'display_name': 'Entwurf Egon', 'status': 'idle'},
          ]);
        }
        return http.Response('', 404);
      }),
    );
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              Scaffold(
                body: TeamSpace(api: api, me: _me(), onOpen: (_, _, _, _) {}),
              ),
            ),
          )
          as Widget,
    );
    await _settle(tester);
    expect(find.text('Bea'), findsOneWidget);
    expect(find.text('Entwurf Egon'), findsNothing);
  });

  testWidgets('a stopped colleague shows why there is no composer', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async => _json({'pending': false, 'entries': []})),
    );
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(ThreadScreen(api: api, agentId: 'a1', agentName: 'Bea', faceState: FaceState.killed, me: _me())),
          )
          as Widget,
    );
    await _settle(tester);
    expect(find.textContaining('Bea ist gestoppt'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });
}
