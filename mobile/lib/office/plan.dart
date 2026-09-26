import 'dart:math' as math;

import '../models.dart';
import 'mathe.dart';

// The floor plan of the office, ported from web/src/team/buero/plan.ts and
// typen.ts (#398).
//
// Only geometry lives here: how a workforce becomes a building. Rooms,
// corridors and seats follow from the head count and from hashes of the
// department names — nothing is drawn by hand, and nothing is random. That
// is what lets the app build the same house as the web without asking it:
// the same departments give the same rooms, the same desks in the same
// arrangement. test/office_plan_test.dart compares this file with the web's
// numbers; a change to the plan is made in both, and the fixture renewed.
//
// Measures are CENTIMETRES. The names stay the web's, so the two files can be
// read side by side.

class Punkt {
  const Punkt(this.x, this.y);
  final double x, y;
}

/// A seat at a desk: the point the figure stands on, and the facing in
/// radians; the furniture is rotated around it as a group.
class Sitz {
  const Sitz(this.x, this.y, this.dreh);
  final double x, y, dreh;
}

enum Gemein { besprechung, kueche, lounge }

/// What the office may know about a colleague: it comes from the data, always.
enum Zustand { schlaeft, arbeitet, frei, wartet, gestoppt }

/// A department with its people — what the screen hands to the plan.
class Gruppe {
  const Gruppe({required this.id, required this.name, required this.farbe, required this.leute});
  final String id, name;

  /// A CSS hex like "#6d8c5a", or "".
  final String farbe;
  final List<Agent> leute;
}

/// The names of the common rooms, translated by the screen.
class Raumnamen {
  const Raumnamen({required this.besprechung, required this.kueche, required this.lounge});
  final String besprechung, kueche, lounge;
}

class Trennwand {
  const Trennwand(this.x, this.y0, this.y1, this.glas);
  final double x, y0, y1;

  /// Glass, or a wall at half height.
  final bool glas;
}

/// A room in the plan; the fields are described in typen.ts.
class Raum {
  Raum({
    required this.id,
    required this.name,
    required this.farbe,
    this.gem,
    this.variante,
    required this.leute,
    required this.x,
    required this.y,
    required this.w,
    required this.h,
    required this.spalten,
    required this.zeilen,
    required this.oben,
    required this.flur,
    required this.tuerX,
    required this.ri,
  });

  final String id, name, farbe;
  final Gemein? gem;
  final int? variante;
  final List<Agent> leute;
  List<Sitz> sitze = const [];
  final double x, y, w, h;
  final int spalten, zeilen;
  final bool oben;
  final int flur;
  double flurY = 0;
  int nr = 0;
  final double tuerX;
  double wand = 0;
  final int ri;
  Punkt innen = const Punkt(0, 0), aussen = const Punkt(0, 0);
  List<Punkt> treff = const [];
  Trennwand? trennwand;
}

class Flur {
  const Flur(this.y, this.h, this.mitte);
  final double y, h, mitte;
}

class Quer {
  const Quer(this.x, this.w, this.mitte);
  final double x, w, mitte;
}

class Plan {
  const Plan({
    required this.breite,
    required this.hoehe,
    required this.raeume,
    required this.flure,
    required this.quer,
    required this.tresenY,
    required this.plaetze,
    required this.treppe,
  });

  final double breite, hoehe;
  final List<Raum> raeume;
  final List<Flur> flure;

  /// The cross corridor on the left that joins every corridor to the entrance.
  final Quer quer;

  /// The front desk at the head of the cross corridor and the queue before it.
  final double tresenY;
  final List<Punkt> plaetze;

  /// The stairwell at the end of the cross corridor; used only when the
  /// house has more than one floor.
  final Punkt treppe;
}

class Etage {
  const Etage(this.nr, this.gruppen, this.plan);
  final int nr;
  final List<Gruppe> gruppen;
  final Plan plan;
}

const double sitzB = 245, sitzH = 265, podLuft = 40, pad = 40, padOben = 56, schildH = 10;
const double aussen = 12, innen = 10, flurH = 170, querB = 220, tuerB = 60;

