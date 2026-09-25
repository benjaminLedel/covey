import 'package:flutter/material.dart';

import 'mark.dart';

/// The start of the app (#332): the mark assembles itself while the app
/// loads its catalogue and reads the Keychain, and then hands over.
///
///   0.00–0.30  the clay tile springs in
///   0.20–0.65  the three birds draw, one after another
///   0.55–1.00  they fly — rise a little, beat their wings
///   0.60–0.85  the word "covey" arrives
///
/// It covers the loading time rather than adding to it: [ready] is when the
/// app could show its first screen, and the splash leaves as soon as both the
/// animation and the app are done. With reduced motion requested it is not
/// played at all.
class Splash extends StatefulWidget {
  const Splash({super.key, required this.ready, required this.onDone});

  /// Completes when the app has what its first screen needs.
  final Future<void> ready;
  final VoidCallback onDone;

  static const duration = Duration(milliseconds: 1500);

  @override
  State<Splash> createState() => _SplashState();
}

class _SplashState extends State<Splash> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(vsync: this, duration: Splash.duration);
  bool _started = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_started) return;
    _started = true;
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final played = still ? Future<void>.value() : _c.forward().orCancel.catchError((_) {});
    if (still) _c.value = 1;
    Future.wait([played, widget.ready]).then((_) {
      if (mounted) widget.onDone();
    });
  }

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  double _span(double t, double from, double to, [Curve curve = Curves.easeOutCubic]) =>
      curve.transform(((t - from) / (to - from)).clamp(0.0, 1.0));

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    // Material, not a bare ColoredBox: the word needs a text style to inherit,
    // or Flutter underlines it as unstyled.
    return Material(
      color: dark ? const Color(0xFF121214) : const Color(0xFFF2F2F2),
      child: Center(
        child: AnimatedBuilder(
          animation: _c,
          builder: (context, _) {
            final t = _c.value;
            final word = _span(t, 0.60, 0.85);
            return Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                SizedBox.square(
                  dimension: 96,
                  child: CustomPaint(
                    painter: MarkPainter(
                      tile: _span(t, 0.0, 0.30, Curves.easeOutBack),
                      birds: [_span(t, 0.20, 0.45), _span(t, 0.30, 0.55), _span(t, 0.40, 0.65)],
                      flight: _span(t, 0.55, 1.0, Curves.easeInOut),
                    ),
                  ),
                ),
                const SizedBox(height: 18),
                Opacity(
                  opacity: word,
                  child: Transform.translate(
                    offset: Offset(0, 8 * (1 - word)),
                    child: Text(
                      'covey',
                      style: TextStyle(
                        fontSize: 28,
                        fontWeight: FontWeight.w600,
                        letterSpacing: 0.5,
                        color: dark ? const Color(0xFFF4F4F5) : const Color(0xFF16161A),
                      ),
                    ),
                  ),
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}
