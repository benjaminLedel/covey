import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/face.dart';
import 'package:flutter_test/flutter_test.dart';

// The face as a picture for a notification (#420): a real PNG of the size
// asked for, for a working and for a stopped agent.

void main() {
  testWidgets('the face is drawn as a PNG, working and stopped', (tester) async {
    for (final state in [FaceState.working, FaceState.killed]) {
      final png = (await tester.runAsync(() => facePng('dora-dringlich', state: state, size: 128)))!;
      expect(png.sublist(0, 4), [0x89, 0x50, 0x4E, 0x47], reason: 'PNG signature');
      final codec = (await tester.runAsync(() => ui.instantiateImageCodec(png)))!;
      final frame = (await tester.runAsync(codec.getNextFrame))!;
      expect(frame.image.width, 128);
      // FACE_PNG=<dir> keeps the pictures, to look at while working on them.
      final out = Platform.environment['FACE_PNG'];
      if (out != null) File('$out/face_${state.name}.png').writeAsBytesSync(png);
    }
  });
}
