import 'package:flutter/widgets.dart';

/// The covey mark: three birds in flight formation on a clay tile.
///
/// Drawn from the same numbers as web/src/components/BirdMark.tsx — a 64
/// unit tile with radius 14, the birds' paths translated by (-0.7, 1.1) and
/// scaled by 2.9, a 2.3 unit white stroke. Painted rather than shipped as an
/// image, so it stays sharp at every size and needs no SVG package. The
/// colours are literals, as on the web: a mark does not change with the
/// appearance.
class CoveyMark extends StatelessWidget {
  const CoveyMark({super.key, this.size = 56});

  final double size;

  @override
  Widget build(BuildContext context) =>
      SizedBox.square(dimension: size, child: CustomPaint(painter: _MarkPainter()));
}

class _MarkPainter extends CustomPainter {
  static const _clay = Color(0xFFCC7A5B);

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
    canvas.drawRRect(
      RRect.fromRectAndRadius(const Rect.fromLTWH(0, 0, 64, 64), const Radius.circular(14)),
      Paint()..color = _clay,
    );
    canvas.translate(-0.7, 1.1);
    canvas.scale(2.9);
    final stroke = Paint()
      ..color = const Color(0xFFFFFFFF)
      ..style = PaintingStyle.stroke
      // In the scaled space, as the SVG's stroke-width is on the scaled group.
      ..strokeWidth = 2.3
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round;
    for (final b in _birds) {
      canvas.drawPath(
        Path()
          ..moveTo(b[0], b[1])
          ..quadraticBezierTo(b[2], b[3], b[4], b[5])
          ..quadraticBezierTo(b[6], b[7], b[8], b[9]),
        stroke,
      );
    }
  }

  @override
  bool shouldRepaint(_MarkPainter old) => false;
}