/// Rooms stay roughly square: twelve seats in one row are a tube.
int spaltenFuer(int n) => math.max(1, math.min(5, math.min(n, jsRound(math.sqrt(n * 1.3)))));
double podBreite(int s) => s * sitzB + ((s / 2).ceil() - 1) * podLuft;
double zimmerBreite(int n) => math.max(430, podBreite(spaltenFuer(n)) + pad * 2);
double zimmerHoehe(int n) => schildH + padOben + pad + ((n / spaltenFuer(n)).ceil() - 1) * sitzH + 215;

/// What a seat occupies on the floor, in its own frame: the desk 150 wide,
/// the monitor forward to −50, the chair and the way to stand up back to +75.
const List<(double, double)> _fuss = [(-75, -50), (75, -50), (75, 75), (-75, 75)];
const double _rand = 20;

class Kasten {
  const Kasten(this.x0, this.y0, this.x1, this.y1);
  final double x0, y0, x1, y1;
}

Kasten sitzFuss(Sitz p) {
  final c = math.cos(p.dreh), s = math.sin(p.dreh);
  var x0 = double.infinity, y0 = double.infinity, x1 = double.negativeInfinity, y1 = double.negativeInfinity;
  for (final (a, b) in _fuss) {
    final px = p.x + a * c - b * s, py = p.y + a * s + b * c;
    x0 = math.min(x0, px);
    x1 = math.max(x1, px);
    y0 = math.min(y0, py);
    y1 = math.max(y1, py);
  }
  return Kasten(x0, y0, x1, y1);
}

/// The area the scene keeps free in front of the door.
Kasten tuerZone(double x, double y, double w, double h, bool oben, double tuerX) =>
    Kasten(tuerX - tuerB / 2 - 10, oben ? y + h - 80 : y - 10, tuerX + tuerB / 2 + 10, oben ? y + h + 10 : y + 80);

/// A seat before placement; `weit` scales its scatter.
class _Roh {
  const _Roh(this.x, this.y, this.dreh, [this.weit = 1]);
  final double x, y, dreh, weit;
}

