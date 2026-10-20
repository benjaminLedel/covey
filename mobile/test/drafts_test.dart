import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/drafts.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

// A reply typed and not sent outlives the screen it was typed in (#621).

Future<Widget> _app(Widget home) async {
  final strings = await Strings.load(const Locale('de'));
  return MaterialApp(
    theme: coveyTheme(Brightness.light),
    builder: (context, child) => StringsScope(strings: strings, child: child!),
    home: home,
  );
}

http.Response _json(Object body, {int status = 200}) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 8; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

final _me = Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true, id: 'me');

void main() {
  setUpAll(() => initializeDateFormatting());
  setUp(Drafts.instance.clearAll);

  test('a draft is kept per thread, and a blank one is none', () {
    final d = Drafts.instance;
    d.write(Drafts.agent('a1'), 'Hallo');
    d.write(Drafts.conversation('a1'), 'Mittag?');
    expect(d.read(Drafts.agent('a1')), 'Hallo');
    expect(d.read(Drafts.conversation('a1')), 'Mittag?', reason: 'an agent and a conversation never share a key');
    d.write(Drafts.agent('a1'), '   ');
    expect(d.read(Drafts.agent('a1')), '');
    d.clear(Drafts.conversation('a1'));
    expect(d.read(Drafts.conversation('a1')), '');
  });

  testWidgets('the text typed stays when the thread is left and opened again, and goes once sent', (tester) async {
    final sent = <String>[];
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.url.path.endsWith('/messages')) {
          sent.add(req.body);
          return _json({'message': {}, 'pending': true}, status: 202);
        }
        return _json({'entries': [], 'pending': false});
      }),
    );
    Widget thread(String agentId) =>
        ThreadScreen(key: ValueKey('thread:$agentId'), api: api, agentId: agentId, agentName: 'Bea', me: _me);

    await tester.pumpWidget(await tester.runAsync(() => _app(thread('a1'))) as Widget);
    await _settle(tester);
    await tester.enterText(find.byType(TextField), 'Halb fertig');

    // Something else in its place: the screen is disposed.
    await tester.pumpWidget(await tester.runAsync(() => _app(const SizedBox())) as Widget);
    await _settle(tester);
    // Another thread starts empty.
    await tester.pumpWidget(await tester.runAsync(() => _app(thread('a2'))) as Widget);
    await _settle(tester);
    expect(tester.widget<TextField>(find.byType(TextField)).controller?.text, '');

    // Back to the first: the text is there.
    await tester.pumpWidget(await tester.runAsync(() => _app(thread('a1'))) as Widget);
    await _settle(tester);
    expect(tester.widget<TextField>(find.byType(TextField)).controller?.text, 'Halb fertig');

    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await _settle(tester);
    expect(sent, hasLength(1));
    expect(jsonDecode(sent.single), {'text': 'Halb fertig'});
    expect(Drafts.instance.read(Drafts.agent('a1')), '', reason: 'a sent message is no draft');
    await tester.pumpWidget(const SizedBox());
  });
}
