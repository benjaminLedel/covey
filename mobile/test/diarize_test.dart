import 'dart:typed_data';

import 'package:covey_mobile/diarize.dart';
import 'package:flutter_test/flutter_test.dart';

Float32List v(List<double> x) => Float32List.fromList(x);

void main() {
  test('close voices are one speaker, a distant one a new speaker (#367)', () {
    final d = Diarizer();
    expect(d.assign(v([1, 0, 0])), 1);
    expect(d.assign(v([0.95, 0.1, 0])), 1, reason: 'the same voice, a little different');
    expect(d.assign(v([0, 1, 0])), 2, reason: 'another voice');
    expect(d.assign(v([0.9, 0.05, 0.1])), 1);
    expect(d.assign(v([0.05, 0.98, 0])), 2);
    expect(d.speakers, 2);
  });

  test('a segment without a voice goes to the previous speaker', () {
    final d = Diarizer();
    expect(d.assign(null), 1, reason: 'the first segment opens speaker 1');
    expect(d.assign(v([0, 1, 0])), 1, reason: 'the first voice fills speaker 1');
    expect(d.assign(v([1, 0, 0])), 2);
    expect(d.assign(null), 2);
    expect(d.assign(Float32List(0)), 2);
  });

  test('scale does not matter, direction does', () {
    final d = Diarizer();
    expect(d.assign(v([10, 0])), 1);
    expect(d.assign(v([0.2, 0.01])), 1);
  });
}
