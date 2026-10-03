import 'dart:async';
import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/live.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

// The app listens to the instance's event stream instead of asking every few
// seconds (#419).

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

void main() {
  tearDown(() => LiveEvents.instance.stop());

  test('the event stream is read, and a burst of events is one reload', () async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        expect(req.url.path, '/api/v1/events');
        expect(req.headers['Accept'], 'text/event-stream');
        // Only the types the app reads (#535); never `recording`.
        expect(req.url.queryParameters['types']!.split(','), unorderedEquals(LiveEvents.types));
        return http.Response(
          'event: hello\ndata: {}\n\n'
          ': keepalive\n\n'
          'event: chat\ndata: {"type":"chat","agent_id":"a1","data":{"state":"answered"}}\n\n'
          'event: task\ndata: {"type":"task","agent_id":"a1"}\n\n'
          'event: chat\ndata: {"type":"chat","agent_id":"a2"}\n\n',
          200,
        );
      }),
    );
    final mine = <void>[];
    final all = <void>[];
    final s1 = LiveEvents.instance
        .of({'chat', 'task'}, agentId: 'a1', settle: const Duration(milliseconds: 50))
        .listen(mine.add);
    final s2 = LiveEvents.instance.of({'chat'}, settle: const Duration(milliseconds: 50)).listen(all.add);
    LiveEvents.instance.start(api);
    await Future<void>.delayed(const Duration(milliseconds: 200));
    expect(mine.length, 1, reason: 'two events of a1 in a burst, one reload');
    expect(all.length, 1);
    await s1.cancel();
    await s2.cancel();
  });

  testWidgets('an open conversation reloads on its agent\'s event, not on another\'s', (tester) async {
    var fetched = 0;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.url.path.endsWith('/thread')) fetched++;
        return _json({'pending': false, 'entries': []});
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
          me: Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true),
        ),
      ),
    );
    for (var i = 0; i < 5; i++) {
      await tester.pump(const Duration(milliseconds: 50));
    }
    final vorher = fetched;
    LiveEvents.instance.inject(const LiveEvent('chat', 'a2', {}));
    await tester.pump(const Duration(seconds: 1));
    expect(fetched, vorher, reason: 'another agent\'s event');
    LiveEvents.instance.inject(const LiveEvent('chat', 'a1', {}));
    await tester.pump(const Duration(seconds: 1));
    await tester.pump(const Duration(milliseconds: 100));
    expect(fetched, vorher + 1, reason: 'its own agent\'s event reloads at once');
    await tester.pumpWidget(const SizedBox());
  });
}
