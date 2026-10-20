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
import 'package:intl/date_symbol_data_local.dart';

// The app listens to the instance's event stream instead of asking every few
// seconds (#419).

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

void main() {
  setUpAll(() => initializeDateFormatting());
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

  test('a stream opened again after a break tells every listener to look once', () async {
    var opened = 0;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        opened++;
        // The first connection ends at once — a deploy, a dead proxy; the
        // second stays (as far as a mock can).
        return http.Response(
          opened == 1 ? 'event: hello\ndata: {}\n\n' : 'event: hello\ndata: {}\n\n: keepalive\n\n',
          200,
        );
      }),
    );
    final mine = <void>[];
    final strict = <void>[];
    final s1 = LiveEvents.instance
        .of({'chat'}, agentId: 'a1', settle: const Duration(milliseconds: 20))
        .listen(mine.add);
    final s2 = LiveEvents.instance
        .of({'chat'}, agentId: 'a1', settle: const Duration(milliseconds: 20), withResync: false)
        .listen(strict.add);
    LiveEvents.instance.start(api);
    // The first open is not "again": nobody is asked to look.
    await Future<void>.delayed(const Duration(milliseconds: 200));
    expect(mine, isEmpty, reason: 'the first open follows no break');
    // The back-off is a second; then the stream is open again.
    await Future<void>.delayed(const Duration(milliseconds: 1500));
    expect(opened, greaterThanOrEqualTo(2));
    expect(mine.length, 1, reason: 'open again after a break: one look, whatever the filter (#536)');
    expect(strict, isEmpty, reason: 'a listener that asked for real events only');
    await s1.cancel();
    await s2.cancel();
  });

  testWidgets('an older read arriving late does not put the conversation back (#536)', (tester) async {
    var reads = 0;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (!req.url.path.endsWith('/thread')) return _json({});
        reads++;
        if (reads == 1) {
          // The first read is slow and sees the conversation before the answer.
          await Future<void>.delayed(const Duration(seconds: 2));
          return _json({'pending': true, 'entries': []});
        }
        return _json({
          'pending': false,
          'entries': [
            {
              'kind': 'answer',
              'id': 'n1',
              'author': 'agent',
              'text': 'Die Antwort steht',
              'at': '2026-10-03T10:00:00Z',
            },
          ],
        });
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
    await tester.pump(const Duration(milliseconds: 50));
    // The answer's event: a second, fast read while the first is still out.
    LiveEvents.instance.inject(const LiveEvent('chat', 'a1', {'state': 'said'}));
    await tester.pump(const Duration(seconds: 1));
    await tester.pump(const Duration(milliseconds: 100));
    expect(reads, 2);
    expect(find.text('Die Antwort steht'), findsOneWidget);
    // Now the first, older read comes back — empty. The answer stays.
    await tester.pump(const Duration(seconds: 2));
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.text('Die Antwort steht'), findsOneWidget, reason: 'the older read must not overwrite the newer one');
    await tester.pumpWidget(const SizedBox());
  });

  test('the stream asks for the longer keepalive (#447)', () async {
    Uri? asked;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        asked = req.url;
        return http.Response('event: hello\ndata: {}\n\n', 200);
      }),
    );
    LiveEvents.instance.start(api);
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(asked!.queryParameters['keepalive'], '${LiveEvents.keepalive.inSeconds}');
    expect(asked!.queryParameters['types']!.split(','), contains('roster'));
  });

  test('a burst that never pauses still reloads, at most maxWait after its first event (#447)', () async {
    final fired = <DateTime>[];
    final sub = LiveEvents.instance
        .of({'recording-like'}, settle: const Duration(milliseconds: 100), maxWait: const Duration(milliseconds: 300))
        .listen((_) => fired.add(DateTime.now()));
    // An event every 50 ms for 700 ms: the settling time never passes.
    for (var i = 0; i < 14; i++) {
      LiveEvents.instance.inject(const LiveEvent('recording-like', '', {}));
      await Future<void>.delayed(const Duration(milliseconds: 50));
    }
    expect(fired.length, greaterThanOrEqualTo(2), reason: 'the cap fires while the burst goes on');
    await Future<void>.delayed(const Duration(milliseconds: 200));
    final after = fired.length;
    await Future<void>.delayed(const Duration(milliseconds: 400));
    expect(fired.length, after, reason: 'quiet after the burst has settled');
    await sub.cancel();
  });

  test('in the background the stream is closed; back in front it opens at once and everyone looks (#447)', () async {
    var opened = 0;
    final streams = <StreamController<List<int>>>[];
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient.streaming((req, _) async {
        opened++;
        final c = StreamController<List<int>>();
        streams.add(c);
        c.add(utf8.encode('event: hello\ndata: {}\n\n'));
        return http.StreamedResponse(c.stream, 200);
      }),
    );
    final looks = <void>[];
    final sub = LiveEvents.instance.of({'chat'}, settle: const Duration(milliseconds: 20)).listen(looks.add);
    LiveEvents.instance.start(api);
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(opened, 1);
    expect(LiveEvents.instance.connected, isTrue);

    LiveEvents.instance.suspend();
    expect(LiveEvents.instance.connected, isFalse);
    // Longer than the reconnect back-off: nothing opens it while the app is away.
    await Future<void>.delayed(const Duration(milliseconds: 1500));
    expect(opened, 1, reason: 'closed in the background, and not opened again');
    expect(looks, isEmpty);

    LiveEvents.instance.resume();
    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(opened, 2, reason: 'opened again at once, not after the watchdog');
    expect(LiveEvents.instance.connected, isTrue);
    expect(looks.length, 1, reason: 'one catch-up look for whatever happened meanwhile');
    await sub.cancel();
    for (final c in streams) {
      await c.close();
    }
  });

  test('only the newest of overlapping reads lands (#619)', () {
    final reads = Latest();
    final first = reads.begin();
    final second = reads.begin();
    expect(reads.isCurrent(first), isFalse);
    expect(reads.isCurrent(second), isTrue);
  });
}
