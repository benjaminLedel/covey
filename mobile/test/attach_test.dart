import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/chat_files.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
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

Attachment _file(String name, String content, {int? length}) =>
    Attachment(name: name, length: length ?? content.length, open: () => Stream.value(utf8.encode(content)));

final _me = Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true, id: 'me');

/// The message request as the server would read it: the content type and
/// the body as text.
typedef _Sent = ({String type, String body});

void main() {
  setUpAll(() => initializeDateFormatting());

  testWidgets('files travel with the message itself, as a form (#586)', (tester) async {
    final calls = <String>[];
    _Sent? sent;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        calls.add('${req.method} ${req.url.path}');
        if (req.url.path.endsWith('/messages')) {
          sent = (type: req.headers['content-type'] ?? '', body: utf8.decode(req.bodyBytes, allowMalformed: true));
          return _json({'message': {}, 'pending': true}, status: 202);
        }
        return _json({'entries': [], 'pending': false});
      }),
    );
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              ThreadScreen(
                api: api,
                agentId: 'a1',
                agentName: 'Bea',
                me: _me,
                pick: (source) async => [_file('whiteboard.jpg', 'JPEGDATA'), _file('angebot.pdf', 'PDFDATA')],
              ),
            ),
          )
          as Widget,
    );
    await _settle(tester);

    await tester.tap(find.byTooltip('Datei anhängen'));
    await _settle(tester);
    await tester.tap(find.text('Datei'));
    await _settle(tester);
    expect(find.text('whiteboard.jpg'), findsOneWidget, reason: 'attached files stand as chips');
    expect(find.text('angebot.pdf'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'Bitte prüfen');
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await _settle(tester);

    expect(calls.where((c) => c.contains('/files/upload')), isEmpty, reason: 'no detour through the Files tab');
    expect(calls, contains('POST /api/v1/agents/a1/messages'));
    expect(sent?.type, startsWith('multipart/form-data'));
    expect(sent?.body, contains('name="text"'));
    expect(sent?.body, contains('Bitte prüfen'));
    expect(sent?.body, contains('name="file"; filename="whiteboard.jpg"'));
    expect(sent?.body, contains('JPEGDATA'));
    expect(sent?.body, contains('filename="angebot.pdf"'));
    expect(find.text('whiteboard.jpg'), findsNothing, reason: 'sent files leave the composer');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a message without files stays JSON', (tester) async {
    _Sent? sent;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.url.path.endsWith('/messages')) {
          sent = (type: req.headers['content-type'] ?? '', body: req.body);
          return _json({'message': {}, 'pending': true}, status: 202);
        }
        return _json({'entries': [], 'pending': false});
      }),
    );
    await tester.pumpWidget(
      await tester.runAsync(() => _app(ThreadScreen(api: api, agentId: 'a1', agentName: 'Bea', me: _me))) as Widget,
    );
    await _settle(tester);
    await tester.enterText(find.byType(TextField), 'Hallo');
    await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await _settle(tester);
    expect(sent?.type, startsWith('application/json'));
    expect(jsonDecode(sent!.body), {'text': 'Hallo'});
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a conversation takes files too, on its own route', (tester) async {
    _Sent? sent;
    final conv = Conversation(
      id: 'c1',
      kind: 'group',
      title: 'Mittag',
      members: [
        ConversationMember(kind: 'human', id: 'me', name: 'Ada', role: 'owner'),
        ConversationMember(kind: 'agent', id: 'a1', name: 'Bea', slug: 'bea'),
      ],
    );
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.method == 'POST' && req.url.path == '/api/v1/conversations/c1/messages') {
          sent = (type: req.headers['content-type'] ?? '', body: utf8.decode(req.bodyBytes, allowMalformed: true));
          return _json({
            'message': {
              'id': 'm1',
              'conversation_id': 'c1',
              'author_kind': 'human',
              'author_id': 'me',
              'text': '',
              'kind': 'text',
              'created_at': '2026-10-13T10:00:00Z',
              'attachments': [
                {'id': 'f1', 'name': 'plan.pdf', 'mime': 'application/pdf', 'size': 2048},
              ],
            },
          }, status: 201);
        }
        if (req.url.path.endsWith('/messages')) return _json({'messages': [], 'more': false, 'pending': false});
        if (req.url.path.endsWith('/read')) return _json({});
        return _json({
          'id': 'c1',
          'kind': 'group',
          'title': 'Mittag',
          'members': [
            {'kind': 'human', 'id': 'me', 'name': 'Ada', 'role': 'owner'},
            {'kind': 'agent', 'id': 'a1', 'name': 'Bea', 'slug': 'bea'},
          ],
        });
      }),
    );
    await tester.pumpWidget(
      await tester.runAsync(() => _app(ThreadScreen.conversation(api: api, conversation: conv, me: _me))) as Widget,
    );
    await _settle(tester);
    // ThreadScreen.conversation has no pick hook: the picker is the
    // system's. The send path is the same one, driven through the API.
    await tester.runAsync(() => api.postConversationMessage('c1', '', files: [_file('plan.pdf', 'PDFDATA')]));
    expect(sent?.type, startsWith('multipart/form-data'));
    expect(sent?.body, isNot(contains('name="text"')), reason: 'files alone are a message');
    expect(sent?.body, contains('filename="plan.pdf"'));
    expect(find.byTooltip('Datei anhängen'), findsOneWidget, reason: 'the button is there in a conversation');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('what crosses the limits is not taken, and the person hears why', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async => _json({'entries': [], 'pending': false})),
    );
    var batch = <Attachment>[];
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(ThreadScreen(api: api, agentId: 'a1', agentName: 'Bea', me: _me, pick: (_) async => batch)),
          )
          as Widget,
    );
    await _settle(tester);

    Future<void> attach() async {
      await tester.tap(find.byTooltip('Datei anhängen'));
      await _settle(tester);
      await tester.tap(find.text('Datei'));
      await _settle(tester);
    }

    batch = [for (var i = 0; i < 11; i++) _file('f$i.txt', 'x')];
    await attach();
    expect(find.text('Höchstens 10 Dateien je Nachricht.'), findsOneWidget);
    expect(find.text('f0.txt'), findsNothing);
    ScaffoldMessenger.of(tester.element(find.byType(Scaffold).first)).clearSnackBars();
    await _settle(tester);

    batch = [_file('gross.mov', 'x', length: 11 * 1024 * 1024)];
    await attach();
    expect(find.text('Zusammen höchstens 10 MB je Nachricht.'), findsOneWidget);
    expect(find.text('gross.mov'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a message shows its files: a picture fetched with the key, a chip that saves', (tester) async {
    final fetched = <String>[];
    final png = base64Decode(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
    );
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.url.path.startsWith('/api/v1/chat/attachments/')) {
          fetched.add('${req.url.path}?${req.url.query} ${req.headers['Authorization']}');
          return http.Response.bytes(req.url.path.endsWith('img1') ? png : utf8.encode('PDFDATA'), 200);
        }
        return _json({
          'entries': [
            {
              'kind': 'answer',
              'id': 'e1',
              'author': 'agent',
              'text': 'Hier ist der Entwurf.',
              'at': '2026-10-13T10:00:00Z',
              'attachments': [
                {'id': 'img1', 'name': 'post.png', 'mime': 'image/png', 'size': 70},
                {'id': 'doc1', 'name': 'angebot.pdf', 'mime': 'application/pdf', 'size': 2048},
              ],
            },
          ],
          'pending': false,
        });
      }),
    );
    final saved = <String>[];
    saveFile = ({required fileName, required bytes, required mimeType}) async {
      saved.add('$fileName $mimeType ${utf8.decode(bytes, allowMalformed: true)}');
      return Uri.parse('file:///tmp/$fileName');
    };
    addTearDown(() => saveFile = ({required fileName, required bytes, required mimeType}) async => null);

    await tester.pumpWidget(
      await tester.runAsync(() => _app(ThreadScreen(api: api, agentId: 'a1', agentName: 'Bea', me: _me))) as Widget,
    );
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await _settle(tester);

    expect(find.text('Hier ist der Entwurf.'), findsOneWidget);
    expect(fetched, contains('/api/v1/chat/attachments/img1? Bearer k'), reason: 'the picture comes with the key');
    expect(find.byType(Image), findsWidgets);
    expect(find.text('angebot.pdf'), findsOneWidget);
    expect(find.text('2 KB'), findsOneWidget);

    await tester.tap(find.text('angebot.pdf'));
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
    await _settle(tester);
    expect(fetched, contains('/api/v1/chat/attachments/doc1?download=1 Bearer k'));
    expect(saved, ['angebot.pdf application/pdf PDFDATA']);
    expect(find.text('Gespeichert'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  test('messages read their attachments', () {
    final m = ConversationMessage.fromJson({
      'id': 'm1',
      'attachments': [
        {'id': 'a', 'name': 'clip.mp4', 'mime': 'video/mp4', 'size': 5, 'home_path': 'chat/1234abcd/clip.mp4'},
      ],
    });
    expect(m.files.single.isVideo, isTrue);
    expect(m.files.single.homePath, 'chat/1234abcd/clip.mp4');
    expect(ThreadEntry.fromMessage(m, meId: 'me').files.single.name, 'clip.mp4');
    expect(ThreadEntry.fromJson({'kind': 'message'}).files, isEmpty);
  });

  test('the limits are the server\'s', () async {
    final s = await Strings.load(const Locale('en'));
    expect(attachmentLimit(s, [for (var i = 0; i < 10; i++) _file('f$i', 'x')]), isNull);
    expect(attachmentLimit(s, [for (var i = 0; i < 11; i++) _file('f$i', 'x')]), 'At most 10 files per message.');
    expect(attachmentLimit(s, [_file('a', 'x', length: chatMaxBytes)]), isNull);
    expect(attachmentLimit(s, [_file('a', 'x', length: chatMaxBytes + 1)]), 'At most 10 MB together per message.');
    expect(fileSize(500), '500 B');
    expect(fileSize(2048), '2 KB');
    expect(fileSize(3 * 1024 * 1024), '3.0 MB');
  });
}
