import 'dart:math' as math;

import 'mathe.dart';
import 'plan.dart';

// The scene: turns a floor plan into boxes and floor areas (#398).
//
// The web builds a three.js world from a catalogue of a hundred pieces
// (web/src/team/buero/szene.ts). Flutter has no stable 3D, so the app draws
// what carries the office's meaning and leaves the ornament out: the floors,
// the walls cut down on the camera side, a desk per colleague with a screen
// that is lit while the agent works, a room that turns warm when somebody in
// it does. The rules for those — which floor a room gets, which walls are
// cut, which accent a chair takes — are the web's, from the same hashes, so
// a room reads as the same room in both.
//
// Nothing here draws; `zeichner.dart` projects and paints. That keeps the
// scene testable without a canvas.

/// The palette; the values are the web's TAG and NACHT (mal.ts).
class Palette {
  const Palette(this.m);
  final Map<String, int> m;
  int operator [](String k) => m[k]!;
}

const tag = Palette({
  'boden': 0xf4eee3,
  'bodenHell': 0xfae0c6,
  'flur': 0xd9cdb9,
  'gemein': 0xe7e6e0,
  'wand': 0xdcd2c4,
  'grund': 0xbccab0,
  'holz': 0xbd9d72,
  'holzDunkel': 0xa89076,
  'stoff': 0x8b9ab4,
  'metall': 0xb4bac2,
  'schirm': 0x4a4d52,
  'schirmAn': 0xffb268,
  'topf': 0xc07a4e,
  'laub': 0x6ba37c,
  'teppich': 0xcbbca2,
  'papier': 0xf7f4ed,
  'dunkel': 0x5c5751,
  'parkett': 0xe9d9bf,
  'parkettHell': 0xf6d9b2,
  'nadelfilz': 0xdddbd2,
  'nadelfilzHell': 0xf3dcc0,
  'linoleum': 0xe6eadf,
  'linoleumHell': 0xf8e5c8,
  'beton': 0xdcdddb,
  'betonHell': 0xefe0ca,
  'terrazzo': 0xf0ece5,
  'terrazzoHell': 0xfbe6cc,
  'glas': 0xd3e3e8,
  'weiss': 0xf6f5f1,
  'holzHell': 0xdcc39b,
  'akzent1': 0xd95f4a,
  'akzent2': 0xd09a22,
  'akzent3': 0x1f8a82,
  'akzent4': 0x3f8ccb,
  'akzent5': 0x7a57b8,
});

const nacht = Palette({
  'boden': 0x2b2c30,
  'bodenHell': 0x3a3229,
  'flur': 0x222327,
  'gemein': 0x2a2c31,
  'wand': 0x34343a,
  'grund': 0x1d221c,
  'holz': 0x4a4038,
  'holzDunkel': 0x332c26,
  'stoff': 0x3a4152,
  'metall': 0x474c54,
  'schirm': 0x24262a,
  'schirmAn': 0xffb268,
  'topf': 0x5a3f2e,
  'laub': 0x2f5340,
  'teppich': 0x38342c,
  'papier': 0x4a4a48,
  'dunkel': 0x25262a,
  'parkett': 0x2f2b27,
  'parkettHell': 0x41352a,
  'nadelfilz': 0x2a2b2f,
  'nadelfilzHell': 0x3b3229,
  'linoleum': 0x292d2b,
  'linoleumHell': 0x3a3329,
  'beton': 0x2c2d30,
  'betonHell': 0x3b342d,
  'terrazzo': 0x303032,
  'terrazzoHell': 0x3f3629,
  'glas': 0x2d3540,
  'weiss': 0x505154,
  'holzHell': 0x5c5040,
  'akzent1': 0x8a4539,
  'akzent2': 0x86682b,
  'akzent3': 0x1d5e59,
  'akzent4': 0x355f82,
  'akzent5': 0x55437c,
});

const _akzentReihe = ['akzent1', 'akzent2', 'akzent3', 'akzent4', 'akzent5'];

/// A room's accent, from its name — as `akzentFuer` in mal.ts.
int akzentFuer(Palette f, String schluessel) => f[_akzentReihe[hash(schluessel) % _akzentReihe.length]];

int mischen(int a, int b, double t) {
  int k(int s) => (((a >> s) & 0xff) + (((b >> s) & 0xff) - ((a >> s) & 0xff)) * t).round();
  return (k(16) << 16) | (k(8) << 8) | k(0);
}

