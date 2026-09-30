import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/live.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/screens/new_conversation.dart';
import 'package:covey_mobile/screens/thread.dart';
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

http.Response _json(Object body, [int status = 200, Map<String, String> headers = const {}]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8', ...headers},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 3; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    for (var j = 0; j < 4; j++) {
      await tester.pump(const Duration(milliseconds: 50));
    }
  }
}

const _me = '00000000-0000-0000-0000-00000000000a';
const _bea = '11111111-2222-3333-4444-555555555555';
const _bob = '22222222-3333-4444-5555-666666666666';

Map<String, Object?> _member(String kind, String id, String name, {String slug = '', String role = 'member'}) => {
  'kind': kind,
  'id': id,
  'name': name,
  if (slug.isNotEmpty) 'slug': slug,
  'role': role,
  'joined_at': '2026-09-29T08:00:00Z',
  'muted': false,
};

Map<String, Object?> _message(String id, String kind, String authorId, String name, String text, String at) => {
  'id': id,
  'conversation_id': 'c1',
  'author_kind': kind,
  'author_id': authorId,
  'author_name': name,
  'text': text,
  'kind': 'text',
  'created_at': at,
};

Map<String, Object?> _group({int unread = 0}) => {
  'id': 'c1',
  'kind': 'group',
  'title': 'Mittag',
  'created_at': '2026-09-29T08:00:00Z',
  'last_message_at': '2026-09-29T09:00:00Z',
  'members': [
    _member('human', _me, 'Ada Lovelace', role: 'owner'),
    _member('agent', _bea, 'Bea', slug: 'bea'),
    _member('human', _bob, 'Bob Builder'),
  ],
  'unread': unread,
  'muted': false,
  'last': _message('m1', 'agent', _bea, 'Bea', 'Die Rechnung ist geprüft.', '2026-09-29T09:00:00Z'),
};

final _agents = [
  {'id': _bea, 'slug': 'bea', 'display_name': 'Bea', 'hired_at': '2026-09-01T00:00:00Z', 'status': 'sleeping'},
];

Map<String, Object?> _meJson({bool canWrite = true, bool? canChat}) => {
  'ID': _me,
  'Email': 'ada@example.org',
  'DisplayName': 'Ada Lovelace',
  'Role': canWrite ? 'org_admin' : 'auditor',
  'TeamSurface': true,
  'CanWrite': canWrite,
  'CanChat': ?canChat,
};