/// Ten arrangements; the department's name picks one among those that fit
/// the room. See plan.ts for why.
List<Sitz> sitzMuster(
  String name,
  double x,
  double y,
  double w,
  int sp,
  int n, {
  double? h,
  bool oben = true,
  double? tuerX,
}) {
  if (n == 0) return [];
  final hh = h ?? zimmerHoehe(n);
  final tx = tuerX ?? x + w / 2;
  final tuerOben = !oben;
  const pi = math.pi;
  Sitz streu(int i, double px, double py, double dreh, [double weit = 1]) => Sitz(
    px + wackel('${name}x$i', 20 * weit),
    py + wackel('${name}y$i', 20 * weit),
    dreh + wackel('${name}d$i', 0.17 * weit),
  );
  final t = y + schildH, b = y + hh, l = x, r = x + w;
  final tuer = tuerZone(x, y, w, hh, oben, tx);
  bool schneidet(Kasten a, Kasten b) => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;

  bool taugt(List<Sitz>? liste) {
    if (liste == null || liste.length != n) return false;
    final f = liste.map(sitzFuss).toList();
    for (final k in f) {
      if (k.x0 < l + _rand || k.x1 > r - _rand || k.y0 < t + _rand || k.y1 > b - _rand) return false;
      if (schneidet(k, tuer)) return false;
    }
    for (var i = 0; i < n; i++) {
      for (var j = i + 1; j < n; j++) {
        final dx = liste[i].x - liste[j].x, dy = liste[i].y - liste[j].y;
        if (math.sqrt(dx * dx + dy * dy) < 125) return false;
        final a = f[i], c = f[j];
        if (math.min(a.x1, c.x1) - math.max(a.x0, c.x0) > 10 && math.min(a.y1, c.y1) - math.max(a.y0, c.y0) > 10) {
          return false;
        }
      }
    }
    return true;
  }

  List<Sitz> setzen(List<_Roh> roh) {
    var x0 = double.infinity, y0 = double.infinity, x1 = double.negativeInfinity, y1 = double.negativeInfinity;
    for (final p in roh) {
      final k = sitzFuss(Sitz(p.x, p.y, p.dreh));
      x0 = math.min(x0, k.x0);
      x1 = math.max(x1, k.x1);
      y0 = math.min(y0, k.y0);
      y1 = math.max(y1, k.y1);
    }
    final oy0 = t + _rand + 30 + (tuerOben ? 90 : 0), oy1 = b - _rand - 30 - (tuerOben ? 0 : 90);
    final restX = math.max<double>(0, r - l - 2 * _rand - 60 - (x1 - x0));
    final restY = math.max<double>(0, oy1 - oy0 - (y1 - y0));
    final dx = l + _rand + 30 + restX / 2 + wackel('${name}v', restX * 0.3) - x0;
    final dy = (tuerOben ? oy1 - restY / 3 - (y1 - y0) : oy0 + restY / 3) - y0;
    return [for (var i = 0; i < roh.length; i++) streu(i, roh[i].x + dx, roh[i].y + dy, roh[i].dreh, roh[i].weit)];
  }

  double podX(int sp_, int i) => ((i % sp_) ~/ 2) * (2 * sitzB + podLuft) + ((i % sp_) % 2) * sitzB;
  const tisch = 165.0; // centre distance of two desks standing edge to edge
  const gegen = 136.0; // two seats facing each other across the monitors

  final muster = <List<Sitz>? Function()>[
    // reihen: rows, all facing the same way.
    () => setzen([for (var i = 0; i < n; i++) _Roh(podX(sp, i), (i ~/ sp) * sitzH, 0)]),
    // gegenueber: every two rows share an edge and look at each other.
    () => setzen([
      for (var i = 0; i < n; i++)
        _Roh(podX(sp, i), ((i ~/ sp) ~/ 2) * (gegen + 260) + ((i ~/ sp) % 2) * gegen, (i ~/ sp) % 2 != 0 ? 0 : pi, 0.5),
    ]),
    // inseln: four desks to a block, two facing two.
    () {
      final inseln = (n / 4).ceil();
      final jeZeile = math.max(1, math.min(inseln, ((w - 2 * _rand - 60 - 306) / 416).floor() + 1));
      return setzen([
        for (var i = 0; i < n; i++)
          _Roh(
            ((i ~/ 4) % jeZeile) * 416 + ((i % 4) % 2) * 156,
            ((i ~/ 4) ~/ jeZeile) * 400 + ((i % 4) >> 1) * gegen,
            (i % 4) >> 1 != 0 ? 0 : pi,
            0.3,
          ),
      ]);
    },
    // versetzt: every second row shifted by half a seat.
    () => setzen([
      for (var i = 0; i < n; i++) _Roh(podX(sp, i) + ((i ~/ sp) % 2 != 0 ? sitzB * 0.4 : 0), (i ~/ sp) * sitzH, 0),
    ]),
    // hufeisen: along three walls, the open side towards the door.
    () {
      if (n < 5 || w < 820) return null;
      final hy = oben ? t + _rand + 90 : b - _rand - 90;
      final capB = ((w - 500) / tisch).floor() + 1;
      final capS = ((hh - schildH - 2 * (_rand + 90) - 2 * tisch) / tisch).floor() + 1;
      if (capS < 1) return null;
      var hinten = math.min(capB, math.max(1, jsRound(n / 3)));
      var seite = n - hinten;
      if ((seite / 2).ceil() > capS) {
        hinten = n - 2 * capS;
        seite = 2 * capS;
      }
      if (hinten > capB || hinten < 1) return null;
      final liste = <Sitz>[
        for (var k = 0; k < hinten; k++) Sitz(x + w / 2 + (k - (hinten - 1) / 2) * tisch, hy, oben ? pi : 0),
        for (var k = 0; k < seite; k++)
          Sitz(
            k % 2 == 0 ? l + _rand + 90 : r - _rand - 90,
            hy + (oben ? 1 : -1) * ((k ~/ 2) + 1) * tisch,
            k % 2 == 0 ? pi / 2 : -pi / 2,
          ),
      ];
      return [for (var i = 0; i < liste.length; i++) streu(i, liste[i].x, liste[i].y, liste[i].dreh, 0.4)];
    },
    // fenster: everyone against the wall opposite the door.
    () {
      if (n < 2) return null;
      final cap = ((w - 2 * _rand - 44 - 150) / tisch).floor() + 1;
      if (n > cap) return null;
      final fy = oben ? t + _rand + 72 : b - _rand - 72;
      return [for (var i = 0; i < n; i++) streu(i, x + w / 2 + (i - (n - 1) / 2) * tisch, fy, oben ? 0 : pi, 0.4)];
    },
    // gedreht: the whole grid tilted by a third of a right angle.
    () {
      final a = (hash('${name}w') % 2 != 0 ? 1 : -1) * (0.42 + (hash('${name}w') % 17) / 100);
      final c = math.cos(a), s = math.sin(a);
      for (var spalten = math.min(n, 5); spalten >= 1; spalten--) {
        final liste = setzen([
          for (var i = 0; i < n; i++)
            _Roh(
              (i % spalten) * sitzB * c - (i ~/ spalten) * sitzH * s,
              (i % spalten) * sitzB * s + (i ~/ spalten) * sitzH * c,
              a,
              0.5,
            ),
        ]);
        if (taugt(liste)) return liste;
      }
      return null;
    },
    // baenke: one row along each long wall, the aisle in the middle.
    () {
      if (n < 4 || hh < 520) return null;
      final hy = oben ? t + _rand + 72 : b - _rand - 72, hd = oben ? 0.0 : pi;
      final vy = oben ? b - _rand - 72 : t + _rand + 72, vd = oben ? pi : 0.0;
      final capB = ((w - 2 * _rand - 44 - 150) / tisch).floor() + 1;
      final slots = <double>[];
      for (var sx = l + _rand + 22 + 75; sx <= r - _rand - 22 - 75; sx += tisch) {
        if (sx + 75 < tuer.x0 - 25 || sx - 75 > tuer.x1 + 25) slots.add(sx);
      }
      stabilSortieren<double>(slots, (p, q) => vorzeichen((p - tx).abs() - (q - tx).abs()));
      var hinten = math.min(capB, (n / 2).ceil()), vorn = n - hinten;
      if (vorn > slots.length) {
        vorn = slots.length;
        hinten = n - vorn;
      }
      if (hinten > capB) return null;
      final vorne = slots.sublist(0, vorn);
      stabilSortieren<double>(vorne, (p, q) => vorzeichen(p - q));
      final liste = <Sitz>[
        for (var k = 0; k < hinten; k++) Sitz(x + w / 2 + (k - (hinten - 1) / 2) * tisch, hy, hd),
        for (final sx in vorne) Sitz(sx, vy, vd),
      ];
      return [for (var i = 0; i < liste.length; i++) streu(i, liste[i].x, liste[i].y, liste[i].dreh, 0.4)];
    },
    // tafel: one long table, with an odd count one at the head.
    () {
      if (n < 3 || n > 8) return null;
      final kopf = n % 2, seite = (n - kopf) ~/ 2;
      final liste = <_Roh>[
        for (var k = 0; k < seite; k++) ...[_Roh(k * tisch, 0, pi, 0.3), _Roh(k * tisch, gegen, 0, 0.3)],
        if (kopf != 0) const _Roh(-140, gegen / 2, pi / 2, 0.3),
      ];
      return setzen(liste);
    },
    // winkel: two desks at a time at a right angle.
    () {
      final paare = (n / 2).ceil();
      final jeZeile = math.max(1, math.min(paare, ((w - 2 * _rand - 60 - 280) / 390).floor() + 1));
      return setzen([
        for (var i = 0; i < n; i++)
          () {
            final pr = i ~/ 2, k = i % 2;
            final gx = (pr % jeZeile) * 390.0, gy = (pr ~/ jeZeile) * 280.0;
            final spiegel = hash('${name}l$pr') % 2 == 1;
            if (!spiegel) return k != 0 ? _Roh(gx + 130, gy + 35, -pi / 2, 0.3) : _Roh(gx, gy, 0, 0.3);
            return k != 0 ? _Roh(gx, gy + 35, pi / 2, 0.3) : _Roh(gx + 130, gy, 0, 0.3);
          }(),
      ]);
    },
  ];

  final passend = <List<Sitz>>[];
  for (final m in muster) {
    final liste = m();
    if (taugt(liste)) passend.add(liste!);
  }
  if (passend.isEmpty) {
    return setzen([for (var i = 0; i < n; i++) _Roh(podX(sp, i), (i ~/ sp) * sitzH, 0, 0.2)]);
  }
  return passend[hash('${name}m') % passend.length];
}

