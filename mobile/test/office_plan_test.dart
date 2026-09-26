import 'dart:convert';
import 'dart:io';

import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/office/mathe.dart';
import 'package:covey_mobile/office/plan.dart';
import 'package:flutter_test/flutter_test.dart';

// The app builds the same office as the web (#398): the fixture is what
// web/src/team/buero/plan.ts computes for a handful of workforces
// (tool/office/fixture.ts writes it), and the port has to land on the same
// numbers — the same rooms, the same arrangement, the same scatter.

const _eps = 1e-6;
final Map<String, dynamic> _fixture =
    jsonDecode(File('test/fixtures/office_plan.json').readAsStringSync()) as Map<String, dynamic>;

Agent _agent(String id) =>
    Agent(id: id, slug: id, displayName: id, jobTitle: '', status: '', departmentId: null, killed: false);

double _d(Object? v) => (v as num).toDouble();

void _nah(double ist, Object? soll, String wo) {
  expect((ist - _d(soll)).abs() < _eps, isTrue, reason: '$wo: $ist, web says $soll');
}

void _sitze(List<Sitz> ist, List<dynamic> soll, String wo) {
  expect(ist.length, soll.length, reason: '$wo: seat count');
  for (var i = 0; i < ist.length; i++) {
    final s = soll[i] as Map<String, dynamic>;
    _nah(ist[i].x, s['x'], '$wo seat $i x');
    _nah(ist[i].y, s['y'], '$wo seat $i y');
    _nah(ist[i].dreh, s['dreh'], '$wo seat $i dreh');
  }
}

void main() {
  test('hash and scatter are the web\'s, code unit for code unit', () {
    for (final e in _fixture['hash'] as List<dynamic>) {
      final m = e as Map<String, dynamic>;
      expect(hash(m['t'] as String), m['h'], reason: 'hash("${m['t']}")');
      _nah(wackel(m['t'] as String, 20), m['w'], 'wackel("${m['t']}")');
    }
  });

  test('every seat arrangement lands where the web puts it', () {
    for (final e in _fixture['muster'] as List<dynamic>) {
      final m = e as Map<String, dynamic>;
      final n = m['n'] as int;
      final ist = sitzMuster(m['name'] as String, 100, 50, _d(m['w']), n < 5 ? n : 5, n, oben: m['oben'] as bool);
      _sitze(ist, m['sitze'] as List<dynamic>, '${m['name']} n=$n oben=${m['oben']}');
    }
  });

  test('the houses have the web\'s floors, rooms, corridors and seats', () {
    const namen = Raumnamen(besprechung: 'Meeting room', kueche: 'Kitchen', lounge: 'Lounge');
    for (final e in _fixture['haeuser'] as List<dynamic>) {
      final fall = e as Map<String, dynamic>;
      final gruppen = [
        for (final g in fall['gruppen'] as List<dynamic>)
          Gruppe(
            id: g['id'] as String,
            name: g['name'] as String,
            farbe: g['farbe'] as String,
            leute: [for (final id in g['leute'] as List<dynamic>) _agent(id as String)],
          ),
      ];
      final haus = hausBauen(gruppen, _d(fall['maxB']), namen, dichte: _d(fall['dichte']));
      final soll = fall['haus'] as List<dynamic>;
      final wo = fall['name'] as String;
      expect(haus.length, soll.length, reason: '$wo: floors');
      for (var ei = 0; ei < haus.length; ei++) {
        final plan = haus[ei].plan, p = (soll[ei] as Map<String, dynamic>)['plan'] as Map<String, dynamic>;
        final w = '$wo floor $ei';
        expect([for (final g in haus[ei].gruppen) g.id], (soll[ei] as Map<String, dynamic>)['gruppen'], reason: w);
        _nah(plan.breite, p['breite'], '$w breite');
        _nah(plan.hoehe, p['hoehe'], '$w hoehe');
        _nah(plan.treppe.y, (p['treppe'] as Map<String, dynamic>)['y'], '$w treppe');
        final flure = p['flure'] as List<dynamic>;
        expect(plan.flure.length, flure.length, reason: '$w corridors');
        for (var i = 0; i < flure.length; i++) {
          _nah(plan.flure[i].y, flure[i]['y'], '$w corridor $i');
        }
        final raeume = p['raeume'] as List<dynamic>;
        expect(plan.raeume.length, raeume.length, reason: '$w rooms');
        for (var i = 0; i < raeume.length; i++) {
          final r = plan.raeume[i], s = raeume[i] as Map<String, dynamic>;
          final rw = '$w room $i (${s['name']})';
          expect(r.id, s['id'], reason: rw);
          expect(r.name, s['name'], reason: rw);
          expect(r.gem?.name, s['gem'], reason: '$rw gem');
          expect(r.variante, s['variante'], reason: '$rw variante');
          expect(r.oben, s['oben'], reason: '$rw oben');
          expect(r.nr, s['nr'], reason: '$rw nr');
          expect(r.zeilen, s['zeilen'], reason: '$rw zeilen');
          for (final (k, v) in [('x', r.x), ('y', r.y), ('w', r.w), ('h', r.h), ('tuerX', r.tuerX), ('wand', r.wand)]) {
            _nah(v, s[k], '$rw $k');
          }
          expect([for (final a in r.leute) a.id], s['leute'], reason: '$rw people');
          _sitze(r.sitze, s['sitze'] as List<dynamic>, rw);
          final treff = s['treff'] as List<dynamic>;
          expect(r.treff.length, treff.length, reason: '$rw treff');
          for (var j = 0; j < treff.length; j++) {
            _nah(r.treff[j].x, treff[j]['x'], '$rw treff $j');
          }
          final tw = s['trennwand'] as Map<String, dynamic>?;
          expect(r.trennwand == null, tw == null, reason: '$rw partition');
          if (tw != null) {
            _nah(r.trennwand!.x, tw['x'], '$rw partition x');
            _nah(r.trennwand!.y1, tw['y1'], '$rw partition y1');
            expect(r.trennwand!.glas, tw['art'] == 'glas', reason: '$rw partition kind');
          }
        }
      }
    }
  });
}
