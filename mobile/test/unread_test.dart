import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

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
  setUpAll(() => initializeDateFormatting());

  testWidgets('the list shows unread answers, and reading drops them (#378)', (tester) async {
    tester.view.physicalSize = const Size(1400, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    var read = false;
    String? readAt;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/auth/me')) return _json({'Email': 'a@b.c', 'DisplayName': 'Ada', 'Role': 'org_admin'});
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/departments')) {
          return _json([
            {'id': 'd1', 'name': 'Buchhaltung'},
          ]);
        }
        if (p.endsWith('/agents')) {
          return _json([
            {
              'id': _agent,
              'slug': 'bea',
              'display_name': 'Bea',
              'hired_at': '2026-09-01T00:00:00Z',
              'status': 'sleeping',
              'department_id': 'd1',
            },
            {
              'id': 'other',
              'slug': 'cid',
              'display_name': 'Cid',
              'hired_at': '2026-09-01T00:00:00Z',
              'status': 'sleeping',
              'job_title': 'Support',
              'department_id': 'd1',
            },
          ]);
        }
        if (p.endsWith('/me/threads')) {
          return _json({
            'threads': [
              {
                'agent_id': _agent,
                'unread': read ? 0 : 2,
                'last_at': '2026-09-26T10:15:00Z',
                'last_text': 'Die Rechnung ist geprüft.',
                'last_kind': 'result',
              },
            ],
          });
        }
        if (p.endsWith('/thread/read')) {
          read = true;
          readAt = (jsonDecode(req.body) as Map<String, dynamic>)['at'] as String;
          return http.Response('', 204);
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
                'at': '2026-09-26T10:15:00Z',
              },
            ],
          });
        }
        return http.Response('', 404);
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);

    expect(find.text('2'), findsNWidgets(2), reason: 'the count in the row and on the rail (#393)');
    expect(find.text('Die Rechnung ist geprüft.'), findsOneWidget, reason: 'the newest line as the subtitle');
    final semantics = tester.ensureSemantics();
    expect(find.bySemanticsLabel(RegExp('^2 ungelesene Nachrichten')), findsOneWidget);
    semantics.dispose();
    expect(find.text('Support'), findsOneWidget, reason: 'a colleague with nothing unread keeps the plain row');
    // Above the departments, in a section of its own (#383).
    expect(
      tester.getTopLeft(find.text('Ungelesen')).dy,
      lessThan(tester.getTopLeft(find.text('Buchhaltung')).dy),
      reason: 'unread stands above the departments',
    );
    expect(tester.getTopLeft(find.text('Bea')).dy, lessThan(tester.getTopLeft(find.text('Buchhaltung')).dy));

    await tester.tap(find.text('Bea'));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await _settle(tester);
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await _settle(tester);
    expect(readAt, '2026-09-26T10:15:00.000Z', reason: 'read up to the newest entry shown');
    expect(find.text('2'), findsNothing, reason: 'the badge is gone once the thread was read');
    expect(find.text('Ungelesen'), findsNothing, reason: 'once read, Bea goes back to the department');
    await tester.pumpWidget(const SizedBox());
  });
}
