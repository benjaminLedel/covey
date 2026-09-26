import 'dart:math' as math;

import 'package:flutter/rendering.dart';

import 'szene.dart';

// Draws a scene the way the web's orthographic camera sees it (#398): turned
// by `drehung` around the vertical, tilted by `neigung` (kamera.ts). There is
// no depth buffer on a canvas, so the bodies are painted from the back to the
// front — the painter's algorithm, ordered by each body's centre. That is
// exact for bodies that do not overlap in depth, which is why the walls are
// cut into pieces (szene.dart).

class Kamera {
  const Kamera({this.drehung = startDrehung, this.neigung = startNeigung});
  final double drehung, neigung;

  /// The point on the picture plane, in centimetres: right and down.
  Offset projiziere(double x, double y, double hoehe) {
    final sd = math.sin(drehung), cd = math.cos(drehung);
    final u = x * cd - y * sd;
    final w = x * sd + y * cd;
    return Offset(u, w * math.sin(neigung) - hoehe * math.cos(neigung));
  }

  /// Larger is nearer the camera.
  double tiefe(double x, double y, double hoehe) {
    final w = x * math.sin(drehung) + y * math.cos(drehung);
    return w * math.cos(neigung) + hoehe * math.sin(neigung);
  }

  /// The picture's extent in centimetres: the ground and the walls on it.
  Rect umriss(Szene s) {
    var r = Rect.zero;
    var erst = true;
    void mit(Offset o) {
      r = erst ? Rect.fromPoints(o, o) : r.expandToInclude(Rect.fromPoints(o, o));
      erst = false;
    }

    final p = s.plan;
    for (final (x, y) in [(0.0, 0.0), (p.breite, 0.0), (0.0, p.hoehe), (p.breite, p.hoehe)]) {
      mit(projiziere(x, y, 0));
      mit(projiziere(x, y, aussenVoll));
    }
    return r;
  }
}

class Zeichner extends CustomPainter {
  Zeichner(this.szene, {required this.kamera, required this.massstab, required this.ursprung, required this.dunkel})
    : _ordnung = _ordnen(szene, kamera);

  final Szene szene;
  final Kamera kamera;

  /// Pixels per centimetre, and where the picture's origin lies.
  final double massstab;
  final Offset ursprung;
  final bool dunkel;
  final List<Quader> _ordnung;

  static List<Quader> _ordnen(Szene s, Kamera k) {
    final l = List<Quader>.of(s.quader);
    final t = {for (final q in l) q: k.tiefe(q.x, q.y, q.z0 + q.h / 2)};
    l.sort((a, b) => t[a]!.compareTo(t[b]!));
    return l;
  }

  Offset _p(double x, double y, double h) => ursprung + kamera.projiziere(x, y, h) * massstab;

  @override
  void paint(Canvas canvas, Size size) {
    final pinsel = Paint()..isAntiAlias = true;
    final flaechen = List<Flaeche>.of(szene.flaechen)..sort((a, b) => a.z.compareTo(b.z));
    for (final f in flaechen) {
      final x0 = f.x - f.b / 2, x1 = f.x + f.b / 2, y0 = f.y - f.t / 2, y1 = f.y + f.t / 2;
      canvas.drawPath(
        Path()..addPolygon([_p(x0, y0, 0), _p(x1, y0, 0), _p(x1, y1, 0), _p(x0, y1, 0)], true),
        pinsel..color = Color(0xff000000 | f.farbe),
      );
    }

    final sd = math.sin(kamera.drehung), cd = math.cos(kamera.drehung);
    // The sun stands left behind the camera: the faces turned to it are
    // lighter, the others fall off — as the directional light in szene.ts.
    final ld = kamera.drehung - 0.9;
    final lx = math.sin(ld), ly = math.cos(ld);
    final glanz = Paint()..maskFilter = const MaskFilter.blur(BlurStyle.normal, 6);
    for (final q in _ordnung) {
      final c = math.cos(q.dreh), s = math.sin(q.dreh);
      (double, double) ecke(double a, double b) => (q.x + a * c - b * s, q.y + a * s + b * c);
      final hb = q.b / 2, ht = q.t / 2;
      final fuss = [ecke(-hb, -ht), ecke(hb, -ht), ecke(hb, ht), ecke(-hb, ht)];
      final unten = [for (final (x, y) in fuss) _p(x, y, q.z0)];
      final oben = [for (final (x, y) in fuss) _p(x, y, q.z0 + q.h)];
      final farbe = Color(0xff000000 | q.farbe);
      if (q.leuchtet && dunkel) {
        canvas.drawPath(Path()..addPolygon(oben, true), glanz..color = farbe.withValues(alpha: 0.7));
      }
      // Four sides, normals in the plan: −y, +x, +y, −x in the box's frame.
      final normalen = [(s, -c), (c, s), (-s, c), (-c, -s)];
      for (var i = 0; i < 4; i++) {
        final (nx, ny) = normalen[i];
        if (nx * sd + ny * cd <= 0) continue;
        final j = (i + 1) % 4;
        final licht = q.leuchtet ? 1.0 : 0.74 + 0.16 * (nx * lx + ny * ly);
        canvas.drawPath(
          Path()..addPolygon([unten[i], unten[j], oben[j], oben[i]], true),
          pinsel..color = _schatten(farbe, licht),
        );
      }
      canvas.drawPath(Path()..addPolygon(oben, true), pinsel..color = farbe);
    }
  }

  static Color _schatten(Color c, double k) =>
      Color.from(alpha: 1, red: (c.r * k).clamp(0, 1), green: (c.g * k).clamp(0, 1), blue: (c.b * k).clamp(0, 1));

  @override
  bool shouldRepaint(Zeichner old) =>
      !identical(old.szene, szene) ||
      old.kamera.drehung != kamera.drehung ||
      old.massstab != massstab ||
      old.ursprung != ursprung ||
      old.dunkel != dunkel;
}