/// One cell of a band before it is laid: a department or a common room.
class _Zelle {
  _Zelle({
    this.gruppe,
    this.gem,
    required this.name,
    required this.schluessel,
    required this.w,
    required this.h,
    this.max,
  });
  final Gruppe? gruppe;
  final Gemein? gem;
  final String name, schluessel;
  double w;
  final double h;
  final double? max;

  _Zelle kopie() => _Zelle(gruppe: gruppe, gem: gem, name: name, schluessel: schluessel, w: w, h: h, max: max);
}

/// A lounge island: centre, size and number; `treffY` is the line in front
/// of it, on the door side, where people meet.
class LoungeZone {
  const LoungeZone(this.nr, this.x, this.y, this.b, this.t, this.treffY);
  final int nr;
  final double x, y, b, t, treffY;
}

/// Lays out one floor; see `bauplan` in plan.ts.
Plan bauplan(List<Gruppe> gruppen, double maxB, Raumnamen namen, {double dichte = 1, bool mitTreppe = false}) {
  final zellen = <_Zelle>[
    for (final g in gruppen)
      () {
        final n = g.leute.length, soll = zimmerBreite(n);
        final w = math.max(
          math.max(430.0, podBreite(spaltenFuer(n)) + pad),
          jsRound(soll * (1 + wackel('${g.name}b', 0.12))).toDouble(),
        );
        return _Zelle(gruppe: g, name: g.name, schluessel: g.name, w: w, h: zimmerHoehe(n), max: soll * 1.25);
      }(),
    _Zelle(gem: Gemein.besprechung, name: namen.besprechung, schluessel: 'besprechung', w: 280, h: 230),
    _Zelle(gem: Gemein.kueche, name: namen.kueche, schluessel: 'kueche', w: 230, h: 230),
  ];
  final innenB = math.max(900.0, maxB - aussen * 2 - querB - innen);
  final ges = zellen.fold<double>(0, (s, z) => s + z.w + innen) - innen;
  final bz = math.max(1, (ges / innenB).ceil());
  final soll = ges / bz;
  var baender = <List<_Zelle>>[];
  var band = <_Zelle>[];
  var breit = 0.0;
  for (final z in zellen) {
    if (band.isNotEmpty && (breit + z.w > innenB || (breit >= soll && baender.length < bz - 1))) {
      baender.add(band);
      band = [];
      breit = 0;
    }
    band.add(z);
    breit += z.w + innen;
  }
  if (band.isNotEmpty) baender.add(band);
  baender = [for (final b in baender) _zeileFuellen(b, innenB, namen.lounge)];
  const x0 = aussen + querB + innen;
  final raeume = <Raum>[];
  final flure = <Flur>[];
  var y = aussen;
  var lounges = 0;
  for (var gi = 0; gi < baender.length; gi += 2) {
    final paar = [baender[gi], if (gi + 1 < baender.length) baender[gi + 1]];
    final fi = flure.length;
    void legen(List<_Zelle> b, double hoehe, bool oben) {
      var x = x0;
      for (final z in b) {
        final w = z.w, g = z.gruppe;
        final n = g?.leute.length ?? 0, sp = g != null ? spaltenFuer(n) : 1;
        final variante = z.gem == Gemein.lounge ? lounges++ : null;
        final r = Raum(
          id: g != null ? g.id : (variante != null ? 'lounge-$variante' : (z.gem?.name ?? '')),
          name: z.name,
          farbe: g?.farbe ?? '',
          gem: z.gem,
          variante: variante,
          leute: g?.leute ?? const [],
          x: x,
          y: y,
          w: w,
          h: hoehe,
          spalten: sp,
          zeilen: n != 0 ? (n / sp).ceil() : 0,
          oben: oben,
          flur: fi,
          tuerX: x + w / 2,
          ri: oben ? 1 : -1,
        );
        r.sitze = sitzMuster(g?.name ?? '', x, y, w, sp, n, h: hoehe, oben: oben, tuerX: r.tuerX);
        if (n >= 10) _trennwandSuchen(r);
        raeume.add(r);
        x += w + innen;
      }
      y += hoehe;
    }

    legen(paar[0], paar[0].map((z) => z.h).reduce(math.max), true);
    flure.add(Flur(y + innen, flurH, y + innen + flurH / 2));
    y += innen + flurH + innen;
    if (paar.length > 1) {
      legen(paar[1], paar[1].map((z) => z.h).reduce(math.max), false);
      y += innen;
    }
  }
  final gebaut = y - innen + aussen, breite = aussen * 2 + querB + innen + innenB;
  const quer = Quer(aussen, querB, aussen + querB / 2);
  final zaehler = <int, int>{};
  for (final r in raeume) {
    final f = flure[r.flur];
    r.wand = r.oben ? r.y + r.h : r.y;
    r.flurY = f.mitte;
    r.innen = Punkt(r.tuerX, r.wand - r.ri * 26);
    r.aussen = Punkt(r.tuerX, r.wand + r.ri * (innen + 26));
    zaehler[r.flur] = (zaehler[r.flur] ?? 0) + 1;
    r.nr = zaehler[r.flur]!;
    if (r.gem == Gemein.lounge) {
      r.treff = [
        for (final z in loungeZonen(r, dichte: dichte))
          for (final sx in const [-1, 1]) Punkt(z.x + sx * math.min(60, z.b / 4), z.treffY),
      ];
    } else if (r.gem != null) {
      final cx = r.x + r.w / 2, cy = r.y + schildH + (r.h - schildH) / 2;
      r.treff = [
        Punkt(cx - 70, cy + 40),
        Punkt(cx + 70, cy + 40),
        Punkt(cx, cy + 52),
        Punkt(cx - 70, cy - 40),
        Punkt(cx + 70, cy - 40),
      ];
    }
  }
  const ey = aussen + 60;
  final plaetze = [for (var i = 0; i < 5; i++) Punkt(quer.mitte, ey + 230 + i * 56)];
  final hoehe = mitTreppe ? math.max(gebaut, plaetze.last.y + 200 + 108 + aussen) : gebaut;
  return Plan(
    breite: breite,
    hoehe: hoehe,
    flure: flure,
    quer: quer,
    raeume: raeume,
    tresenY: ey + 130,
    plaetze: plaetze,
    treppe: Punkt(quer.mitte, hoehe - aussen - 96),
  );
}