/// A flat area on the floor (plan x, y are its centre), drawn before any
/// body; `z` only orders the floors among each other (runner over corridor).
class Flaeche {
  const Flaeche(this.x, this.y, this.b, this.t, this.farbe, [this.z = 0]);
  final double x, y, b, t, z;
  final int farbe;
}

/// A box standing on the floor. x, y: the centre of its foot in plan
/// centimetres; b along x, t along y before the turn `dreh`; h the height,
/// `z0` where it starts above the floor. `leuchtet` is drawn without shade:
/// a lit screen.
class Quader {
  const Quader(this.x, this.y, this.b, this.t, this.h, this.farbe, {this.z0 = 0, this.dreh = 0, this.leuchtet = false});
  final double x, y, b, t, h, z0, dreh;
  final int farbe;
  final bool leuchtet;
}

/// What the screen draws over the scene: a face per colleague, a sign per
/// room. Positions in plan centimetres plus a height.
class Figur {
  const Figur(this.agentId, this.x, this.y, this.hoehe);
  final String agentId;
  final double x, y, hoehe;
}

class Schild {
  const Schild(this.raum, this.x, this.y, this.hoehe);
  final Raum raum;
  final double x, y, hoehe;
}

class Szene {
  Szene(this.plan);
  final Plan plan;
  final flaechen = <Flaeche>[];
  final quader = <Quader>[];
  final figuren = <Figur>[];
  final schilder = <Schild>[];

  /// The rooms in which somebody works; they are drawn warm.
  final helle = <String>{};
}

enum Seite { oben, unten, links, rechts }

const Map<Seite, Seite> _gegen = {
  Seite.oben: Seite.unten,
  Seite.unten: Seite.oben,
  Seite.links: Seite.rechts,
  Seite.rechts: Seite.links,
};

/// The start view; mirrors szene.ts and kamera.ts.
const startDrehung = 0.38;
const startNeigung = 0.7;

const double wandVoll = 92, aussenVoll = 120, sockelH = 30;

/// Where a room touches the outer wall.
Map<Seite, bool> amAussen(Raum r, Plan plan) {
  const tol = innen + 2;
  return {
    Seite.oben: (r.y - aussen).abs() <= tol,
    Seite.unten: (r.y + r.h - (plan.hoehe - aussen)).abs() <= tol,
    Seite.links: (r.x - aussen).abs() <= tol,
    Seite.rechts: (r.x + r.w - (plan.breite - aussen)).abs() <= tol,
  };
}

/// The two sides of the building that face the camera at `drehung`,
/// rounded to the nearest quarter view.
Map<Seite, bool> kameraSeiten(double drehung) {
  final k = ((drehung - startDrehung) / (math.pi / 2)).roundToDouble();
  final d = startDrehung + (k * math.pi) / 2;
  final sx = math.sin(d), sy = math.cos(d);
  return {Seite.unten: sy > 0, Seite.oben: sy < 0, Seite.rechts: sx > 0, Seite.links: sx < 0};
}

/// Which sides of a room another room shares; a shared wall is drawn by the
/// room on the left or the band above only.
Map<Seite, bool> geteilt(Raum r, Plan plan) {
  bool neben(Raum q) => !identical(q, r) && (q.y - r.y).abs() < 1;
  return {
    Seite.oben: plan.raeume.any((q) => (q.y + q.h + innen - r.y).abs() < 1),
    Seite.unten: plan.raeume.any((q) => (r.y + r.h + innen - q.y).abs() < 1),
    Seite.links: plan.raeume.any((q) => neben(q) && (q.x + q.w + innen - r.x).abs() < 1),
    Seite.rechts: plan.raeume.any((q) => neben(q) && (r.x + r.w + innen - q.x).abs() < 1),
  };
}

/// How high a room's wall stands on one side: full at the back, a plinth
/// towards the camera — the cutaway, so nothing hides a desk.
double wandHoch(Raum r, Seite seite, Plan plan, double drehung) {
  final cam = kameraSeiten(drehung);
  if (amAussen(r, plan)[seite]!) return cam[seite]! ? sockelH : aussenVoll;
  return cam[seite]! || (geteilt(r, plan)[seite]! && cam[_gegen[seite]]!) ? sockelH : wandVoll;
}

const _bodenArten = ['boden', 'parkett', 'nadelfilz', 'linoleum', 'beton', 'terrazzo'];

