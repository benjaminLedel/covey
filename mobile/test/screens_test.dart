import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/pairing.dart';
import 'package:covey_mobile/screens/connect.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
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

// UTF-8 as the instance sends it; http.Response(String) would encode Latin-1.
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

Me _me({bool team = true}) => Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: team);

final _thread = {
  'pending': false,
  'entries': [
    {
      'kind': 'message',
      'id': 't1',
      'task_id': 't1',
      'task_title': 'Rechnung',
      'author': 'chat:a@b.c',
      'text': 'Bitte die Rechnung prüfen',
      'task_state': 'blocked',
    },
    {
      'kind': 'question',
      'id': 'n1',
      'task_id': 't1',
      'task_title': 'Rechnung',
      'author': 'agent',
      'text': 'Darf ich Globex direkt antworten?',
      'task_state': 'blocked',
    },
  ],
};

void main() {
  testWidgets('the connect screen refuses plain http before asking anything', (tester) async {
    var asked = false;
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              ConnectScreen(
                desktop: false,
                onConnected: (_, _) async {},
                makeApi: (b, k) => CoveyApi(
                  b,
                  k,
                  client: MockClient((_) async {
                    asked = true;
                    return http.Response('', 200);
                  }),
                ),
              ),
            ),
          )
          as Widget,
    );
    // The address and key are the fallback, one tap away.
    await tester.tap(find.text('Stattdessen mit Adresse und API-Schlüssel verbinden'));
    await tester.pump();
    expect(find.text('app.covey.work'), findsOneWidget, reason: 'the hosted instance is the default (#331)');
    await tester.enterText(find.byType(TextField).first, 'http://covey.example.org');
    await tester.scrollUntilVisible(find.text('Verbinden'), 200, scrollable: find.byType(Scrollable).first);
    // Fully into view: a button half under the edge takes no tap.
    await tester.ensureVisible(find.text('Verbinden'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Verbinden'));
    await tester.pump();
    expect(find.text('Nur HTTPS-Adressen.'), findsOneWidget);
    expect(asked, isFalse);
  });

  testWidgets('a reply goes to the question it was chosen at', (tester) async {
    final posts = <String, Object?>{};
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.method == 'POST') {
          posts[req.url.path] = jsonDecode(req.body);
          return _json({'woken': true});
        }
        return _json(_thread);
      }),
    );
    await tester.pumpWidget(
      await tester.runAsync(() => _app(ThreadScreen(api: api, agentId: 'a1', agentName: 'Bea', me: _me()))) as Widget,
    );
    await _settle(tester);

    expect(find.text('Darf ich Globex direkt antworten?'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Antworten'));
    await tester.pump();
    expect(find.text('Antwort auf: Rechnung'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'Ja, bitte.');
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await _settle(tester);
    expect(posts['/api/v1/tasks/t1/reply'], {'text': 'Ja, bitte.'});
    expect(posts.containsKey('/api/v1/agents/a1/messages'), isFalse);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('without the team surface the thread can be read and not written to', (tester) async {
    final api = CoveyApi(Uri.parse('https://c.example'), 'k', client: MockClient((_) async => _json(_thread)));
    await tester.pumpWidget(
      await tester.runAsync(() => _app(ThreadScreen(api: api, agentId: 'a1', agentName: 'Bea', me: _me(team: false))))
          as Widget,
    );
    await _settle(tester);

    expect(find.text('Darf ich Globex direkt antworten?'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Antworten'), findsNothing);
    expect(tester.widget<TextField>(find.byType(TextField)).enabled, isFalse);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a scanned code becomes the connection, without typing anything (#330)', (tester) async {
    Uri? instance;
    String? key;
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              ConnectScreen(
                desktop: false,
                onConnected: (i, k) async {
                  instance = i;
                  key = k;
                },
                scan: (_) async =>
                    PairingCode.parse('covey://pair?instance=https%3A%2F%2Fapp.covey.work&code=coveypair_x'),
                redeem: (code) async => 'covey_paired',
              ),
            ),
          )
          as Widget,
    );
    await tester.tap(find.text('QR-Code scannen'));
    await _settle(tester);
    expect(instance.toString(), 'https://app.covey.work');
    expect(key, 'covey_paired');
  });

  testWidgets('a used code says so and offers nothing else', (tester) async {
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              ConnectScreen(
                desktop: false,
                onConnected: (_, _) async => fail('must not connect'),
                scan: (_) async =>
                    PairingCode.parse('covey://pair?instance=https%3A%2F%2Fapp.covey.work&code=coveypair_x'),
                redeem: (code) async => throw ApiException(401, 'pairing code invalid, used or expired'),
              ),
            ),
          )
          as Widget,
    );
    await tester.tap(find.text('QR-Code scannen'));
    await _settle(tester);
    expect(find.textContaining('ungültig, schon benutzt oder abgelaufen'), findsOneWidget);
  });
}
