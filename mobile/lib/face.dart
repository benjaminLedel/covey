import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

/// An agent's face (#337) — the app's port of web/src/components/Gesicht.tsx.
///
/// Not an avatar somebody picks: it is computed from the agent's slug, so the
/// same agent has the same face on every device and in the browser. The
/// hash, the tones and the features are the web's numbers exactly; a test
/// pins the hash against the web's own function. It shows the state the
/// list otherwise only carries as a word:
///
///   working  — the head breathes, the eyes look around and blink, the mouth
///              is a concentrated line
///   sleeping — eyes shut, a small o for a mouth, three z rising
///   killed   — grey and still
///
/// Still when the system asks for reduced motion; the z then stay, as on the
/// web, because there the motion itself was the information.
enum FaceState { working, sleeping, killed }

/// The state the web's team shell derives from an agent (Team.tsx).
FaceState faceStateOf({required bool killed, required String status}) =>
    killed ? FaceState.killed : (status == 'sleeping' ? FaceState.sleeping : FaceState.working);

/// FNV-1a over the UTF-16 code units, with JavaScript's Math.imul and
/// Math.abs — the web's hash, bit for bit.
int faceHash(String text) {
  var h = 2166136261;
  for (final c in text.codeUnits) {
    h = (h ^ c).toSigned(32);
    h = (h * 16777619).toSigned(32);
  }
  return h.abs();
}

/// Six tones on one lightness and one chroma, differing only in hue — so
/// fifty heads read as one workforce and two side by side stay two.
/// (light, dark), from Gesicht.tsx.
const _tones = [
  (Color(0xFF497367), Color(0xFF86B7A9)),
  (Color(0xFF566B86), Color(0xFF94ADCE)),
  (Color(0xFF826051), Color(0xFFC9A18F)),
  (Color(0xFF6F6280), Color(0xFFB3A3C7)),
  (Color(0xFF6D6B49), Color(0xFFB0AD85)),
  (Color(0xFF835D62), Color(0xFFCB9DA3)),
];

class _Features {
  _Features(String slug) : this._(faceHash(slug));

  _Features._(int h)
    : tone = _tones[h % _tones.length],
      gap = 3.4 + (h % 3) * 0.95,
      eyeY = 11.2 + ((h ~/ 3) % 3) * 0.95,
      eyeR = 1.75 + ((h ~/ 11) % 2) * 0.35,
      mouth = 4.4 + ((h ~/ 23) % 2) * 1.6,
      radius = 6 + ((h ~/ 47) % 3) * 1.6,
      // So that not all of them blink at once.
      offset = (h % 7) * 0.9;

  final (Color, Color) tone;
  final double gap, eyeY, eyeR, mouth, radius, offset;
}

/// One clock for every face: a list of fifty faces repaints fifty painters,
/// it does not rebuild fifty widgets.
class _FaceClock extends ChangeNotifier {
  _FaceClock._() {
    _ticker = Ticker((d) {
      seconds = d.inMicroseconds / 1e6;
      notifyListeners();
    });
  }

  static final instance = _FaceClock._();
  late final Ticker _ticker;
  double seconds = 0;
  int _users = 0;

  void attach() {
    if (_users++ == 0) _ticker.start();
  }

  void detach() {
    if (--_users == 0) _ticker.stop();
  }
}

class Face extends StatefulWidget {
  const Face({super.key, required this.slug, this.state = FaceState.working, this.size = 26});

  final String slug;
  final FaceState state;
  final double size;

  @override
  State<Face> createState() => _FaceState();
}

class _FaceState extends State<Face> {
  late _Features _f = _Features(widget.slug);
  bool _attached = false;

  @override
  void didUpdateWidget(Face old) {
    super.didUpdateWidget(old);
    if (old.slug != widget.slug) _f = _Features(widget.slug);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final wants = !still && widget.state != FaceState.killed;
    if (wants && !_attached) _FaceClock.instance.attach();
    if (!wants && _attached) _FaceClock.instance.detach();
    _attached = wants;
  }

