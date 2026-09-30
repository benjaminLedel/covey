import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/pairing.dart';
import 'package:covey_mobile/push.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

http.Response _json(Object body, [int status = 200, Map<String, String> headers = const {}]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8', ...headers},
);

const _me = '00000000-0000-0000-0000-00000000000a';
const _agent = '11111111-2222-3333-4444-555555555555';

/// A conversation as GET /conversations lists it (internal/chat).
Map<String, Object?> _summary() => {
  'id': 'c1',
  'org_id': 'o1',
  'kind': 'group',
  'title': 'Mittag',
  'created_at': '2026-09-29T08:00:00Z',
  'last_message_at': '2026-09-29T09:00:00Z',
  'members': [
    {'kind': 'human', 'id': _me, 'name': 'Ada', 'role': 'owner', 'joined_at': '2026-09-29T08:00:00Z', 'muted': false},
    {
      'kind': 'agent',
      'id': _agent,
      'name': 'Bea',
      'slug': 'bea',
      'role': 'member',
      'joined_at': '2026-09-29T08:00:00Z',
      'muted': false,
    },
    {
      'kind': 'human',
      'id': 'h2',
      'name': 'Bob',
      'email': 'bob@example.org',
      'role': 'member',
      'joined_at': '2026-09-29T08:00:00Z',
      'left_at': '2026-09-29T08:30:00Z',
      'muted': false,
    },
  ],
  'unread': 3,
  'muted': false,
  'last': {
    'id': 'm2',
    'conversation_id': 'c1',
    'org_id': 'o1',
    'author_kind': 'agent',
    'author_id': _agent,
    'author_name': 'Bea',
    'text': 'Die Rechnung ist geprüft.',
    'kind': 'text',
    'created_at': '2026-09-29T09:00:00.123456Z',
  },
};

