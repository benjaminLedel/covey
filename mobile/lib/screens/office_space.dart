import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../api.dart';
import '../chrome.dart';
import '../face.dart';
import '../i18n.dart';
import '../models.dart';
import '../office/plan.dart';
import '../office/szene.dart';
import '../office/zeichner.dart';
import '../theme.dart';
import '../ui.dart';

/// The office (#398): the workforce as a building, as the web draws it at /.
///
/// One room per department, one desk per colleague; a screen is lit while
/// its agent has a running task, and a room with a lit desk is a warm room.
/// The plan is the web's, computed from the same departments by the same
/// rules (office/plan.dart), so a room stands in the same place in both.
///
/// The faces and the room signs are widgets over the canvas, not paint on
/// it, for the reason the web gives in Buero.tsx: a tap on a colleague is a
/// button, not arithmetic, and a face keeps its size while the house zooms.
class OfficeSpace extends StatefulWidget {
  const OfficeSpace({
    super.key,
    required this.api,
    required this.me,
    required this.onOpen,
    this.actions = const [],
    this.bottomClearance = capsuleClearance,
    this.compact = false,
  });

  final CoveyApi api;
  final Me me;
  final void Function(String agentId, String name, String slug, FaceState state) onOpen;
  final List<Widget> actions;
  final double bottomClearance;
  final bool compact;

  @override
  State<OfficeSpace> createState() => _OfficeSpaceState();
}

/// The building's width in centimetres; the web's BAU_BREITE.
const _bauBreite = 3200.0;

class _OfficeSpaceState extends State<OfficeSpace> {
  List<Agent>? _agents;
  List<Department> _departments = const [];
  Map<String, Running> _running = const {};
  Set<String> _waiting = const {};
  Object? _error;
  int _etage = 0;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _load();
    // What changes while one looks: who works and who waits. The building
    // itself is asked for again only with a pull or a reopening.
    _poll = Timer.periodic(const Duration(seconds: 10), (_) => _loadLive());
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final r = await Future.wait([widget.api.agents(), widget.api.departments()]);
      if (!mounted) return;
      setState(() {
        _agents = r[0] as List<Agent>;
        _departments = r[1] as List<Department>;
        _error = null;
      });
      await _loadLive();
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  Future<void> _loadLive() async {
    try {
      final r = await Future.wait([widget.api.running(), widget.api.waiting()]);
      if (!mounted) return;
      setState(() {
        _running = {for (final l in r[0] as List<Running>) l.agentId: l};
        _waiting = {for (final e in (r[1] as InboxPage).items) e.agentId};
      });
    } catch (_) {
      // The building stands without the live state; the next tick tries again.
    }
  }

  Zustand _zustand(Agent a) => a.killed
      ? Zustand.gestoppt
      : _waiting.contains(a.id)
      ? Zustand.wartet
      : _running.containsKey(a.id)
      ? Zustand.arbeitet
      : a.status == 'sleeping'
      ? Zustand.schlaeft
      : Zustand.frei;

