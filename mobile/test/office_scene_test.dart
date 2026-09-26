import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/office/plan.dart';
import 'package:covey_mobile/office/szene.dart';
import 'package:covey_mobile/office/zeichner.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';

// What the office says about the workforce, it says through the scene (#398):
// a face per colleague, a lit screen per running task, a warm floor where
// somebody works — and nothing else that could be read as information.

Agent _a(String id) =>
    Agent(id: id, slug: id, displayName: id, jobTitle: '', status: '', departmentId: null, killed: false);

Gruppe _g(String name, int n, [String farbe = '']) =>
    Gruppe(id: name, name: name, farbe: farbe, leute: [for (var i = 0; i < n; i++) _a('$name-$i')]);

const _namen = Raumnamen(besprechung: 'Meeting room', kueche: 'Kitchen', lounge: 'Lounge');

final _gruppen = [
  _g('Engineering', 12, '#3f8ccb'),
  _g('Support', 7, '#7a57b8'),
  _g('QA', 5),
  _g('Marketing', 6, '#d95f4a'),
];

void main() {
  final plan = hausBauen(_gruppen, 3200, _namen)[0].plan;
  Zustand arbeitetBei(Set<String> ids, String id) => ids.contains(id) ? Zustand.arbeitet : Zustand.frei;

  test('every colleague gets a face, at the seat the plan gave them', () {
    final s = szeneBauen(plan, (_) => Zustand.frei);
    expect(s.figuren.map((f) => f.agentId).toSet(), {for (final g in _gruppen) ...g.leute.map((a) => a.id)});
    final raum = plan.raeume.firstWhere((r) => r.name == 'QA');
    final f = s.figuren.firstWhere((f) => f.agentId == 'QA-2');
    expect((f.x, f.y), (raum.sitze[2].x, raum.sitze[2].y));
  });

  test('a screen is lit exactly while its agent works, and its room is warm', () {
    final arbeiten = {'Support-3', 'QA-0'};
    final s = szeneBauen(plan, (id) => arbeitetBei(arbeiten, id));
    expect(s.quader.where((q) => q.leuchtet).length, arbeiten.length);
    expect(s.helle, {'Support', 'QA'});
    final ruhig = szeneBauen(plan, (_) => Zustand.schlaeft);
    expect(ruhig.quader.where((q) => q.leuchtet), isEmpty);
    expect(ruhig.helle, isEmpty);
  });

  test('the walls towards the camera are cut down to the plinth', () {
    final s = szeneBauen(plan, (_) => Zustand.frei);
    final wand = tag['wand'];
    // At the start view the camera stands below and to the right of the plan.
    final vorn = s.quader.where((q) => q.farbe == wand && q.y > plan.hoehe - aussen);
    final hinten = s.quader.where((q) => q.farbe == wand && q.y < aussen);
    expect(vorn, isNotEmpty);
    expect(vorn.every((q) => q.h == sockelH), isTrue);
    expect(hinten.every((q) => q.h == aussenVoll), isTrue);
  });

  test('the house paints, by day and by night', () async {
    for (final dunkel in [false, true]) {
      final s = szeneBauen(plan, (id) => arbeitetBei({'Engineering-1', 'Marketing-4'}, id), dunkel: dunkel);
      const k = Kamera();
      final u = k.umriss(s);
      const breite = 1600.0;
      final m = breite / u.width;
      final size = Size(breite, u.height * m);
      final rec = ui.PictureRecorder();
      final canvas = Canvas(rec);
      Zeichner(s, kamera: k, massstab: m, ursprung: -u.topLeft * m, dunkel: dunkel).paint(canvas, size);
      final img = await rec.endRecording().toImage(size.width.ceil(), size.height.ceil());
      // OFFICE_PNG=<dir> keeps the pictures, to look at while working on the scene.
      final out = Platform.environment['OFFICE_PNG'];
      if (out != null) {
        final png = await img.toByteData(format: ui.ImageByteFormat.png);
        File('$out/office_${dunkel ? 'night' : 'day'}.png').writeAsBytesSync(png!.buffer.asUint8List());
      }
    }
  });
}