void main() {
  group('the conversation JSON', () {
    test('a summary reads members, the newest line and what is unread', () {
      final c = Conversation.fromJson(_summary());
      expect(c.group, isTrue);
      expect(c.name(_me), 'Mittag');
      expect(c.unread, 3);
      expect(c.members, hasLength(3));
      expect(c.active.map((m) => m.name), ['Ada', 'Bea'], reason: 'Bob left');
      expect(c.members[0].role, 'owner');
      expect(c.members[1].slug, 'bea');
      expect(c.last!.authorName, 'Bea');
      expect(c.latest, DateTime.utc(2026, 9, 29, 9, 0, 0, 123, 456).toLocal());
      expect(c.directAgent, isNull, reason: 'a group is no agent’s thread');
    });

    test('a direct conversation is named after the other member; with an agent it is its thread', () {
      final j = _summary()
        ..['kind'] = 'direct'
        ..['title'] = null;
      (j['members'] as List).removeLast();
      final c = Conversation.fromJson(j);
      expect(c.name(_me), 'Bea');
      expect(c.other(_me)!.id, _agent);
      expect(c.directAgent!.slug, 'bea');
    });

    test('a page, and a told result keeps its report one tap away', () {
      final p = ConversationPage.fromJson({
        'messages': [
          {
            'id': 'm1',
            'conversation_id': 'c1',
            'author_kind': 'human',
            'author_id': _me,
            'author_name': 'Ada',
            'text': 'Ist die Rechnung da?',
            'kind': 'text',
            'created_at': '2026-09-29T08:59:00Z',
          },
          {
            'id': 'm2',
            'conversation_id': 'c1',
            'author_kind': 'agent',
            'author_id': _agent,
            'author_name': 'Bea',
            'text': 'Geprüft, alles stimmt.',
            'kind': 'result',
            'task_id': 't1',
            'task_title': 'Rechnung prüfen',
            'task_state': 'done',
            'report': '## Bericht\nAlles stimmt.',
            'created_at': '2026-09-29T09:00:00Z',
          },
        ],
        'more': true,
        'pending': true,
      });
      expect(p.more, isTrue);
      expect(p.pending, isTrue);
      final mine = ThreadEntry.fromMessage(p.messages[0], meId: _me);
      expect(mine.fromPerson, isTrue);
      expect(mine.kind, 'message');
      final told = ThreadEntry.fromMessage(p.messages[1], meId: _me, slug: 'bea');
      expect(told.fromPerson, isFalse);
      expect(told.speaker, 'Bea');
      expect(told.speakerSlug, 'bea');
      expect(told.said, 'Geprüft, alles stimmt.');
      expect(told.text, '## Bericht\nAlles stimmt.');
      expect(told.taskTitle, 'Rechnung prüfen');
    });

    test('another person’s line is not the reader’s', () {
      final m = ConversationMessage.fromJson({
        'id': 'm3',
        'author_kind': 'human',
        'author_id': 'h2',
        'author_name': 'Bob',
        'text': 'Ich komme mit.',
        'kind': 'text',
      });
      final e = ThreadEntry.fromMessage(m, meId: _me);
      expect(e.fromPerson, isFalse);
      expect(e.speakerHuman, isTrue);
    });
  });

  group('CanChat (#440)', () {
    test('every seat may chat where the instance says so, whatever CanWrite says', () {
      final me = Me.fromJson({'Role': 'auditor', 'CanWrite': false, 'CanChat': true});
      expect(me.canWrite, isFalse);
      expect(me.canChat, isTrue);
    });
    test('an instance older than #440 says nothing, and CanWrite decides as before', () {
      expect(Me.fromJson({'Role': 'auditor', 'CanWrite': false}).canChat, isFalse);
      expect(Me.fromJson({'Role': 'org_admin', 'CanWrite': true}).canChat, isTrue);
      expect(Me.fromJson({}).canChat, isTrue);
    });
    test('a new photo keeps what the seat may do', () {
      final me = Me(email: '', displayName: '', role: 'auditor', teamSurface: true, canWrite: false, canChat: true);
      expect(me.withPhoto('p').canChat, isTrue);
    });
  });

  group('the conversation API', () {
    test('an instance without conversations answers 404, and the list is null', () async {
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async => http.Response('404 page not found', 404)),
      );
      expect(await api.conversations(), isNull);
    });

    test('other failures are errors, not "no conversations"', () async {
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async => _json({'error': 'boom'}, 500)),
      );
      expect(api.conversations(), throwsA(isA<ApiException>()));
    });

    test('the list is read again with its ETag, and a 304 keeps what was read (#447)', () async {
      final seen = <String?>[];
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async {
          seen.add(req.headers['If-None-Match']);
          if (req.headers['If-None-Match'] == '"e1"') return http.Response('', 304);
          return _json(
            {
              'conversations': [_summary()],
              'cursor': '2026-09-29T09:00:00Z',
            },
            200,
            {'etag': '"e1"'},
          );
        }),
      );
      expect((await api.conversations())!.single.title, 'Mittag');
      final again = await api.conversations();
      expect(seen, [null, '"e1"']);
      expect(again!.single.title, 'Mittag', reason: 'the body kept from before');
    });

    test('a delta read asks only for what came after the newest message', () async {
      final asked = <Uri>[];
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async {
          asked.add(req.url);
          return _json({'messages': [], 'more': false, 'pending': false});
        }),
      );
      await api.conversationMessages('c1');
      await api.conversationMessages('c1', after: 'm2');
      final older = ConversationMessage.fromJson({
        'id': 'm1',
        'author_kind': 'human',
        'text': '',
        'kind': 'text',
        'created_at': '2026-09-29T08:59:00Z',
      });
      await api.conversationMessages('c1', before: older);
      expect(asked[0].path, '/api/v1/conversations/c1/messages');
      expect(asked[0].query, isEmpty);
      expect(asked[1].queryParameters, {'after': 'm2'});
      expect(asked[2].queryParameters, {'before': '2026-09-29T08:59:00.000Z', 'before_id': 'm1'});
    });

    test('starting: a direct conversation takes one member, a group a title and the members', () async {
      final bodies = <Map<String, dynamic>>[];
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async {
          bodies.add(jsonDecode(req.body) as Map<String, dynamic>);
          return _json(_summary(), 201);
        }),
      );
      await api.openDirect(const MemberRef('agent', _agent));
      await api.createGroup('Mittag', const [MemberRef('human', 'h2'), MemberRef('agent', _agent)]);
      expect(bodies[0], {
        'kind': 'direct',
        'member': {'kind': 'agent', 'id': _agent},
      });
      expect(bodies[1], {
        'kind': 'group',
        'title': 'Mittag',
        'members': [
          {'kind': 'human', 'id': 'h2'},
          {'kind': 'agent', 'id': _agent},
        ],
      });
    });
  });

  group('links and notifications', () {
    test('a conversation link is /team/c/<id>, on the web or as covey://', () {
      const id = '22222222-3333-4444-5555-666666666666';
      expect(conversationLink(Uri.parse('https://c.example/team/c/$id')), id);
      expect(conversationLink(Uri.parse('covey://team/c/$id')), id);
      expect(conversationLink(Uri.parse('covey://team/$id')), isNull);
      expect(threadLinkAgent(Uri.parse('covey://team/c/$id')), isNull, reason: 'not an agent’s thread');
      expect(conversationLink(Uri.parse('covey://team/c/nope')), isNull);
    });

    test('a tapped notification opens the agent’s thread, or the conversation it names', () {
      expect(PushNotices.linkFor(_agent), Uri.parse('covey://team/$_agent'));
      expect(PushNotices.linkFor('conversation:c1'), Uri.parse('covey://team/c/c1'));
    });
  });
}