/// One floor carries a good forty seats; see plan.ts.
const etagePlaetze = 44;

List<List<Gruppe>> etagenTeilen(List<Gruppe> gruppen) {
  final etagen = <List<Gruppe>>[];
  var band = <Gruppe>[];
  var voll = 0;
  for (final g in gruppen) {
    final n = g.leute.length;
    if (band.isNotEmpty && voll + n > etagePlaetze) {
      etagen.add(band);
      band = [];
      voll = 0;
    }
    band.add(g);
    voll += n;
  }
  if (band.isNotEmpty || etagen.isEmpty) etagen.add(band);
  return etagen;
}

/// One plan per floor; `nr` is the floor's index, 0 at the entrance.
List<Etage> hausBauen(List<Gruppe> gruppen, double maxB, Raumnamen namen, {double dichte = 1}) {
  final teile = etagenTeilen(gruppen), treppe = teile.length > 1;
  return [
    for (var nr = 0; nr < teile.length; nr++)
      Etage(nr, teile[nr], bauplan(teile[nr], maxB, namen, dichte: dichte, mitTreppe: treppe)),
  ];
}

/// The partition in large rooms; see plan.ts.
void _trennwandSuchen(Raum r) {
  final fuss = r.sitze.map(sitzFuss).toList();
  stabilSortieren<Kasten>(fuss, (a, b) => vorzeichen(a.x0 - b.x0));
  final gassen = <double>[];
  var rechts = r.x + _rand;
  for (final k in fuss) {
    if (k.x0 - rechts >= 40) gassen.add((rechts + k.x0) / 2);
    rechts = math.max(rechts, k.x1);
  }
  final mitte = r.x + r.w / 2;
  final kandidaten = gassen
      .where((gx) => gx > r.x + r.w * 0.25 && gx < r.x + r.w * 0.75 && (gx - r.tuerX).abs() > 60)
      .toList();
  stabilSortieren<double>(kandidaten, (a, b) => vorzeichen((a - mitte).abs() - (b - mitte).abs()));
  if (kandidaten.isEmpty) return;
  final gx = kandidaten.first;
  final hinten = r.oben ? r.y + schildH : r.y + r.h, ri = r.oben ? 1 : -1;
  final tiefe = r.h - schildH;
  final tuerY = r.oben ? r.y + r.h - 26 : r.y + 26;
  var laenge = tiefe * (0.5 + (hash('${r.name}t') % 20) / 100);
  for (final p in r.sitze) {
    if ((p.x - gx) * (r.tuerX - gx) >= 0) continue;
    final t = (gx - r.tuerX) / (p.x - r.tuerX), kreuz = tuerY + t * (p.y - tuerY);
    laenge = math.min(laenge, (kreuz - hinten) * ri - 50);
  }
  if (laenge < tiefe * 0.35) return;
  final ende = hinten + ri * laenge;
  r.trennwand = Trennwand(gx, math.min(hinten, ende), math.max(hinten, ende), hash('${r.name}g') % 3 != 0);
}