/// Builds the scene of one floor. `zustand` is what the data says about an
/// agent; the screen and the room light are the only things it changes.
Szene szeneBauen(
  Plan plan,
  Zustand Function(String agentId) zustand, {
  bool dunkel = false,
  double drehung = startDrehung,
}) {
  final f = dunkel ? nacht : tag;
  final s = Szene(plan);
  final cam = kameraSeiten(drehung);

  // Ground, corridors and the runner on every walking line.
  s.flaechen.add(Flaeche(plan.breite / 2, plan.hoehe / 2, plan.breite + 150, plan.hoehe + 150, f['grund'], -2));
  s.flaechen.add(
    Flaeche(plan.quer.x + plan.quer.w / 2, plan.hoehe / 2, plan.quer.w, plan.hoehe - aussen * 2, f['flur']),
  );
  for (final fl in plan.flure) {
    final b = plan.breite - plan.quer.x - aussen;
    s.flaechen.add(Flaeche(plan.quer.x + b / 2, fl.y + fl.h / 2, b, fl.h, f['flur']));
  }
  final laeufer = mischen(f['akzent3'], f['papier'], 0.5), saum = mischen(laeufer, f['dunkel'], 0.35);
  const lq = 76.0, lf = 56.0;
  s.flaechen.add(Flaeche(plan.quer.mitte, plan.hoehe / 2, lq + 8, plan.hoehe - aussen * 2 - 60, saum, 0.8));
  s.flaechen.add(Flaeche(plan.quer.mitte, plan.hoehe / 2, lq, plan.hoehe - aussen * 2 - 68, laeufer, 1));
  for (final fl in plan.flure) {
    final x0 = plan.quer.mitte, x1 = plan.breite - aussen - 40;
    s.flaechen.add(Flaeche((x0 + x1) / 2, fl.mitte, x1 - x0, lf + 8, saum, 0.8));
    s.flaechen.add(Flaeche((x0 + x1) / 2 + 4, fl.mitte, x1 - x0 - 8, lf, laeufer, 1));
  }

  // The reception at the head of the cross corridor.
  s.quader.add(Quader(plan.quer.mitte, plan.tresenY, math.min(180, plan.quer.w - 14), 60, 100, f['holz']));
  s.quader.add(Quader(plan.quer.mitte, plan.tresenY - 4, math.min(186, plan.quer.w - 8), 68, 4, f['weiss'], z0: 100));

  _aussenwand(s, f, cam);

  for (final r in plan.raeume) {
    final arbeitet = r.leute.any((a) => zustand(a.id) == Zustand.arbeitet);
    if (arbeitet) s.helle.add(r.id);
    final akzent = akzentFuer(f, r.name);

    if (r.gem != null) {
      s.flaechen.add(Flaeche(r.x + r.w / 2, r.y + r.h / 2, r.w, r.h, f['gemein']));
    } else {
      final art = _bodenArten[hash('${r.name}b') % _bodenArten.length];
      s.flaechen.add(Flaeche(r.x + r.w / 2, r.y + r.h / 2, r.w, r.h, f[arbeitet ? '${art}Hell' : art]));
    }

    // Walls with a gap for the door; see szene.ts.
    const t = innen;
    final am = amAussen(r, plan), ge = geteilt(r, plan);
    double hoch(Seite seite) => wandHoch(r, seite, plan, drehung);
    void stueck(double x, double y, double b, double tt, double h) =>
        _wand(s, x + b / 2, y + tt / 2, b, tt, h, f['wand']);
    List<(double, double)> luecke(double x0, double laenge, bool tuer) => tuer
        ? [(x0, r.tuerX - tuerB / 2 - x0), (r.tuerX + tuerB / 2, x0 + laenge - (r.tuerX + tuerB / 2))]
        : [(x0, laenge)];
    if (!am[Seite.oben]! && !ge[Seite.oben]!) {
      for (final (ax, aw) in luecke(r.x - t, r.w + 2 * t, !r.oben)) {
        if (aw > 2) stueck(ax, r.y - t, aw, t, hoch(Seite.oben));
      }
    }
    if (!am[Seite.unten]!) {
      for (final (ax, aw) in luecke(r.x - t, r.w + 2 * t, r.oben)) {
        if (aw > 2) stueck(ax, r.y + r.h, aw, t, hoch(Seite.unten));
      }
    }
    if (!am[Seite.links]! && !ge[Seite.links]!) stueck(r.x - t, r.y, t, r.h, hoch(Seite.links));
    if (!am[Seite.rechts]!) stueck(r.x + r.w, r.y, t, r.h, hoch(Seite.rechts));

    // The department colour as door frame and skirting.
    if (r.gem == null && RegExp(r'^#[0-9a-fA-F]{6}$').hasMatch(r.farbe)) {
      final farbe = mischen(int.parse(r.farbe.substring(1), radix: 16), f['wand'], 0.45);
      final wy = r.oben ? r.y + r.h + innen / 2 : r.y - innen / 2;
      final wandH = hoch(r.oben ? Seite.unten : Seite.oben);
      for (final sx in const [-1, 1]) {
        s.quader.add(Quader(r.tuerX + sx * (tuerB / 2 - 1), wy, 5, innen + 4, wandH + 3, farbe));
      }
      s.quader.add(Quader(r.x + r.w / 2, r.oben ? r.y + 2 : r.y + r.h - 2, r.w - 8, 3, 9, farbe));
    }

    final tw = r.trennwand;
    if (tw != null) {
      final h = tw.glas ? 80.0 : 46.0;
      s.quader.add(Quader(tw.x, (tw.y0 + tw.y1) / 2, 4, tw.y1 - tw.y0, h, tw.glas ? f['glas'] : f['wand']));
    }

    for (var i = 0; i < r.sitze.length; i++) {
      final p = r.sitze[i];
      final agent = i < r.leute.length ? r.leute[i] : null;
      final slug = agent?.slug ?? '${r.name}$i';
      final an = agent != null && zustand(agent.id) == Zustand.arbeitet;
      _arbeitsplatz(s, f, p, an, akzent, slug);
      if (agent != null) s.figuren.add(Figur(agent.id, p.x, p.y, 44));
    }

    switch (r.gem) {
      case Gemein.besprechung:
        _besprechung(s, f, r);
      case Gemein.kueche:
        _kueche(s, f, r);
      case Gemein.lounge:
        _lounge(s, f, r, akzent);
      case null:
        _pflanze(s, f, r);
    }
    s.schilder.add(Schild(r, r.x + r.w / 2, r.y + 30, 60));
  }
  return s;
}