  /// The departments with people in them, in the org chart's order, and
  /// those without a department last — as Buero.tsx builds them.
  List<Gruppe> _gruppen(BuildContext context, List<Agent> agents) {
    final ohne = agents.where((a) => !_departments.any((d) => d.id == a.departmentId)).toList();
    return [
      for (final d in _departments)
        if (agents.any((a) => a.departmentId == d.id))
          Gruppe(id: d.id, name: d.name, farbe: d.color, leute: agents.where((a) => a.departmentId == d.id).toList()),
      if (ohne.isNotEmpty) Gruppe(id: 'ohne', name: context.t('team.ohneAbteilung'), farbe: '', leute: ohne),
    ];
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final agents = _agents;
    if (agents == null) {
      return Center(
        child: EmptyNote(
          _error == null ? context.t('common.loading') : context.t('mobile.fehler', args: {'error': '$_error'}),
        ),
      );
    }
    final namen = Raumnamen(
      besprechung: context.t('team.raumBesprechung'),
      kueche: context.t('team.raumTeekueche'),
      lounge: context.t('team.raumLounge'),
    );
    final haus = hausBauen(_gruppen(context, agents), _bauBreite, namen);
    final etage = haus[_etage.clamp(0, haus.length - 1)];
    final dunkel = Theme.of(context).brightness == Brightness.dark;
    final byId = {for (final a in agents) a.id: a};
    // The house is wide: 32 metres on the web, however many work in it. On
    // an upright screen the camera takes a quarter turn — the turn the web's
    // rotation offers too — so the long side runs down the screen instead of
    // shrinking to a strip. The plan stays the web's; only the view turns.
    final groesse = MediaQuery.sizeOf(context);
    final kamera = Kamera(drehung: groesse.height > groesse.width ? startDrehung - math.pi / 2 : startDrehung);
    final szene = szeneBauen(
      etage.plan,
      (id) => byId[id] == null ? Zustand.frei : _zustand(byId[id]!),
      dunkel: dunkel,
      drehung: kamera.drehung,
    );
    final top = MediaQuery.paddingOf(context).top + (widget.compact ? 18 : (MacChrome.active ? MacChrome.height : 8));

    return ColoredBox(
      color: c.surface0,
      child: Stack(
        children: [
          Positioned.fill(
            child: _Buehne(
              szene: szene,
              kamera: kamera,
              dunkel: dunkel,
              agents: byId,
              zustand: _zustand,
              running: _running,
              oben: top + 56,
              unten: widget.bottomClearance,
              onOpen: widget.me.canWrite
                  ? (a) => widget.onOpen(a.id, a.displayName, a.slug, faceStateOf(killed: a.killed, status: a.status))
                  : null,
            ),
          ),
          Positioned(
            left: 16 + LightsInset.of(context),
            right: 16,
            top: top,
            child: Row(
              children: [
                Expanded(child: Text(context.t('team.ueberblick'), style: context.type.headlineMedium)),
                if (haus.length > 1)
                  _Etagen(
                    anzahl: haus.length,
                    gewaehlt: etage.nr,
                    titel: (nr) =>
                        '${nr == 0 ? context.t('team.etageErdTitel') : context.t('team.etageTitel', args: {'n': nr})}'
                        ' · ${haus[nr].gruppen.map((g) => g.name).join(' · ')}',
                    onSelect: (nr) => setState(() => _etage = nr),
                  ),
                ...widget.actions,
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// The floor buttons: EG, 1, 2 … — the web's `bu-etagen`.
class _Etagen extends StatelessWidget {
  const _Etagen({required this.anzahl, required this.gewaehlt, required this.titel, required this.onSelect});

  final int anzahl, gewaehlt;
  final String Function(int nr) titel;
  final ValueChanged<int> onSelect;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Semantics(
      label: context.t('team.etage'),
      child: Glass(
        child: Padding(
          padding: const EdgeInsets.all(3),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (var nr = anzahl - 1; nr >= 0; nr--)
                Tooltip(
                  message: titel(nr),
                  child: InkResponse(
                    onTap: () => onSelect(nr),
                    radius: 20,
                    child: Container(
                      constraints: const BoxConstraints(minWidth: 34, minHeight: 34),
                      alignment: Alignment.center,
                      decoration: BoxDecoration(shape: BoxShape.circle, color: nr == gewaehlt ? c.bgAccent : null),
                      child: Text(
                        nr == 0 ? context.t('team.etageErd') : '$nr',
                        style: context.type.labelLarge?.copyWith(
                          color: nr == gewaehlt ? c.textAccent : c.textSecondary,
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The stage: the canvas under an InteractiveViewer, and over it the faces
/// and signs, placed from the same projection every time the view moves.
class _Buehne extends StatefulWidget {
  const _Buehne({
    required this.szene,
    required this.kamera,
    required this.dunkel,
    required this.agents,
    required this.zustand,
    required this.running,
    required this.oben,
    required this.unten,
    required this.onOpen,
  });

  final Szene szene;
  final Kamera kamera;
  final bool dunkel;
  final Map<String, Agent> agents;
  final Zustand Function(Agent) zustand;
  final Map<String, Running> running;

  /// What the header and the capsule cover; the house is fitted between.
  final double oben, unten;
  final ValueChanged<Agent>? onOpen;

  @override
  State<_Buehne> createState() => _BuehneState();
}

class _BuehneState extends State<_Buehne> {
  final _ansicht = TransformationController();

  @override
  void dispose() {
    _ansicht.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, box) {
        final umriss = widget.kamera.umriss(widget.szene);
        const rand = 12.0;
        final platz = Size(box.maxWidth - 2 * rand, box.maxHeight - widget.oben - widget.unten - 2 * rand);
        final massstab = math.max(0.02, math.min(platz.width / umriss.width, platz.height / umriss.height));
        final bild = Size(umriss.width * massstab, umriss.height * massstab);
        // Centred in the free band between header and capsule.
        final links = (box.maxWidth - bild.width) / 2;
        final oben = widget.oben + rand + (platz.height - bild.height) / 2;
        final ursprung = Offset(links, oben) - umriss.topLeft * massstab;
        Offset punkt(double x, double y, double h) =>
            MatrixUtils.transformPoint(_ansicht.value, ursprung + widget.kamera.projiziere(x, y, h) * massstab);

        return Stack(
          clipBehavior: Clip.hardEdge,
          children: [
            Positioned.fill(
              child: InteractiveViewer(
                transformationController: _ansicht,
                minScale: 1,
                maxScale: 8,
                boundaryMargin: EdgeInsets.all(math.max(box.maxWidth, box.maxHeight) / 2),
                child: RepaintBoundary(
                  child: CustomPaint(
                    size: Size(box.maxWidth, box.maxHeight),
                    painter: Zeichner(
                      widget.szene,
                      kamera: widget.kamera,
                      massstab: massstab,
                      ursprung: ursprung,
                      dunkel: widget.dunkel,
                    ),
                  ),
                ),
              ),
            ),
            AnimatedBuilder(
              animation: _ansicht,
              builder: (context, _) {
                final zoom = _ansicht.value.getMaxScaleOnAxis();
                return Stack(
                  children: [
                    // The signs once a room is large enough to carry one.
                    if (massstab * zoom > 0.22)
                      for (final s in widget.szene.schilder)
                        if (s.raum.name.isNotEmpty)
                          _an(punkt(s.x, s.y, s.hoehe), IgnorePointer(child: _SchildWidget(s.raum))),
                    // The nearer face lies over the one behind it.
                    for (final f in [
                      ...widget.szene.figuren,
                    ]..sort((a, b) => widget.kamera.tiefe(a.x, a.y, 0).compareTo(widget.kamera.tiefe(b.x, b.y, 0))))
                      if (widget.agents[f.agentId] case final a?)
                        _an(
                          punkt(f.x, f.y, f.hoehe),
                          _Kopf(
                            agent: a,
                            zustand: widget.zustand(a),
                            running: widget.running[a.id],
                            // Two seats stand at least 125 cm apart; a face
                            // no wider than that leaves its neighbour a tap.
                            groesse: (110 * massstab * zoom - 4).clamp(14, 40),
                            onTap: widget.onOpen == null ? null : () => widget.onOpen!(a),
                          ),
                        ),
                  ],
                );
              },
            ),
          ],
        );
      },
    );
  }

  /// Centres a child on a point without knowing its size.
  Widget _an(Offset p, Widget child) => Positioned(
    left: p.dx,
    top: p.dy,
    child: FractionalTranslation(translation: const Offset(-0.5, -0.5), child: child),
  );
}

class _SchildWidget extends StatelessWidget {
  const _SchildWidget(this.raum);
  final Raum raum;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final farbe = RegExp(r'^#[0-9a-fA-F]{6}$').hasMatch(raum.farbe)
        ? Color(0xff000000 | int.parse(raum.farbe.substring(1), radix: 16))
        : null;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
      decoration: BoxDecoration(
        color: c.surface2.withValues(alpha: 0.9),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: c.hairline, width: 0.6),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (farbe != null) ...[
            Container(
              width: 7,
              height: 7,
              decoration: BoxDecoration(color: farbe, shape: BoxShape.circle),
            ),
            const SizedBox(width: 5),
          ],
          Text(raum.name, style: context.type.labelSmall?.copyWith(color: c.textSecondary)),
        ],
      ),
    );
  }
}

/// A colleague at the desk: the face, and what the data says about it — a
/// ring while a task runs, an amber mark while a decision waits.
class _Kopf extends StatelessWidget {
  const _Kopf({required this.agent, required this.zustand, required this.running, required this.groesse, this.onTap});

  final Agent agent;
  final Zustand zustand;
  final Running? running;
  final double groesse;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final ring = switch (zustand) {
      Zustand.arbeitet => const Color(0xffffb268),
      Zustand.wartet => c.textWait,
      _ => null,
    };
    final wer = [agent.displayName, if (running != null) running!.title].join(' · ');
    return Tooltip(
      message: wer,
      child: Semantics(
        button: onTap != null,
        label: wer,
        child: GestureDetector(
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.all(2),
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: c.surface2,
              border: ring == null ? null : Border.all(color: ring, width: 2),
              boxShadow: [BoxShadow(color: c.shadow, blurRadius: 4, offset: const Offset(0, 1))],
            ),
            child: Opacity(
              opacity: zustand == Zustand.gestoppt ? 0.5 : 1,
              child: Face(
                slug: agent.slug,
                state: faceStateOf(killed: agent.killed, status: agent.status),
                size: groesse,
              ),
            ),
          ),
        ),
      ),
    );
  }
}
