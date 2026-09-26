import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/photo.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// A 300x200 picture, left half red, right half blue.
Future<Uint8List> _landscape() async {
  final rec = ui.PictureRecorder();
  final canvas = Canvas(rec);
  canvas.drawRect(const Rect.fromLTWH(0, 0, 150, 200), Paint()..color = const Color(0xFFFF0000));
  canvas.drawRect(const Rect.fromLTWH(150, 0, 150, 200), Paint()..color = const Color(0xFF0000FF));
  final img = await rec.endRecording().toImage(300, 200);
  final data = await img.toByteData(format: ui.ImageByteFormat.png);
  return data!.buffer.asUint8List();
}

void main() {
  testWidgets('the crop screen hands back a square of what the circle shows (#377)', (tester) async {
    final strings = await tester.runAsync(() => Strings.load(const Locale('de')));
    final source = (await tester.runAsync(() async {
      final bytes = await _landscape();
      return (await (await ui.instantiateImageCodec(bytes)).getNextFrame()).image;
    }))!;
    Uint8List? cropped;
    await tester.pumpWidget(
      MaterialApp(
        theme: coveyTheme(Brightness.light),
        builder: (context, child) => StringsScope(strings: strings!, child: child!),
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () async => cropped = await Navigator.of(
              context,
            ).push<Uint8List>(MaterialPageRoute(builder: (_) => PhotoCropScreen(image: source))),
            child: const Text('open'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    expect(find.text('Zuschneiden'), findsOneWidget);

    await tester.runAsync(() async {
      await tester.tap(find.text('Übernehmen'));
      await Future<void>.delayed(const Duration(milliseconds: 300));
    });
    await tester.pumpAndSettle();
    expect(cropped, isNotNull);
    final out = (await tester.runAsync(() async {
      final codec = await ui.instantiateImageCodec(cropped!);
      return (await codec.getNextFrame()).image;
    }))!;
    expect(out.width, PhotoCropScreen.edge);
    expect(out.height, PhotoCropScreen.edge);
    // Unmoved, the circle shows the centred square: red left, blue right.
    final px = (await tester.runAsync(() => out.toByteData()))!;
    int at(int x, int y) => px.getUint32((y * out.width + x) * 4);
    expect(at(20, 256) >> 24, greaterThan(200), reason: 'left is red');
    expect(at(490, 256) & 0xff00, greaterThan(0), reason: 'right is blue');
  });

  testWidgets('without a photo a person is their monogram (#377)', (tester) async {
    expect(PersonPhoto.initials('Ada Lovelace'), 'AL');
    expect(PersonPhoto.initials(''), '');
  });
}
