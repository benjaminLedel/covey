import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
import 'package:file_picker/file_picker.dart';
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

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 8; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

Attachment _file(String name, String content) =>
    Attachment(name: name, length: content.length, open: () => Stream.value(utf8.encode(content)));

void main() {
  testWidgets('files go into the inbox first, then the message names them (#340)', (tester) async {
    final calls = <String>[];
    String? uploadBody;
    Map<String, Object?>? message;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        calls.add('${req.method} ${req.url.path}?${req.url.query}');
        if (req.url.path.endsWith('/files/upload')) {
          uploadBody = utf8.decode(req.bodyBytes, allowMalformed: true);
          return _json({'uploaded': []});
        }
        if (req.url.path.endsWith('/messages')) {
          message = jsonDecode(req.body) as Map<String, Object?>;
          return _json({'message': {}, 'pending': true});
        }
        return _json({'entries': [], 'pending': false});
      }),
    );
    final me = Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true);
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              ThreadScreen(
                api: api,
                agentId: 'a1',
                agentName: 'Bea',
                me: me,
                pick: (type) async => [_file('whiteboard.jpg', 'JPEGDATA'), _file('angebot.pdf', 'PDFDATA')],
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

    final upload = calls.indexWhere((c) => c.startsWith('POST /api/v1/agents/a1/files/upload?path=eingang'));
    final send = calls.indexWhere((c) => c.startsWith('POST /api/v1/agents/a1/messages'));
    expect(upload, isNonNegative);
    expect(send, greaterThan(upload), reason: 'the files first, then the message');
    expect(uploadBody, contains('filename="whiteboard.jpg"'));
    expect(uploadBody, contains('JPEGDATA'));
    expect(message?['text'], 'Bitte prüfen\n\n(Anhang im Arbeitsplatz: eingang/whiteboard.jpg, eingang/angebot.pdf)');
    expect(find.text('whiteboard.jpg'), findsNothing, reason: 'sent files leave the composer');
    await tester.pumpWidget(const SizedBox());
  });

  test('the attachment line is the web\'s wording', () async {
    final s = await Strings.load(const Locale('de'));
    expect(s.t('team.anhangZeile', args: {'pfade': 'eingang/x.pdf'}), contains('eingang/x.pdf'));
    expect(FileType.media, isNotNull);
  });
}