void main() {
  setUpAll(() => initializeDateFormatting());

  testWidgets('an instance without conversations keeps the per-agent list (#479)', (tester) async {
    final asked = <String>[];
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        asked.add(p);
        if (p.endsWith('/auth/me')) return _json(_meJson());
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/agents')) return _json(_agents);
        if (p.endsWith('/me/threads')) return _json({'threads': []});
        if (p.endsWith('/agents/$_bea/thread')) return _json({'entries': [], 'pending': false});
        return http.Response('404 page not found', 404);
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);

    expect(asked, contains('/api/v1/conversations'), reason: 'it asks');
    expect(find.text('Gespräche'), findsNothing, reason: 'no section for what the instance does not have');
    expect(find.text('Neues Gespräch'), findsNothing);
    await tester.tap(find.text('Bea'));
    await _settle(tester);
    final thread = tester.widget<ThreadScreen>(find.byType(ThreadScreen));
    expect(thread.conversation, isNull, reason: 'the agent’s thread, as before');
    expect(asked, contains('/api/v1/agents/$_bea/thread'));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a seat that may not hand over work still chats when the instance says it may (#440)', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/auth/me')) return _json(_meJson(canWrite: false, canChat: true));
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/agents')) return _json(_agents);
        if (p.endsWith('/me/threads')) return _json({'threads': []});
        if (p.endsWith('/conversations')) return _json({'conversations': []});
        if (p.endsWith('/thread')) return _json({'entries': [], 'pending': false});
        return http.Response('', 404);
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);
    await tester.tap(find.text('Bea'));
    await _settle(tester);
    expect(find.byType(ThreadScreen), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a group: listed with its newest line, read with deltas, written in, @ offers the members', (
    tester,
  ) async {
    final asked = <Uri>[];
    final etags = <String?>[];
    final posted = <String>[];
    String? readAt;
    var messages = [_message('m1', 'agent', _bea, 'Bea', 'Die Rechnung ist geprüft.', '2026-09-29T09:00:00Z')];
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/auth/me')) return _json(_meJson());
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/agents')) return _json(_agents);
        if (p.endsWith('/me/threads')) return _json({'threads': []});
        if (p.endsWith('/conversations')) {
          return _json({
            'conversations': [_group(unread: readAt == null ? 3 : 0)],
          });
        }
        if (p.endsWith('/conversations/c1')) return _json(_group());
        if (p.endsWith('/conversations/c1/read')) {
          readAt = (jsonDecode(req.body) as Map<String, dynamic>)['at'] as String;
          return http.Response('', 204);
        }
        if (p.endsWith('/conversations/c1/messages')) {
          if (req.method == 'POST') {
            final text = (jsonDecode(req.body) as Map<String, dynamic>)['text'] as String;
            posted.add(text);
            final m = _message('m${messages.length + 1}', 'human', _me, 'Ada Lovelace', text, '2026-09-29T09:05:00Z');
            messages = [...messages, m];
            return _json({'message': m, 'tasks': [], 'pending': false}, 201);
          }
          asked.add(req.url);
          etags.add(req.headers['If-None-Match']);
          final after = req.url.queryParameters['after'];
          final i = after == null ? -1 : messages.indexWhere((m) => m['id'] == after);
          final page = {'messages': messages.sublist(i + 1), 'more': false, 'pending': false};
          final etag = '"${after ?? 'first'}-${messages.length}"';
          if (req.headers['If-None-Match'] == etag) return http.Response('', 304);
          return _json(page, 200, {'etag': etag});
        }
        return http.Response('', 404);
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);

    expect(find.text('Gespräche'), findsOneWidget);
    expect(find.text('Neues Gespräch'), findsOneWidget, reason: 'the way to start one stands at the section');
    expect(find.text('Mittag'), findsOneWidget);
    expect(find.text('Bea: Die Rechnung ist geprüft.'), findsOneWidget, reason: 'in a group, who said it');
    expect(find.text('3'), findsWidgets, reason: 'what is unread');

    await tester.tap(find.text('Mittag'));
    await _settle(tester);
    final screen = tester.widget<ThreadScreen>(find.byType(ThreadScreen));
    expect(screen.conversation?.id, 'c1');
    expect(find.text('Die Rechnung ist geprüft.'), findsOneWidget);
    expect(find.text('Mitglieder: 3'), findsOneWidget);
    expect(readAt, '2026-09-29T09:00:00.000Z', reason: 'read up to the newest message shown');
    expect(asked.first.queryParameters, isEmpty, reason: 'the first read is the newest page');

    // Something moved: only what came after the newest message is asked for.
    LiveEvents.instance.inject(const LiveEvent('chat', '', {'conversation_id': 'c1'}));
    await _settle(tester);
    expect(asked.last.queryParameters, {'after': 'm1'});
    LiveEvents.instance.inject(const LiveEvent('chat', '', {'conversation_id': 'c1'}));
    await _settle(tester);
    expect(etags.last, '"m1-1"', reason: 'the same delta again carries its ETag and costs a 304');
    // Another conversation's event is not this one's.
    final before = asked.length;
    LiveEvents.instance.inject(const LiveEvent('chat', '', {'conversation_id': 'c2'}));
    await _settle(tester);
    expect(asked.length, before);

    // "@b" offers Bea (the agent, by slug) and Bob (by first name).
    final field = find.byType(TextField);
    await tester.enterText(field, 'Hallo @b');
    await tester.pump();
    expect(find.text('@bea'), findsOneWidget);
    expect(find.text('@Bob'), findsOneWidget);
    await tester.tap(find.text('@bea'));
    await tester.pump();
    expect(tester.widget<TextField>(field).controller!.text, 'Hallo @bea ');
    expect(find.text('@Bob'), findsNothing, reason: 'the list closes once the handle is in');

    await tester.enterText(field, 'Hallo @bea, bitte buchen.');
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await _settle(tester);
    expect(posted, ['Hallo @bea, bitte buchen.']);
    expect(find.text('Hallo @bea, bitte buchen.'), findsOneWidget, reason: 'the message is in the thread');
    expect(asked.last.queryParameters, {'after': 'm2'}, reason: 'after sending, still only the delta');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('starting: two picks make a group, which needs a name; agents need the manage role', (tester) async {
    Map<String, dynamic>? created;
    CoveyApi api(bool canWrite) => CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/org/chart')) {
          return _json({
            'humans': [
              {'id': _me, 'display_name': 'Ada Lovelace', 'email': 'ada@example.org'},
              {'id': _bob, 'display_name': 'Bob Builder', 'email': 'bob@example.org', 'job_title': 'Einkauf'},
            ],
            'agents': [
              ..._agents,
              {'id': 'x', 'slug': 'kai', 'display_name': 'Kai', 'status': 'sleeping'},
            ],
          });
        }
        if (p.endsWith('/me/reachable-agents')) {
          return _json({
            'reach': 'org',
            'agents': [_bea, 'x'],
          });
        }
        if (p.endsWith('/conversations') && req.method == 'POST') {
          created = jsonDecode(req.body) as Map<String, dynamic>;
          return _json(_group(), 201);
        }
        return http.Response('', 404);
      }),
    );
    final me = _meJson();
    // Pushed over a screen, as the team space does: it pops with what it
    // opened.
    Future<void> open(bool canWrite) async {
      await tester.pumpWidget(const SizedBox());
      await tester.pumpWidget(
        await tester.runAsync(
              () => _app(
                Builder(
                  builder: (context) => TextButton(
                    onPressed: () => Navigator.of(context).push(
                      MaterialPageRoute<void>(
                        builder: (_) =>
                            NewConversationScreen(api: api(canWrite), me: Me.fromJson({...me, 'CanWrite': canWrite})),
                      ),
                    ),
                    child: const Text('los'),
                  ),
                ),
              ),
            )
            as Widget,
      );
      await tester.tap(find.text('los'));
      await _settle(tester);
    }

    await open(true);
    expect(find.text('Ada Lovelace'), findsNothing, reason: 'not oneself');
    expect(find.text('Kai'), findsNothing, reason: 'a draft is nobody to talk to yet');
    FilledButton button() => tester.widget<FilledButton>(find.byType(FilledButton));
    expect(button().onPressed, isNull, reason: 'nothing picked');
    await tester.tap(find.text('Bob Builder'));
    await tester.pump();
    expect(find.text('Gespräch öffnen'), findsOneWidget);
    expect(button().onPressed, isNotNull, reason: 'one pick opens the direct conversation');
    await tester.tap(find.text('Bea').last);
    await tester.pump();
    expect(find.text('Gruppe anlegen'), findsOneWidget);
    expect(find.text('Eine Gruppe braucht einen Namen.'), findsOneWidget);
    expect(button().onPressed, isNull, reason: 'a group needs a name');
    await tester.enterText(find.widgetWithText(TextField, 'Gruppenname'), 'Mittag');
    await tester.pump();
    await tester.tap(find.byType(FilledButton));
    await _settle(tester);
    expect(find.byType(NewConversationScreen), findsNothing, reason: 'it closes with the group it made');
    expect(created, {
      'kind': 'group',
      'title': 'Mittag',
      'members': [
        {'kind': 'human', 'id': _bob},
        {'kind': 'agent', 'id': _bea},
      ],
    });

    await open(false);
    await tester.tap(find.text('Bob Builder'));
    await tester.tap(find.text('Bea').last);
    await tester.pump();
    await tester.enterText(find.widgetWithText(TextField, 'Gruppenname'), 'Mittag');
    await tester.pump();
    expect(find.textContaining('braucht das Recht'), findsOneWidget);
    expect(button().onPressed, isNull, reason: 'a group with an agent is the manage roles’');
    await tester.pumpWidget(const SizedBox());
  });
}
