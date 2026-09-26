import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/face.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/office_space.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

// The office as a screen (#398): the instance's departments and agents become
// the house, the running tasks light it, and a face is the way into the
// colleague's conversation.

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Map<String, Object?> _agent(String id, String dept, {String status = 'idle'}) => {
  'id': id,
  'slug': id,
  'display_name': id.toUpperCase(),
  'hired_at': '2026-09-01T00:00:00Z',
  'job_title': '',
  'status': status,
  'department_id': dept,
};

CoveyApi _api(int leute) => CoveyApi(
  Uri.parse('https://c.example'),
  'k',
  client: MockClient((req) async {
    final p = req.url.path;
    if (p.endsWith('/departments')) {
      return _json([
        {'id': 'd1', 'name': 'Entwicklung', 'color': '#3f8ccb'},
        {'id': 'd2', 'name': 'Support', 'color': '#d95f4a'},
      ]);
    }
    if (p.endsWith('/agents')) {
      return _json([
        for (var i = 0; i < leute; i++) _agent('a$i', i.isEven ? 'd1' : 'd2', status: i == 3 ? 'sleeping' : 'idle'),
      ]);
    }
    if (p.endsWith('/org/running')) {
      return _json([
        {
          'task_id': 't1',
          'title': 'Fix the login',
          'agent_id': 'a0',
          'agent_name': 'A0',
          'agent_slug': 'a0',
          'since': '2026-09-26T10:00:00Z',
        },
      ]);
    }
    if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
    return http.Response('', 404);
  }),
);

Future<void> _pump(WidgetTester tester, CoveyApi api, void Function(String) onOpen, GlobalKey key) async {
  final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
  await tester.pumpWidget(
    MaterialApp(
      theme: coveyTheme(Brightness.light),
      builder: (context, child) => StringsScope(strings: strings!, child: child!),
      home: RepaintBoundary(
        key: key,
        child: Scaffold(
          body: OfficeSpace(
            api: api,
            me: Me(email: 'a@b.c', displayName: 'Ada', role: 'admin', teamSurface: true),
            onOpen: (id, name, slug, state) => onOpen(id),
          ),
        ),
      ),
    ),
  );
  for (var i = 0; i < 8; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

void main() {
  testWidgets('every colleague sits at a desk, and a tap opens their conversation', (tester) async {
    tester.view.physicalSize = const Size(1170, 2532);
    tester.view.devicePixelRatio = 3;
    addTearDown(tester.view.reset);
    final key = GlobalKey();
    String? geoeffnet;
    await _pump(tester, _api(9), (id) => geoeffnet = id, key);

    expect(find.byType(Face), findsNWidgets(9));
    expect(find.text('Büro'), findsOneWidget);
    // One house, no floor buttons below forty-four seats.
    expect(find.text('EG'), findsNothing);
    // On a phone the rooms are too small for a sign until one zooms in.
    expect(find.text('Entwicklung'), findsNothing);

    await tester.tap(find.bySemanticsLabel(RegExp('^A0 · Fix the login')));
    expect(geoeffnet, 'a0');

    // OFFICE_PNG=<dir> keeps the screen, to look at while working on it.
    final out = Platform.environment['OFFICE_PNG'];
    if (out != null) {
      await tester.runAsync(() async {
        final b = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
        final img = await b.toImage(pixelRatio: 1);
        final png = await img.toByteData(format: ui.ImageByteFormat.png);
        File('$out/office_screen.png').writeAsBytesSync(png!.buffer.asUint8List());
      });
    }
  });

  testWidgets('on a wide window the rooms carry their signs', (tester) async {
    tester.view.physicalSize = const Size(1600, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await _pump(tester, _api(9), (_) {}, GlobalKey());
    expect(find.text('Entwicklung'), findsOneWidget);
    expect(find.text('Support'), findsOneWidget);
  });

  testWidgets('a workforce beyond one floor gets the floor buttons', (tester) async {
    await _pump(tester, _api(60), (_) {}, GlobalKey());
    expect(find.text('EG'), findsOneWidget);
    expect(find.text('1'), findsOneWidget);
  });
}