  @override
  void dispose() {
    if (_attached) _FaceClock.instance.detach();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // The dark tones on a dark ground, as light-dark() does on the web.
    final isDark = Theme.of(context).brightness == Brightness.dark;
    Widget face = SizedBox.square(
      dimension: widget.size,
      child: CustomPaint(
        painter: _FacePainter(f: _f, state: widget.state, dark: isDark, clock: _attached ? _FaceClock.instance : null),
      ),
    );
    // Stopped: no tone left, and the sign everybody reads as "no entry" on
    // the corner (#414) — grey eyes alone were close to asleep and easy to
    // miss in a list. The sign stays red; only the face is greyed.
    if (widget.state == FaceState.killed) {
      face = Stack(
        clipBehavior: Clip.none,
        children: [
          Opacity(
            opacity: 0.55,
            child: ColorFiltered(colorFilter: const ColorFilter.matrix(_grey), child: face),
          ),
          Positioned.fill(
            child: CustomPaint(painter: _StopSign(ring: Theme.of(context).colorScheme.surface)),
          ),
        ],
      );
    }
    return face;
  }
}

const _grey = <double>[
  0.2126, 0.7152, 0.0722, 0, 0, //
  0.2126, 0.7152, 0.0722, 0, 0,
  0.2126, 0.7152, 0.0722, 0, 0,
  0, 0, 0, 1, 0,
];

/// A keyframe track: (fraction of the period, value), eased in and out
/// between neighbours — the CSS animations of app.css.
double _track(double t, List<(double, double)> frames) {
  for (var i = 0; i < frames.length - 1; i++) {
    final (a, va) = frames[i];
    final (b, vb) = frames[i + 1];
    if (t >= a && t <= b) {
      if (b == a) return vb;
      final x = Curves.easeInOut.transform((t - a) / (b - a));
      return va + (vb - va) * x;
    }
  }
  return frames.last.$2;
}

/// Where in its period an animation stands, with the face's own delay.
double _phase(double seconds, double period, double delay) {
  final s = seconds - delay;
  if (s <= 0) return 0;
  return (s % period) / period;
}

class _FacePainter extends CustomPainter {
  _FacePainter({required this.f, required this.state, required this.dark, required this.clock}) : super(repaint: clock);

  final _Features f;
  final FaceState state;
  final bool dark;
  final _FaceClock? clock;

