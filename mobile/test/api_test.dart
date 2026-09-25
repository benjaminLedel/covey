import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

void main() {
  group('parseInstance', () {
    test('adds https to a bare host and drops a trailing slash', () {
      expect(parseInstance('covey.example.org/').toString(), 'https://covey.example.org');
      expect(parseInstance(' https://x.org/covey/ ').toString(), 'https://x.org/covey');
      expect(
        parseInstance('https://x.org:8443/?next=1#top').toString(),
        'https://x.org:8443',
        reason: 'a query or fragment in a pasted address is not part of the instance',
      );
    });

    test('refuses plain http outside loopback (spec/27: HTTPS only)', () {
      expect(() => parseInstance('http://covey.example.org'), throwsFormatException);
      expect(() => parseInstance(''), throwsFormatException);
    });
  });

  test('sends the key as a bearer and reads the API under /api/v1', () async {
    late http.Request seen;
    final api = CoveyApi(
      Uri.parse('https://c.example/sub'),
      'covey_abc',
      client: MockClient((req) async {
        seen = req;
        return http.Response(
          jsonEncode({'Email': 'a@b.c', 'DisplayName': 'A', 'Role': 'org_admin', 'TeamSurface': false}),
          200,
        );
      }),
    );
    final me = await api.me();
    expect(seen.url.toString(), 'https://c.example/sub/api/v1/auth/me');
    expect(seen.headers['Authorization'], 'Bearer covey_abc');
    expect(me.teamSurface, isFalse);
  });

  test('a query stays a query, not part of the path', () async {
    late Uri seen;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        seen = req.url;
        return http.Response(jsonEncode({'items': [], 'pending': 0}), 200);
      }),
    );
    await api.waiting();
    expect(seen.path, '/api/v1/inbox');
    expect(seen.queryParameters, {'status': 'open', 'sort': 'urgent', 'limit': '100'});
  });

  test('a plain-text error is an HTTP error with its sentence, not a parse error', () async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((_) async => http.Response('404 page not found\n', 404)),
    );
    await expectLater(
      api.agents(),
      throwsA(
        isA<ApiException>()
            .having((e) => e.status, 'status', 404)
            .having((e) => e.message, 'message', 'HTTP 404: 404 page not found'),
      ),
    );
  });

  test('an instance older than #328 has the team surface on', () {
    expect(Me.fromJson({'Email': 'a@b.c'}).teamSurface, isTrue);
  });

  test('a refusal carries the status and the instance\'s sentence', () async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((_) async {
        return http.Response(jsonEncode({'error': 'the team surface is not enabled for this organisation'}), 403);
      }),
    );
    await expectLater(
      api.send('a1', 'hallo'),
      throwsA(
        isA<ApiException>()
            .having((e) => e.status, 'status', 403)
            .having((e) => e.message, 'message', contains('team surface')),
      ),
    );
  });

  test('a reply that woke nobody is not an error', () async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((_) async {
        return http.Response(jsonEncode({'note': {}, 'woken': false}), 200);
      }),
    );
    expect(await api.reply('t1', 'ja'), isFalse);
  });

  test('the thread: sides and the open question', () {
    final th = Thread.fromJson({
      'pending': true,
      'entries': [
        {
          'kind': 'message',
          'id': 't1',
          'task_id': 't1',
          'author': 'chat:a@b.c',
          'text': 'Bitte prüfen',
          'task_state': 'blocked',
        },
        {
          'kind': 'question',
          'id': 'n1',
          'task_id': 't1',
          'author': 'agent',
          'text': 'Darf ich?',
          'task_state': 'blocked',
        },
        {'kind': 'question', 'id': 'n2', 'task_id': 't2', 'author': 'agent', 'text': 'Alt', 'task_state': 'done'},
      ],
    });
    expect(th.pending, isTrue);
    expect(th.entries[0].fromPerson, isTrue);
    expect(th.entries[1].fromPerson, isFalse);
    expect(th.entries[1].isOpenQuestion, isTrue);
    expect(th.entries[2].isOpenQuestion, isFalse, reason: 'a question on a finished task is history, not a door');
  });

  test('applicants are not colleagues', () {
    expect(Agent.fromJson({'id': 'a', 'status': 'applicant'}).isApplicant, isTrue);
    expect(Agent.fromJson({'id': 'a', 'status': 'sleeping'}).isApplicant, isFalse);
  });
}
