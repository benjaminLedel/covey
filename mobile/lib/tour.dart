import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'i18n.dart';
import 'prefs.dart';
import 'theme.dart';

/// The tour (#402): what the app can do, shown once, in a handful of steps.
///
/// The same idea as the web's (web/src/components/Tour.tsx) and the same
/// words — the steps take their text from the catalogues' `tour.*` keys,
/// with the app's variants where a phone does it differently (a tap on a
/// colleague, the + for capturing). A step points at a [TourAnker] by its
/// id; what only exists inside an open conversation stands in the middle.
///
/// Seen is remembered on this device. Whoever wants it again finds it in
/// the settings.
class TourSchritt {
  const TourSchritt(this.titel, this.text, [this.ziel]);

  /// Catalogue keys, spelt out so tool/locales.dart finds them.
  final String titel, text;

  /// The [TourAnker] to point at; none: in the middle.
  final String? ziel;
}

const appTour = [
  TourSchritt('tour.willkommen.titel', 'tour.willkommen.text'),
  TourSchritt('tour.teamApp.titel', 'tour.teamApp.text', 'team'),
  TourSchritt('tour.gespraech.titel', 'tour.gespraech.text'),
  TourSchritt('tour.entscheiden.titel', 'tour.entscheiden.text'),
  TourSchritt('tour.notizenApp.titel', 'tour.notizenApp.text', 'notes'),
  TourSchritt('tour.plus.titel', 'tour.plus.text', 'plus'),
  TourSchritt('tour.personApp.titel', 'tour.personApp.text', 'person'),
];

const _gesehen = 'tour.team';

Future<bool> tourGesehen() async => await Prefs.instance.read(_gesehen) == '1';

/// Marks an element the tour can point at. The last one built under an id
/// wins; a capsule and a sidebar never stand on screen together.
class TourAnker extends StatefulWidget {
  const TourAnker({super.key, required this.id, required this.child});

  final String id;
  final Widget child;

  static final _anker = <String, GlobalKey>{};

  /// Where the element stands on screen, or null when it is not built.
  static Rect? rahmen(String id) {
    final box = _anker[id]?.currentContext?.findRenderObject();
    if (box is! RenderBox || !box.hasSize || !box.attached) return null;
    return box.localToGlobal(Offset.zero) & box.size;
  }

  @override
  State<TourAnker> createState() => _TourAnkerState();
}

class _TourAnkerState extends State<TourAnker> {
  final _key = GlobalKey();

  @override
  Widget build(BuildContext context) {
    TourAnker._anker[widget.id] = _key;
    return KeyedSubtree(key: _key, child: widget.child);
  }

  @override
  void dispose() {
    if (TourAnker._anker[widget.id] == _key) TourAnker._anker.remove(widget.id);
    super.dispose();
  }
}

/// Shows the tour over everything, and remembers it once it ends — whether
/// seen through or skipped.
Future<void> zeigeTour(BuildContext context, {List<TourSchritt> schritte = appTour}) async {
  await showGeneralDialog<void>(
    context: context,
    barrierDismissible: false,
    barrierColor: Colors.transparent,
    barrierLabel: context.t('tour.titel'),
    transitionDuration: const Duration(milliseconds: 180),
    pageBuilder: (_, _, _) => _Tour(schritte: schritte),
    transitionBuilder: (_, a, _, child) => FadeTransition(opacity: a, child: child),
  );
  await Prefs.instance.write(_gesehen, '1');
}

class _Tour extends StatefulWidget {
  const _Tour({required this.schritte});

  final List<TourSchritt> schritte;

  @override
  State<_Tour> createState() => _TourState();
}

