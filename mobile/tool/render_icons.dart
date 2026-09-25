// Renders the launcher icons from the app's own MarkPainter (#335), so the
// mark on the home screen and the mark in the app are one drawing:
//
//   cd mobile && flutter test tool/render_icons.dart
//   dart run flutter_launcher_icons
//
// Run under `flutter test` because painting needs the engine; it is a tool,
// not a test, and lives outside test/ so `flutter test` alone never runs it.
import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/mark.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';

const _size = 1024.0;

Future<void> _write(String name, void Function(Canvas canvas) paint) async {
  final recorder = ui.PictureRecorder();
  paint(Canvas(recorder));
  final image = await recorder.endRecording().toImage(_size.toInt(), _size.toInt());
  final png = await image.toByteData(format: ui.ImageByteFormat.png);
  File('assets/icon/$name').writeAsBytesSync(png!.buffer.asUint8List());
}

/// The mark painted into [rect] of the canvas.
void _mark(Canvas canvas, Rect rect, MarkPainter painter) {
  canvas.save();
  canvas.translate(rect.left, rect.top);
  painter.paint(canvas, rect.size);
  canvas.restore();
}

void main() {
  test('render launcher icons', () async {
    await TestWidgetsFlutterBinding.ensureInitialized().runAsync(() async {
      const full = Rect.fromLTWH(0, 0, _size, _size);

      // iOS and the Android legacy icon: full bleed, square corners — the
      // system masks it.
      await _write('icon.png', (c) => _mark(c, full, MarkPainter(radius: 0)));

      // Android adaptive foreground: the birds alone, the mark scaled to 78 %
      // so the widest bird stays inside the 66 % safe zone of the mask. The
      // background layer is the clay colour, set in pubspec.yaml.
      final inner = Rect.fromCenter(center: full.center, width: _size * 0.78, height: _size * 0.78);
      await _write('foreground.png', (c) => _mark(c, inner, MarkPainter(tile: 0)));

      // macOS and Windows (#334): the rounded tile with a margin, the way a
      // desktop icon is drawn — macOS does not mask it, so the corners are
      // the mark's own. 824 of 1024 is Apple's grid for the icon body.
      final body = Rect.fromCenter(center: full.center, width: 824, height: 824);
      await _write('icon-desktop.png', (c) => _mark(c, body, MarkPainter()));
    });
  });
}
