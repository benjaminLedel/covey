import 'dart:math' as math;

import 'package:flutter/widgets.dart';

/// The covey mark: three birds in flight formation on a clay tile.
///
/// Drawn from the same numbers as web/src/components/BirdMark.tsx — a 64
/// unit tile with radius 14, the birds' paths translated by (-0.7, 1.1) and
/// scaled by 2.9, a 2.3 unit white stroke in that scaled space. Painted
/// rather than shipped as an image, so it stays sharp at every size, needs no
/// SVG package, and can be drawn in stages for the start animation (#332).
/// The colours are literals, as on the web: a mark does not change with the
/// appearance.
class CoveyMark extends StatelessWidget {
  const CoveyMark({super.key, this.size = 56});

  final double size;

  @override
  Widget build(BuildContext context) =>
      SizedBox.square(dimension: size, child: CustomPaint(painter: MarkPainter()));
}

/// Paints the mark, optionally part-way: [tile] scales the clay tile in,
/// [birds] holds how far each bird's stroke is drawn (0..1), and [flight]
/// lifts the birds a little and beats their wings — the one moment the mark
/// moves.
class MarkPainter extends CustomPainter {
  MarkPainter({this.tile = 1, this.birds = const [1, 1, 1], this.flight = 0, this.radius = 14});

  final double tile;

  /// The tile's corner radius in the 64-unit space. 0 for the iOS app icon,
  /// whose corners the system rounds itself (#335).
  final double radius;
  final List<double> birds;
  final double flight;

  static const clay = Color(0xFFCC7A5B);

  // Each bird: M a b Q c d e f Q g h i j — two quadratic curves.
  static const _birds = [
    [7.0, 15.0, 9.75, 11.8, 12.5, 15.0, 15.25, 11.8, 18.0, 15.0],
    [3.5, 10.0, 5.5, 7.7, 7.5, 10.0, 9.5, 7.7, 11.5, 10.0],
    [13.0, 8.0, 14.5, 6.3, 16.0, 8.0, 17.5, 6.3, 19.0, 8.0],
  ];

  @override
  void paint(Canvas canvas, Size size) {
    final s = size.width / 64;
    canvas.scale(s);
    if (tile > 0) {
      canvas.save();
      canvas.translate(32, 32);
      canvas.scale(tile);
      canvas.translate(-32, -32);
      canvas.drawRRect(
        RRect.fromRectAndRadius(const Rect.fromLTWH(0, 0, 64, 64), Radius.circular(radius)),
        Paint()..color = clay,
      );
      canvas.restore();
    }
    canvas.translate(-0.7, 1.1);
    canvas.scale(2.9);
    final stroke = Paint()
      ..color = const Color(0xFFFFFFFF)
      ..style = PaintingStyle.stroke
      // In the scaled space, as the SVG's stroke-width is on the scaled group.
      ..strokeWidth = 2.3
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round;
    for (var i = 0; i < _birds.length; i++) {
      final t = i < birds.length ? birds[i].clamp(0.0, 1.0) : 1.0;
      if (t <= 0) continue;
      final b = _birds[i];
      // In flight each bird rises a little, out of step with the others, and
      // its wings (the two control points) beat. The envelope is zero at both
      // ends, so the last frame is exactly the resting mark — the hand-over to
      // the static one does not jump.
      final envelope = math.sin(math.pi * flight);
      final phase = flight * math.pi * 2 + i * 1.3;
      final lift = -0.8 * envelope * (1 + 0.3 * math.sin(phase));
      final beat = 1.1 * envelope * math.sin(phase * 2);
      final path = Path()
        ..moveTo(b[0], b[1] + lift)
        ..quadraticBezierTo(b[2], b[3] + lift + beat, b[4], b[5] + lift)
        ..quadraticBezierTo(b[6], b[7] + lift + beat, b[8], b[9] + lift);
      if (t >= 1) {
        canvas.drawPath(path, stroke);
      } else {
        for (final m in path.computeMetrics()) {
          canvas.drawPath(m.extractPath(0, m.length * t), stroke);
        }
      }
    }
  }

  @override
  bool shouldRepaint(MarkPainter old) =>
      old.tile != tile || old.flight != flight || old.radius != radius || !_same(old.birds, birds);

  static bool _same(List<double> a, List<double> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }
}
