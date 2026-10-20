import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/live.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/office_space.dart';
import 'package:covey_mobile/screens/team_space.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

// #619: a colleague created or hired while the app is open turns up without
// a restart. The instance says so with a `roster` event; the team and office
// spaces read agents and departments again on it, and on a resync.

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Map<String, Object?> _agent(String id, String name) => {
  'id': id,
  'slug': id,
  'display_name': name,
  'status': 'idle',
  'hired_at': '2026-09-01T00:00:00Z',
  'department_id': 'd1',
};

Me _me() => Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true);

/// An instance whose roster grows by one colleague after [grow] is called.
/// [slowFirst] holds the first read of the agents back for two seconds.
class _Instance {
  _Instance({this.slowFirst = false});

  final bool slowFirst;
  var grown = false;
  var agentReads = 0;
  var departmentReads = 0;
  var inboxReads = 0;

  void grow() => grown = true;

  CoveyApi api() => CoveyApi(
    Uri.parse('https://c.example'),
    'k',
    client: MockClient((req) async {
      final p = req.url.path;
      if (p.endsWith('/inbox')) {
        inboxReads++;
        return _json({'items': [], 'pending': 0});
      }
      if (p.endsWith('/departments')) {
        departmentReads++;
        return _json([
          {'id': 'd1', 'name': 'Entwicklung', 'color': '#3f8ccb'},
        ]);
      }
      if (p.endsWith('/agents')) {
        agentReads++;
        final mine = grown;
        if (slowFirst && agentReads == 1) await Future<void>.delayed(const Duration(seconds: 2));
        return _json([_agent('a1', 'Bea'), if (mine) _agent('a2', 'Cem')]);
      }
      if (p.endsWith('/org/running')) return _json([]);
      return http.Response('', 404);
    }),
  );
}

Future<void> _pump(WidgetTester tester, Widget body) async {
  final strings = await tester.runAsync(() async {
    await initializeDateFormatting();
    return Strings.load(const Locale('de'));
  });
  await tester.pumpWidget(
    MaterialApp(
      theme: coveyTheme(Brightness.light),
      builder: (context, child) => StringsScope(strings: strings!, child: child!),
      home: Scaffold(body: body),
    ),
  );
  await _settle(tester);
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 6; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

/// Past the live events' settling time.
Future<void> _afterEvent(WidgetTester tester) async {
  await tester.pump(const Duration(seconds: 1));
  await _settle(tester);
}

void main() {
  tearDown(() => LiveEvents.instance.stop());

  testWidgets('the team space shows a new colleague on a roster event', (tester) async {
    final instance = _Instance();
    await _pump(tester, TeamSpace(api: instance.api(), me: _me(), onOpen: (_, _, _, _) {}));
    expect(find.text('Bea'), findsOneWidget);
    expect(find.text('Cem'), findsNothing);

    instance.grow();
    LiveEvents.instance.inject(const LiveEvent('roster', 'a2', {'change': 'hired'}));
    await _afterEvent(tester);
    expect(find.text('Cem'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a message reads who waits again, not the roster', (tester) async {
    final instance = _Instance();
    await _pump(tester, TeamSpace(api: instance.api(), me: _me(), onOpen: (_, _, _, _) {}));
    final agents = instance.agentReads;
    final inbox = instance.inboxReads;
    LiveEvents.instance.inject(const LiveEvent('chat', 'a1', {}));
    await _afterEvent(tester);
    expect(instance.inboxReads, inbox + 1);
    expect(instance.agentReads, agents, reason: 'a message does not change who is on the team');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a resync reads the roster again', (tester) async {
    final instance = _Instance();
    await _pump(tester, TeamSpace(api: instance.api(), me: _me(), onOpen: (_, _, _, _) {}));
    instance.grow();
    LiveEvents.instance.inject(const LiveEvent(LiveEvents.resync, '', {}));
    await _afterEvent(tester);
    expect(find.text('Cem'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('an older read of the roster arriving late does not put it back', (tester) async {
    final instance = _Instance(slowFirst: true);
    await _pump(tester, TeamSpace(api: instance.api(), me: _me(), onOpen: (_, _, _, _) {}));
    // The first read is still out; the roster event's read is fast and sees
    // the new colleague.
    instance.grow();
    LiveEvents.instance.inject(const LiveEvent('roster', 'a2', {'change': 'created'}));
    await _afterEvent(tester);
    expect(find.text('Cem'), findsOneWidget);
    // Now the first, older read comes back — without Cem. Cem stays.
    await tester.pump(const Duration(seconds: 2));
    await _settle(tester);
    expect(find.text('Cem'), findsOneWidget, reason: 'the older read must not overwrite the newer one');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('the office reads agents and departments again on a roster event', (tester) async {
    final instance = _Instance();
    await _pump(tester, OfficeSpace(api: instance.api(), me: _me(), onOpen: (_, _, _, _) {}));
    final agents = instance.agentReads;
    final departments = instance.departmentReads;
    LiveEvents.instance.inject(const LiveEvent('task', 'a1', {}));
    await _afterEvent(tester);
    expect(instance.agentReads, agents, reason: 'a task moves who works, not the building');

    LiveEvents.instance.inject(const LiveEvent('roster', '', {'change': 'departments'}));
    await _afterEvent(tester);
    expect(instance.agentReads, agents + 1);
    expect(instance.departmentReads, departments + 1);
    await tester.pumpWidget(const SizedBox());
  });
}