  @override
  void paint(Canvas canvas, Size size) {
    final s = clock?.seconds;
    canvas.scale(size.width / 24);
    final tone = dark ? f.tone.$2 : f.tone.$1;
    final ink = dark ? const Color(0xFF12100F) : const Color(0xFFFFFFFF);
    final sleeping = state == FaceState.sleeping;
    final closed = sleeping || state == FaceState.killed;

    // The head: a soft square, not a ball — a ball beside a ball reads as a
    // bullet point. It breathes: 3.7 s working, 5.2 s asleep.
    var breath = 1.0, rise = 0.0;
    if (s != null) {
      final p = _phase(s, sleeping ? 5.2 : 3.7, f.offset);
      final v = _track(p, const [(0, 0), (0.5, 1), (1, 0)]);
      breath = 1 + 0.085 * v;
      rise = -0.35 * v;
    }
    canvas.save();
    canvas.translate(12, 12 + rise);
    canvas.scale(breath);
    canvas.translate(-12, -12);
    canvas.drawRRect(
      RRect.fromRectAndRadius(const Rect.fromLTWH(1.5, 1.5, 21, 21), Radius.circular(f.radius)),
      Paint()..color = tone,
    );
    canvas.restore();

    final line = Paint()
      ..color = ink
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.9
      ..strokeCap = StrokeCap.round;
    final y = f.eyeY;

    if (closed) {
      for (final x in [12 - f.gap, 12 + f.gap]) {
        // Asleep: the arc of a lowered lid. Stopped: a line. Two closed
        // states, two shapes — a line alone reads as a missing eye.
        final path = sleeping
            ? (Path()
                ..moveTo(x - f.eyeR, y - 0.4)
                ..quadraticBezierTo(x, y + 1.5, x + f.eyeR, y - 0.4))
            : (Path()
                ..moveTo(x - f.eyeR, y)
                ..lineTo(x + f.eyeR, y));
        canvas.drawPath(path, line);
      }
      if (sleeping) {
        // The sleeping mouth: a small o that breathes with the head.
        canvas.save();
        canvas.translate(12, y + 5);
        canvas.scale(breath);
        canvas.drawCircle(
          Offset.zero,
          1.15,
          Paint()
            ..color = ink
            ..style = PaintingStyle.stroke
            ..strokeWidth = 1.5,
        );
        canvas.restore();
      } else {
        // Stopped: no breath, a straight line (#414).
        canvas.drawLine(Offset(12 - f.mouth / 2, y + 5), Offset(12 + f.mouth / 2, y + 5), line);
      }
    } else {
      // The eyes look around (5.3 s) and blink twice every 7.1 s.
      var dx = 0.0, dy = 0.0, blink = 1.0, mouth = 1.0;
      if (s != null) {
        final look = _phase(s, 5.3, f.offset);
        dx = _track(look, const [
          (0, 0),
          (0.18, 0),
          (0.26, 2.6),
          (0.40, 2.6),
          (0.48, 0),
          (0.58, 0),
          (0.66, -2.6),
          (0.82, -2.6),
          (0.90, 0),
          (1, 0),
        ]);
        dy = _track(look, const [
          (0, 0),
          (0.18, 0),
          (0.26, -0.7),
          (0.40, -0.7),
          (0.48, 0.5),
          (0.58, 0.5),
          (0.66, 0.5),
          (0.82, 0.5),
          (0.90, 0),
          (1, 0),
        ]);
        final b = _phase(s, 7.1, f.offset + 1.5);
        blink = _track(b, const [
          (0, 1),
          (0.62, 1),
          (0.645, 0.1),
          (0.655, 0.1),
          (0.68, 1),
          (0.93, 1),
          (0.95, 0.1),
          (0.96, 0.1),
          (0.985, 1),
          (1, 1),
        ]);
        // The mouth shortens while it thinks (5.3 s).
        mouth = _track(look, const [(0, 1), (0.24, 1), (0.32, 0.6), (0.46, 0.6), (0.54, 1), (1, 1)]);
      }
      final eye = Paint()..color = ink;
      for (final x in [12 - f.gap, 12 + f.gap]) {
        canvas.save();
        canvas.translate(x + dx, y + dy);
        canvas.scale(1, blink);
        canvas.drawCircle(Offset.zero, f.eyeR, eye);
        canvas.restore();
      }
      // A line, not an arc: an arc would be a smile, and a smiling agent
      // claims something about its mood.
      final half = f.mouth * mouth / 2;
      canvas.drawLine(Offset(12 - half, y + 5), Offset(12 + half, y + 5), line);
    }

    // Three z rise, staggered, only while asleep. They stand beside the head
    // on the page's ground, so they carry the head's colour, not the ink's.
    if (sleeping) {
      const zs = [(19.0, 6.0, 0.0), (21.5, 1.5, 0.5), (24.0, -2.5, 1.0)];
      for (final (x, zy, delay) in zs) {
        var opacity = 0.9, tx = 0.0, ty = 0.0, sc = 1.0;
        if (s != null) {
          final p = _phase(s, 4.2, f.offset + delay);
          opacity = _track(p, const [(0, 0), (0.25, 0.95), (0.55, 0.85), (1, 0)]);
          tx = 3 * p;
          ty = 2 - 8 * p;
          sc = 0.7 + 0.45 * p;
        }
        final tp = TextPainter(
          text: TextSpan(
            text: 'z',
            style: TextStyle(
              fontSize: 7,
              fontWeight: FontWeight.w700,
              color: tone.withValues(alpha: opacity),
            ),
          ),
          textDirection: TextDirection.ltr,
        )..layout();
        canvas.save();
        canvas.translate(x + tx, zy + ty);
        canvas.scale(sc);
        tp.paint(canvas, Offset(0, -tp.height * 0.8));
        canvas.restore();
      }
    }
  }

  @override
  bool shouldRepaint(_FacePainter old) => old.f != f || old.state != state || old.dark != dark || old.clock != clock;
}

/// The stop sign on a stopped face (#414): a red disc with a white bar, in
/// the face's own 24-unit grid, bottom right — as Gesicht.tsx draws it.
class _StopSign extends CustomPainter {
  _StopSign({required this.ring});
  final Color ring;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.scale(size.width / 24);
    const c = Offset(19.5, 19.5);
    canvas.drawCircle(c, 5.2 + 0.65, Paint()..color = ring);
    canvas.drawCircle(c, 5.2, Paint()..color = const Color(0xFF9D2427));
    canvas.drawRRect(
      RRect.fromRectAndRadius(const Rect.fromLTWH(16.7, 18.55, 5.6, 1.9), const Radius.circular(0.5)),
      Paint()..color = const Color(0xFFFFFFFF),
    );
  }

  @override
  bool shouldRepaint(_StopSign old) => old.ring != ring;
}