class _TourState extends State<_Tour> {
  var _i = 0;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final s = widget.schritte[_i];
    final letzter = _i == widget.schritte.length - 1;
    final ziel = s.ziel == null ? null : TourAnker.rahmen(s.ziel!)?.inflate(6);
    final karte = Container(
      constraints: const BoxConstraints(maxWidth: 340),
      padding: const EdgeInsets.fromLTRB(18, 16, 18, 10),
      decoration: BoxDecoration(
        color: c.surface2,
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: c.border, width: 0.5),
        boxShadow: [BoxShadow(color: c.shadow, blurRadius: 28, offset: const Offset(0, 10))],
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.t('tour.schritt', args: {'n': _i + 1, 'm': widget.schritte.length}),
            style: context.type.labelMedium?.copyWith(color: c.textMuted),
          ),
          const SizedBox(height: 4),
          Semantics(header: true, child: Text(context.t(s.titel), style: context.type.titleMedium)),
          const SizedBox(height: 6),
          Text(context.t(s.text), style: context.type.bodyMedium?.copyWith(color: c.textSecondary, height: 1.45)),
          const SizedBox(height: 10),
          // In one line where the words fit, stacked where they do not: a
          // narrow phone in German or Polish has no room for all three.
          SizedBox(
            width: double.infinity,
            child: OverflowBar(
              alignment: MainAxisAlignment.end,
              overflowAlignment: OverflowBarAlignment.end,
              spacing: 4,
              children: [
                if (!letzter)
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(),
                    child: Text(context.t('tour.ueberspringen'), style: TextStyle(color: c.textMuted)),
                  ),
                if (_i > 0) TextButton(onPressed: () => setState(() => _i--), child: Text(context.t('tour.zurueck'))),
                FilledButton(
                  autofocus: true,
                  onPressed: () => letzter ? Navigator.of(context).pop() : setState(() => _i++),
                  child: Text(letzter ? context.t('tour.fertig') : context.t('tour.weiter')),
                ),
              ],
            ),
          ),
        ],
      ),
    );
    return Material(
      type: MaterialType.transparency,
      child: Stack(
        children: [
          Positioned.fill(
            child: CustomPaint(painter: _Abdunkeln(ziel, c.textAccent, Colors.black.withValues(alpha: 0.4))),
          ),
          Positioned.fill(
            child: SafeArea(
              child: CustomSingleChildLayout(delegate: _KartenOrt(ziel, MediaQuery.paddingOf(context)), child: karte),
            ),
          ),
        ],
      ),
    );
  }
}

/// The dimming with a hole where the target is, and a ring around it.
class _Abdunkeln extends CustomPainter {
  _Abdunkeln(this.ziel, this.ring, this.dunkel);

  final Rect? ziel;
  final Color ring, dunkel;

  @override
  void paint(Canvas canvas, Size size) {
    final alles = Path()..addRect(Offset.zero & size);
    final z = ziel;
    if (z == null) {
      canvas.drawPath(alles, Paint()..color = dunkel);
      return;
    }
    final loch = RRect.fromRectAndRadius(z, const Radius.circular(14));
    canvas.drawPath(Path.combine(PathOperation.difference, alles, Path()..addRRect(loch)), Paint()..color = dunkel);
    canvas.drawRRect(
      loch,
      Paint()
        ..color = ring
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2,
    );
  }

  @override
  bool shouldRepaint(_Abdunkeln old) => old.ziel != ziel || old.ring != ring || old.dunkel != dunkel;
}

/// Where the card stands: beside a target at the left edge (the sidebar of
/// a wide window) when there is room, otherwise above or below it (the capsule at
/// the bottom, the account at the top), always inside the screen; without a
/// target in the middle. The target is in screen coordinates, the layout in
/// the safe area's — `rand` is the difference.
class _KartenOrt extends SingleChildLayoutDelegate {
  _KartenOrt(this.ziel, this.rand);

  final Rect? ziel;
  final EdgeInsets rand;
  static const _abstand = 14.0, _luft = 16.0;

  @override
  BoxConstraints getConstraintsForChild(BoxConstraints c) =>
      BoxConstraints(maxWidth: math.max(0, c.maxWidth - 2 * _luft), maxHeight: c.maxHeight);

  @override
  Offset getPositionForChild(Size size, Size child) {
    double klemme(double v, double lo, double hi) => v.clamp(lo, math.max(lo, hi)).toDouble();
    final z = ziel?.shift(Offset(-rand.left, -rand.top));
    if (z == null) return Offset((size.width - child.width) / 2, (size.height - child.height) / 2);
    // Beside the target only at the left edge — the sidebar of a wide
    // window; a capsule item in the middle gets the card above it.
    if (z.left < 120 && z.right + _abstand + child.width <= size.width - _luft) {
      return Offset(
        z.right + _abstand,
        klemme(z.center.dy - child.height / 2, _luft, size.height - child.height - _luft),
      );
    }
    final x = klemme(z.center.dx - child.width / 2, _luft, size.width - child.width - _luft);
    final oben = z.top - _abstand - child.height;
    final unten = z.bottom + _abstand;
    final y = oben >= _luft && (z.center.dy > size.height / 2 || unten + child.height > size.height - _luft)
        ? oben
        : klemme(unten, _luft, size.height - child.height - _luft);
    return Offset(x, y);
  }

  @override
  bool shouldRelayout(_KartenOrt old) => old.ziel != ziel || old.rand != rand;
}
