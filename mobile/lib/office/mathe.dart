// The computed scatter, as in web/src/team/buero/mathe.ts (#398).
//
// Everything in the office that is meant to look random comes from a hash of
// a key: the same offset for the same seat on every visit, and still no grid.
// The web and the app must draw the same number from the same key — the
// office is one building, whichever window it is seen through — so this is
// JavaScript's arithmetic, bit for bit, and test/office_plan_test.dart holds
// it to the web's numbers.

import '../face.dart';

/// FNV-1a, the web's hash bit for bit — the same function the faces use,
/// because the faces and the house must draw the same number from a key.
int hash(String text) => faceHash(text);

/// A number between −weite and +weite, fixed per key.
double wackel(String schluessel, double weite) => ((hash(schluessel) % 2000) / 1000 - 1) * weite;

/// One entry of a list, fixed per key — this is how the catalogue chooses.
T sorte<T>(List<T> liste, Object schluessel) => liste[hash('$schluessel') % liste.length];

/// `Math.round`: halves go up, also below zero, where Dart's `round` goes
/// away from zero.
int jsRound(double v) => (v + 0.5).floor();

/// `Array.prototype.sort` is stable, Dart's `List.sort` is not — and the plan
/// sorts lists with ties (door slots at equal distance) whose order decides
/// where a desk stands.
void stabilSortieren<T>(List<T> liste, int Function(T a, T b) vergleich) {
  final idx = List<int>.generate(liste.length, (i) => i);
  final kopie = List<T>.of(liste);
  idx.sort((a, b) {
    final c = vergleich(kopie[a], kopie[b]);
    return c != 0 ? c : a - b;
  });
  for (var i = 0; i < idx.length; i++) {
    liste[i] = kopie[idx[i]];
  }
}

/// A JavaScript comparator returns any number; Dart wants its sign.
int vorzeichen(num v) => v < 0 ? -1 : (v > 0 ? 1 : 0);
