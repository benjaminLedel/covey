import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/notes.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';

// The notes board (#404): on a phone the statuses are tabs and the chosen
// one's cards stand at full width; on a wide pane the four columns share the
// width. On both a long press offers the statuses to move a card to.

Note _n(String id, String title, String status) =>
    Note.fromJson({'id': id, 'title': title, 'status': status, 'created_at': '2026-09-26T10:00:00Z'});

final _notes = [
  _n('1', 'Angebot schreiben', 'todo'),
  _n('2', 'Rechnung prüfen', 'todo'),
  _n('3', 'Website-Text', 'doing'),
  _n('4', 'Steuer', 'done'),
  _n('5', 'Idee für den Newsletter', ''),
];

Future<void> _pump(
  WidgetTester tester,
  Size size,
  GlobalKey key,
  Future<void> Function(Note, String) onMove, {
  List<Note>? notes,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
  await tester.pumpWidget(
    MaterialApp(
      theme: coveyTheme(Brightness.light),
      builder: (context, child) => StringsScope(strings: strings!, child: child!),
      home: Scaffold(
        body: RepaintBoundary(
          key: key,
          child: SingleChildScrollView(
            child: NotesBoard(notes: notes ?? _notes, onOpen: (_) {}, onMove: onMove),
          ),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> _bild(WidgetTester tester, GlobalKey key, String name) async {
  // BOARD_PNG=<dir> keeps the pictures, to look at while working on the board.
  final out = Platform.environment['BOARD_PNG'];
  if (out == null) return;
  await tester.runAsync(() async {
    final b = key.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final img = await b.toImage(pixelRatio: 1);
    final png = await img.toByteData(format: ui.ImageByteFormat.png);
    File('$out/$name.png').writeAsBytesSync(png!.buffer.asUint8List());
  });
}

void main() {
  testWidgets('on a phone the statuses are tabs, and a long press moves a card', (tester) async {
    final key = GlobalKey();
    (Note, String)? bewegt;
    await _pump(tester, const Size(390, 844), key, (n, st) async => bewegt = (n, st));

    // The first status that has notes is chosen: "Ohne Status" with one.
    expect(find.text('Idee für den Newsletter'), findsOneWidget);
    expect(find.text('Angebot schreiben'), findsNothing);
    await tester.tap(find.text('Offen'));
    await tester.pumpAndSettle();
    expect(find.text('Angebot schreiben'), findsOneWidget);
    expect(find.text('Rechnung prüfen'), findsOneWidget);
    expect(find.text('Steuer'), findsNothing);
    // Nothing scrolls sideways but the row of tabs.
    expect(find.byType(DragTarget<Note>), findsNothing);
    await _bild(tester, key, 'board_phone');

    await tester.longPress(find.text('Rechnung prüfen'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Erledigt').last);
    await tester.pumpAndSettle();
    expect(bewegt?.$1.id, '2');
    expect(bewegt?.$2, 'done');
  });

  testWidgets('a status without notes says so', (tester) async {
    await _pump(
      tester,
      const Size(390, 844),
      GlobalKey(),
      (_, _) async {},
      notes: _notes.where((n) => n.status != 'done').toList(),
    );
    await tester.ensureVisible(find.text('Erledigt'));
    await tester.tap(find.text('Erledigt'));
    await tester.pumpAndSettle();
    expect(find.text('Keine Notizen mit diesem Status.'), findsOneWidget);
    expect(find.text('Angebot schreiben'), findsNothing);
  });

  testWidgets('on a wide pane the four columns share the width', (tester) async {
    final key = GlobalKey();
    await _pump(tester, const Size(1100, 700), key, (_, _) async {});
    expect(find.byType(DragTarget<Note>), findsNWidgets(4));
    for (final t in ['Angebot schreiben', 'Rechnung prüfen', 'Website-Text', 'Steuer', 'Idee für den Newsletter']) {
      expect(find.text(t), findsOneWidget);
    }
    final breiten = tester.widgetList(find.byType(DragTarget<Note>)).map((w) => tester.getSize(find.byWidget(w)).width);
    expect(breiten.toSet().length, 1, reason: 'equal columns');
    expect(breiten.first * 4, greaterThan(1000), reason: 'the columns take the width');
    await _bild(tester, key, 'board_wide');
  });
}
