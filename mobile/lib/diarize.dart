import 'dart:math' as math;
import 'dart:typed_data';

import 'diagnostics.dart';

/// Tells the voices of one meeting apart (#367): each segment's speaker
/// embedding is compared with the speakers found so far; close enough, it
/// joins the closest, otherwise it is a new speaker. A speaker's reference
/// is the normalised running mean of their embeddings, so it settles as they
/// talk. A segment without an embedding — too short to judge — goes to the
/// previous speaker rather than to a guess.
///
/// Nothing is kept beyond the meeting: the embeddings live in this object
/// and nowhere else.
class Diarizer {
  Diarizer({this.threshold = 0.55});

  /// Cosine similarity from which two segments count as one voice. TitaNet's
  /// same-speaker scores sit well above it on clean speech; short, noisy
  /// segments fall towards it.
  final double threshold;

  final List<Float32List> _centroids = [];
  final List<int> _counts = [];
  int? _last;

  /// How many speakers there are so far.
  int get speakers => _centroids.length;

  /// The speaker of a segment, from 1.
  int assign(Float32List? voice) {
    if (voice == null || voice.isEmpty) return _last ?? _open(null);
    final v = _normalised(voice);
    var best = -1;
    var bestScore = -1.0;
    for (var i = 0; i < _centroids.length; i++) {
      if (_centroids[i].length != v.length) continue;
      final score = _dot(_centroids[i], v);
      if (score > bestScore) {
        bestScore = score;
        best = i;
      }
    }
    // A speaker opened by a segment too short to judge takes the first voice
    // that matches nobody else.
    final placeholder = _centroids.indexWhere((c) => c.isEmpty);
    if (best >= 0 && bestScore >= threshold) {
      _merge(best, v);
      _last = best + 1;
    } else if (placeholder >= 0) {
      _merge(placeholder, v);
      _last = placeholder + 1;
    } else {
      _last = _open(v);
    }
    diag('dictation', 'voice: speaker $_last (similarity ${bestScore.toStringAsFixed(2)}, $speakers so far)');
    return _last!;
  }

  int _open(Float32List? v) {
    _centroids.add(v ?? Float32List(0));
    _counts.add(v == null ? 0 : 1);
    return _last = _centroids.length;
  }

  void _merge(int i, Float32List v) {
    final c = _centroids[i];
    final n = _counts[i];
    if (c.isEmpty) {
      _centroids[i] = v;
      _counts[i] = 1;
      return;
    }
    final m = Float32List(c.length);
    for (var k = 0; k < c.length; k++) {
      m[k] = (c[k] * n + v[k]) / (n + 1);
    }
    _centroids[i] = _normalised(m);
    _counts[i] = n + 1;
  }

  static Float32List _normalised(Float32List v) {
    var sum = 0.0;
    for (final x in v) {
      sum += x * x;
    }
    final norm = math.sqrt(sum);
    if (norm == 0) return v;
    return Float32List.fromList([for (final x in v) x / norm]);
  }

  static double _dot(Float32List a, Float32List b) {
    var s = 0.0;
    for (var i = 0; i < a.length; i++) {
      s += a[i] * b[i];
    }
    return s;
  }
}
