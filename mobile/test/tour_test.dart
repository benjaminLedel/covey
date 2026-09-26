import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/prefs.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/theme.dart';
import 'package:covey_mobile/tour.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

// The tour (#402): the first time the app sees the team it explains it,
// pointing at the real capsule items, and once it is over it stays away —
// until it is asked for again in the settings.

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

CoveyApi _api() => CoveyApi(
  Uri.parse('https://c.example'),
  'k',
  client: MockClient((req) async {
    final p = req.url.path;
    if (p.endsWith('/auth/me')) {
      return _json({'Email': 'a@b.c', 'DisplayName': 'Ada', 'TeamSurface': true, 'CanWrite': true});
    }
    if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
    if (p.endsWith('/agents') || p.endsWith('/departments')) return _json([]);
    if (p.endsWith('/me/notes')) return _json({'notes': [], 'summarize': false});
    return http.Response('', 404);
  }),
);

Future<void> _pump(WidgetTester tester, GlobalKey key) async {
  await initializeDateFormatting();
  final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
  await tester.pumpWidget(
    RepaintBoundary(
      key: key,
      child: MaterialApp(
        theme: coveyTheme(Brightness.light),
        builder: (context, child) => StringsScope(strings: strings!, child: child!),
        home: HomeScreen(api: _api(), onDisconnect: () {}),
      ),
    ),
  );
  await _settle(tester);
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 10; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

void main() {
  setUp(() async => Prefs.instance.write('tour.team', null));
  tearDown(() async => Prefs.instance.write('tour.team', '1'));

  testWidgets('the first visit gets the tour, pointing at the capsule; then it stays away', (tester) async {
    final key = GlobalKey();
    await _pump(tester, key);

    expect(find.text('Willkommen bei covey'), findsOneWidget);
    expect(find.text('1 von 7'), findsOneWidget);
    await tester.tap(find.text('Weiter'));
    await _settle(tester);
    expect(
      find.text(
        'Tippen Sie auf eine Kollegin, um mit ihr zu sprechen. '
        'Oben steht, wer auf Sie wartet; die Zahl zählt Antworten, die Sie noch nicht gelesen haben.',
      ),
      findsOneWidget,
    );
    // The step points at the team's capsule item, which is on screen.
    expect(TourAnker.rahmen('team'), isNotNull);

    // TOUR_PNG=<dir> keeps the picture, to look at while working on it.
    final out = Platform.environment['TOUR_PNG'];
    if (out != null) {
      await tester.runAsync(() async {
        final b = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
        final img = await b.toImage(pixelRatio: 1);
        final png = await img.toByteData(format: ui.ImageByteFormat.png);
        File('$out/tour_team.png').writeAsBytesSync(png!.buffer.asUint8List());
      });
    }

    for (var i = 0; i < 5; i++) {
      await tester.tap(find.text('Weiter'));
      await _settle(tester);
    }
    expect(find.text('Ihr Konto'), findsOneWidget);
    await tester.tap(find.text("Los geht's"));
    await _settle(tester);
    expect(find.text('Ihr Konto'), findsNothing);
    expect(await tester.runAsync(tourGesehen), isTrue);

    // A second start of the home screen does not bring it back.
    await tester.pumpWidget(const SizedBox());
    await _pump(tester, GlobalKey());
    expect(find.text('Willkommen bei covey'), findsNothing);
  });

  testWidgets('skipping counts as seen, and the settings bring it back', (tester) async {
    await _pump(tester, GlobalKey());
    await tester.tap(find.text('Überspringen'));
    await _settle(tester);
    expect(find.text('Willkommen bei covey'), findsNothing);
    expect(await tester.runAsync(tourGesehen), isTrue);

    await tester.tap(find.byWidgetPredicate((w) => w is TourAnker && w.id == 'person').first);
    await _settle(tester);
    await tester.scrollUntilVisible(find.text('Einführung'), 200);
    await tester.tap(find.text('Einführung'));
    await _settle(tester);
    expect(find.text('Willkommen bei covey'), findsOneWidget);
  });
}
