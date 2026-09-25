import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/dictation.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/screens/notes.dart';
import 'package:covey_mobile/theme.dart';
import 'package:covey_mobile/ui.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

Future<Widget> _app(Widget home) async {
  await initializeDateFormatting();
  final strings = await Strings.load(const Locale('de'));
  return MaterialApp(
    theme: coveyTheme(Brightness.light),
    builder: (context, child) => StringsScope(strings: strings, child: child!),
    home: home,
  );
}

http.Response _json(Object body, [int status = 200]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 6; i++) {
    await tester.pump(const Duration(milliseconds: 50));
  }
}

/// Dictation without a microphone: it "hears" what the test says.
class _FakeDictation extends Dictation {
  bool _on = false;
  String _heard = '';

  @override
  bool get running => _on;

  @override
  String get text => _heard;

  @override
  Future<bool> start({bool continuous = false}) async {
    _on = true;
    notifyListeners();
    return true;
  }

  void hear(String s) {
    _heard = s;
    notifyListeners();
  }

  @override
  Future<String> stop() async {
    _on = false;
    notifyListeners();
    return _heard;
  }
}

Map<String, Object?> _note(String id, String kind, String body, {String summary = ''}) => {
  'id': id,
  'kind': kind,
  'title': '',
  'body': body,
  'summary': summary,
  'duration_seconds': kind == 'meeting' ? 1805 : 0,
  'created_at': '2026-09-25T11:40:00Z',
};

void main() {
  testWidgets('without the team surface the app is the notetaker, without a banner (#336)', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        if (req.url.path.endsWith('/auth/me')) {
          return _json({'Email': 'a@b.c', 'DisplayName': 'Ada', 'Role': 'auditor', 'TeamSurface': false});
        }
        if (req.url.path.endsWith('/me/notes')) {
          return _json({
            'notes': [_note('n1', 'meeting', 'Ada: Angebot bis Freitag.')],
            'summarize': true,
          });
        }
        return http.Response('', 404);
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);

    // One space: the capsule has nothing to switch, only the + remains.
    final capsule = tester.widget<SpaceCapsule>(find.byType(SpaceCapsule));
    expect(capsule.spaces.length, 1);
    expect(find.text('Team'), findsNothing);
    expect(find.text('Notizen'), findsWidgets);
    expect(find.textContaining('Meeting · '), findsOneWidget);
    expect(find.textContaining('30:05'), findsOneWidget, reason: 'a meeting shows how long it ran');
    expect(
      find.textContaining('Kollegen und Gespräche'),
      findsNothing,
      reason: 'the reason is in the menu, not a banner',
    );
  });

  testWidgets('with the team surface there are two spaces, Team and Notes', (tester) async {
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (p.endsWith('/auth/me')) return _json({'Email': 'a@b.c', 'DisplayName': 'Ada', 'TeamSurface': true});
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/agents') || p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/me/notes')) return _json({'notes': [], 'summarize': false});
        return http.Response('', 404);
      }),
    );
    await tester.pumpWidget(await tester.runAsync(() => _app(HomeScreen(api: api, onDisconnect: () {}))) as Widget);
    await _settle(tester);
    final capsule = tester.widget<SpaceCapsule>(find.byType(SpaceCapsule));
    expect([for (final s in capsule.spaces) s.label], ['Team', 'Notizen']);
  });

  testWidgets('a dictated note is saved as a voice note', (tester) async {
    Map<String, Object?>? posted;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        posted = jsonDecode(req.body) as Map<String, Object?>;
        return _json(_note('n2', 'voice', posted!['body'] as String), 201);
      }),
    );
    final dictation = _FakeDictation();
    await tester.pumpWidget(await tester.runAsync(() => _app(NoteEditor(api: api, dictation: dictation))) as Widget);

    await tester.tap(find.text('Diktieren'));
    await tester.pump();
    dictation.hear('Milch und Brot kaufen');
    await tester.pump();
    expect(find.text('Diktat beenden'), findsOneWidget);
    expect(find.text('Milch und Brot kaufen'), findsOneWidget, reason: 'what is heard appears in the field');

    await tester.tap(find.text('Speichern'));
    await _settle(tester);
    expect(posted, {'kind': 'voice', 'title': '', 'body': 'Milch und Brot kaufen', 'duration_seconds': 0});
  });

  testWidgets('a meeting records until it is stopped and is saved with its length', (tester) async {
    Map<String, Object?>? posted;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        posted = jsonDecode(req.body) as Map<String, Object?>;
        return _json(_note('n3', 'meeting', posted!['body'] as String), 201);
      }),
    );
    final dictation = _FakeDictation();
    await tester.pumpWidget(await tester.runAsync(() => _app(MeetingScreen(api: api, dictation: dictation))) as Widget);
    await _settle(tester);
    expect(find.text('Aufnahme läuft'), findsOneWidget);

    dictation.hear('Wir verschieben den Launch auf Oktober.');
    await tester.pump(const Duration(seconds: 3));
    expect(find.text('Wir verschieben den Launch auf Oktober.'), findsOneWidget);

    await tester.tap(find.text('Beenden und speichern'));
    await _settle(tester);
    expect(posted?['kind'], 'meeting');
    expect(posted?['body'], 'Wir verschieben den Launch auf Oktober.');
  });

  testWidgets('summarise is offered for a meeting, and only where the instance can', (tester) async {
    var summarized = false;
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        summarized = req.url.path.endsWith('/summarize');
        return _json(_note('n1', 'meeting', 'x', summary: '## Zusammenfassung\nLaunch im Oktober.'));
      }),
    );
    final meeting = Note.fromJson(_note('n1', 'meeting', 'Wir verschieben den Launch.'));

    await tester.pumpWidget(
      await tester.runAsync(() => _app(NoteScreen(api: api, note: meeting, canSummarize: false))) as Widget,
    );
    expect(find.text('Zusammenfassen'), findsNothing);

    await tester.pumpWidget(
      await tester.runAsync(() => _app(NoteScreen(api: api, note: meeting, canSummarize: true))) as Widget,
    );
    await tester.tap(find.text('Zusammenfassen'));
    await _settle(tester);
    expect(summarized, isTrue);
    expect(find.textContaining('Launch im Oktober.'), findsOneWidget);
  });
}