/// A long wall is cut into pieces: the painter orders bodies by their
/// centre, and a wall ten metres long would stand in front of a desk it is
/// behind.
void _wand(Szene s, double cx, double cy, double b, double t, double h, int farbe) {
  const stueck = 90.0;
  if (b >= t) {
    final n = math.max(1, (b / stueck).ceil());
    for (var i = 0; i < n; i++) {
      s.quader.add(Quader(cx - b / 2 + b / n * (i + 0.5), cy, b / n, t, h, farbe));
    }
  } else {
    final n = math.max(1, (t / stueck).ceil());
    for (var i = 0; i < n; i++) {
      s.quader.add(Quader(cx, cy - t / 2 + t / n * (i + 0.5), b, t / n, h, farbe));
    }
  }
}

/// The outer wall: full at the back, a plinth on the camera's sides. The
/// web's windows are left out for now.
void _aussenwand(Szene s, Palette f, Map<Seite, bool> cam) {
  final plan = s.plan;
  const a = aussen;
  final b = plan.breite, h = plan.hoehe;
  double hoch(Seite x) => cam[x]! ? sockelH : aussenVoll;
  _wand(s, b / 2, a / 2, b, a, hoch(Seite.oben), f['wand']);
  _wand(s, b / 2, h - a / 2, b, a, hoch(Seite.unten), f['wand']);
  _wand(s, a / 2, h / 2, a, h - 2 * a, hoch(Seite.links), f['wand']);
  _wand(s, b - a / 2, h / 2, a, h - 2 * a, hoch(Seite.rechts), f['wand']);
}

/// A point of a workplace's own frame, turned by the seat's facing and put
/// on the seat — as three.js turns the group in szene.ts.
(double, double) _lokal(Sitz p, double a, double b) {
  final c = math.cos(p.dreh), s = math.sin(p.dreh);
  return (p.x + a * c - b * s, p.y + a * s + b * c);
}

