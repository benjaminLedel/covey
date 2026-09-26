import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/profile.dart';
import 'package:covey_mobile/screens/settings.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

// Several organisations (#417): a key belongs to one seat, so the app keeps a
// connection per organisation and switches between them.

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

void main() {
  test('a connection saved before #417 is taken over', () async {
    final store = MemoryProfileStore({'instance': 'https://c.example', 'api_key': 'covey_alt'});
    final all = await store.all();
    expect(all.single.key, 'covey_alt');
    expect((await store.read())?.key, 'covey_alt');
    // Written anew, the old single entries go.
    await store.write('https://c.example', 'covey_alt', orgId: 'org-a', label: 'Acme');
    expect(store.values.containsKey('api_key'), isFalse);
    expect((await store.all()).single.orgId, 'org-a', reason: 'the old connection learnt its organisation');
  });

  test('a second organisation is added, the same one replaced, and leaving one switches to the next', () async {
    final store = MemoryProfileStore();
    await store.write('https://c.example', 'k-a', orgId: 'org-a', label: 'Acme');
    await store.write('https://c.example', 'k-b', orgId: 'org-b', label: 'Globex');
    expect((await store.all()).map((p) => p.label), ['Acme', 'Globex']);
    expect((await store.active())?.label, 'Globex', reason: 'the one just paired is active');

    await store.write('https://c.example', 'k-a2', orgId: 'org-a', label: 'Acme');
    expect((await store.all()).length, 2, reason: 'a new pairing to the same organisation replaces its key');
    expect((await store.active())?.key, 'k-a2');

    await store.activate((await store.all()).last);
    expect((await store.active())?.label, 'Globex');
    await store.clear();
    expect((await store.all()).single.label, 'Acme', reason: 'only the active connection goes');
    expect((await store.active())?.label, 'Acme');
    await store.clear();
    expect(await store.all(), isEmpty);
    expect(await store.read(), isNull);
  });

  testWidgets('the settings list the organisations and switch between them', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k-a',
      client: MockClient((req) async {
        if (req.url.path.endsWith('/auth/memberships')) {
          return _json([
            {'org_id': 'org-a', 'org_name': 'Acme', 'role': 'org_admin'},
            {'org_id': 'org-b', 'org_name': 'Globex', 'role': 'agent_owner'},
            {'org_id': 'org-c', 'org_name': 'Initech', 'role': 'auditor'},
          ]);
        }
        return _json({});
      }),
    );
    const a = Profile(instance: 'https://c.example', key: 'k-a', orgId: 'org-a', label: 'Acme');
    const b = Profile(instance: 'https://c.example', key: 'k-b', orgId: 'org-b', label: 'Globex');
    Profile? gewechselt;
    var hinzu = 0;
    final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
    await tester.pumpWidget(
      MaterialApp(
        theme: coveyTheme(Brightness.light),
        builder: (context, child) => StringsScope(strings: strings!, child: child!),
        home: SettingsScreen(
          api: api,
          me: Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true, orgId: 'org-a'),
          onDisconnect: () {},
          profiles: const [a, b],
          active: a,
          onSwitch: (p) => gewechselt = p,
          onAddOrganisation: () => hinzu++,
        ),
      ),
    );
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 50));
    }

    expect(find.text('Acme'), findsOneWidget);
    expect(find.text('Globex'), findsOneWidget);
    // Initech is a seat without a connection here: named, with where to pair it.
    expect(find.text('Initech'), findsOneWidget);
    expect(find.textContaining('Im Browser in dieser Organisation'), findsOneWidget);
    await tester.tap(find.text('Globex'));
    expect(gewechselt?.label, 'Globex');
    await tester.ensureVisible(find.text('Weitere Organisation koppeln'));
    await tester.pump();
    await tester.tap(find.text('Weitere Organisation koppeln'));
    expect(hinzu, 1);
  });
}