const double _loungeMin = 380, _loungeMax = 1800, _loungeTiefe = 420;

/// What a row leaves over; see `zeileFuellen` in plan.ts.
List<_Zelle> _zeileFuellen(List<_Zelle> b0, double innenB, String loungeName) {
  final b = [for (final z in b0) z.kopie()];
  double roh() => b.fold<double>(0, (s, z) => s + z.w + innen) - innen;
  var rest = innenB - roh();
  if (rest >= _loungeMin) {
    for (final z in b) {
      if (z.gem != null) {
        final plus = math.min(120.0, rest * 0.1);
        z.w += plus;
        rest -= plus;
      }
    }
  }
  if (rest >= _loungeMin) {
    final zahl = math.min((rest / (_loungeMax + innen)).ceil(), b.length + 1);
    final lw = rest / zahl - innen;
    final schluessel = b.map((z) => z.schluessel).join('|');
    for (var k = 0; k < zahl; k++) {
      final frei = <int>[];
      for (var i = 0; i <= b.length; i++) {
        final vor = i > 0 ? b[i - 1].gem : null, an = i < b.length ? b[i].gem : null;
        if (vor != Gemein.lounge && an != Gemein.lounge) frei.add(i);
      }
      if (frei.isEmpty) break;
      b.insert(
        frei[hash('${schluessel}lo$k') % frei.length],
        _Zelle(gem: Gemein.lounge, name: loungeName, schluessel: 'lounge', w: lw, h: _loungeTiefe),
      );
    }
    return b;
  }
  for (var runde = 0; runde < 20 && rest > 0.5; runde++) {
    final offen = b.where((z) => z.max == null || z.w < z.max! - 0.5).toList();
    final nehmer = offen.isNotEmpty ? offen : b;
    final teil = rest / nehmer.length;
    for (final z in nehmer) {
      final plus = offen.isNotEmpty && z.max != null ? math.min(teil, z.max! - z.w) : teil;
      z.w += plus;
      rest -= plus;
    }
  }
  return b;
}

/// The islands of a lounge; see plan.ts.
List<LoungeZone> loungeZonen(Raum r, {double dichte = 1}) {
  final iw = r.w - pad * 2, ih = r.h - schildH - pad * 2;
  final t = math.max(120.0, math.min(ih - 90, 380.0));
  final zahl = math.max(1, math.min(4, ((iw * math.min(1, dichte)) / 360).floor()));
  final zb = iw / zahl;
  final cy = r.y + schildH + pad + ih / 2 + (r.oben ? -30 : 30);
  return [
    for (var j = 0; j < zahl; j++)
      LoungeZone(j, r.x + pad + zb * (j + 0.5), cy, zb - 30, t, cy + (r.oben ? 1 : -1) * (t / 2 + 30)),
  ];
}

int? raumAn(Plan plan, Punkt p) {
  for (var i = 0; i < plan.raeume.length; i++) {
    final r = plan.raeume[i];
    if (p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h) return i;
  }
  return null;
}