void _arbeitsplatz(Szene s, Palette f, Sitz p, bool an, int akzent, String slug) {
  void teil(
    double a,
    double b,
    double w,
    double t,
    double h,
    int farbe, {
    double z0 = 0,
    double dreh = 0,
    bool leuchtet = false,
  }) {
    final (x, y) = _lokal(p, a, b);
    s.quader.add(Quader(x, y, w, t, h, farbe, z0: z0, dreh: p.dreh + dreh, leuchtet: leuchtet));
  }

  // The desk: two side panels and the top; forward (−y) the monitor.
  teil(-71, -20, 4, 60, 70, f['metall']);
  teil(71, -20, 4, 60, 70, f['metall']);
  teil(0, -20, 150, 66, 4, f['weiss'], z0: 70);
  teil(0, -44, 8, 6, 12, f['dunkel'], z0: 74);
  teil(0, -44, 58, 4, 34, an ? f['schirmAn'] : f['schirm'], z0: 84, leuchtet: an);
  // The chair, pulled out and twisted — never neatly at the desk.
  final cx = wackel('${slug}sx', 26), cy = 34 + wackel('${slug}sy', 16), cd = wackel('${slug}sd', 0.5);
  final c = math.cos(cd), sn = math.sin(cd);
  (double, double) stuhl(double a, double b) => (cx + a * c - b * sn, cy + a * sn + b * c);
  final (sx, sy) = stuhl(0, 0);
  teil(sx, sy, 8, 8, 40, f['dunkel'], dreh: cd);
  teil(sx, sy, 46, 44, 6, akzent, z0: 40, dreh: cd);
  final (lx, ly) = stuhl(0, 22);
  teil(lx, ly, 44, 6, 44, akzent, z0: 46, dreh: cd);
}

void _besprechung(Szene s, Palette f, Raum r) {
  final cx = r.x + r.w / 2, cy = r.y + schildH + (r.h - schildH) / 2;
  final tb = math.min(r.w - 2 * pad - 80, 220.0), tt = math.min(r.h - schildH - 2 * pad - 80, 100.0);
  s.quader.add(Quader(cx, cy, tb, tt, 72, f['holz']));
  final plaetze = math.max(1, (tb / 70).floor());
  for (var i = 0; i < plaetze; i++) {
    final x = cx - tb / 2 + tb / plaetze * (i + 0.5);
    for (final sy in const [-1, 1]) {
      s.quader.add(Quader(x, cy + sy * (tt / 2 + 22), 40, 38, 44, f['stoff']));
    }
  }
}

void _kueche(Szene s, Palette f, Raum r) {
  // The counter along the wall opposite the door, a table in the middle.
  final y = r.oben ? r.y + schildH + 32 : r.y + r.h - 32;
  s.quader.add(Quader(r.x + r.w / 2, y, r.w - 40, 60, 90, f['weiss']));
  s.quader.add(Quader(r.x + r.w / 2, y, r.w - 40, 62, 4, f['holzHell'], z0: 90));
  s.quader.add(Quader(r.x + r.w / 2, r.y + r.h / 2 + (r.oben ? 30 : -30), 80, 80, 74, f['holz']));
}

void _lounge(Szene s, Palette f, Raum r, int akzent) {
  for (final z in loungeZonen(r)) {
    s.flaechen.add(Flaeche(z.x, z.y, z.b, z.t, mischen(akzent, f['teppich'], 0.6), 0.5));
    final sofaY = z.y + (r.oben ? -z.t / 2 + 40 : z.t / 2 - 40);
    s.quader.add(Quader(z.x, sofaY, math.min(z.b - 40, 200), 70, 40, akzent));
    s.quader.add(Quader(z.x, sofaY + (r.oben ? -28 : 28), math.min(z.b - 40, 200), 14, 72, akzent));
    s.quader.add(Quader(z.x, z.y + (r.oben ? 30 : -30), 90, 50, 40, f['holz']));
    _baum(s, f, z.x + z.b / 2 - 40, z.y, '${r.id}${z.nr}');
  }
}

/// A plant in a corner opposite the door, where the name says so.
void _pflanze(Szene s, Palette f, Raum r) {
  if (hash('${r.name}pf') % 3 == 0) return;
  final links = hash('${r.name}pl') % 2 == 0;
  final x = links ? r.x + 50 : r.x + r.w - 50;
  final y = r.oben ? r.y + schildH + 50 : r.y + r.h - 50;
  _baum(s, f, x, y, r.name);
}

void _baum(Szene s, Palette f, double x, double y, String k) {
  s.quader.add(Quader(x, y, 36, 36, 36, f['topf']));
  final h = 50 + (hash('${k}bh') % 40).toDouble();
  s.quader.add(Quader(x, y, 50, 50, h, f['laub'], z0: 36, dreh: 0.785));
}
