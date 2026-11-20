import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../live.dart';
import '../models.dart';
import '../theme.dart';
import '../ui.dart';
import 'decision_cards.dart';

/// How many workflow approvals wait for the person — the badge on the space
/// in the sidebar and the capsule (#748).
final approvalsOpen = ValueNotifier<int>(0);

/// The workflows' approvals put to the person (#748), in a space of their
/// own: they used to stand in the team list among the agents' questions,
/// where a person with several of them scrolled past them to reach a
/// colleague, and one looking for them found them between conversations.
///
/// Grouped by workflow, oldest first within each — the server's order —, so
/// the run that has waited longest is decided first.
class ApprovalsSpace extends StatefulWidget {
  const ApprovalsSpace({
    super.key,
    required this.api,
    this.actions = const [],
    this.bottomClearance = capsuleClearance,
    this.compact = false,
  });

  final CoveyApi api;
  final List<Widget> actions;
  final double bottomClearance;
  final bool compact;

  @override
  State<ApprovalsSpace> createState() => _ApprovalsSpaceState();
}

class _ApprovalsSpaceState extends State<ApprovalsSpace> {
  List<WorkflowDecision>? _open;
  Object? _error;

  /// The runs decided here, with the option chosen: the card says so for a
  /// moment before the next read takes it away.
  final Map<String, String> _decided = {};
  Timer? _reload;
  Timer? _poll;
  StreamSubscription<void>? _live;
  final _reads = Latest();

  @override
  void initState() {
    super.initState();
    _load();
    // The event stream has no kind of its own for workflow runs; a task or
    // an approval often comes with one, and a minute's read covers the rest.
    _live = LiveEvents.instance.of({'approval', 'task'}).listen((_) => unawaited(_load()));
    _poll = Timer.periodic(const Duration(minutes: 1), (_) => unawaited(_load()));
  }

  @override
  void dispose() {
    _reload?.cancel();
    _poll?.cancel();
    _live?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    final ticket = _reads.begin();
    try {
      final d = await widget.api.workflowDecisions();
      if (!mounted || !_reads.isCurrent(ticket)) return;
      setState(() {
        _open = d;
        _error = null;
        _decided.removeWhere((run, _) => !d.any((x) => x.run == run));
      });
      approvalsOpen.value = d.where((x) => !_decided.containsKey(x.run)).length;
    } catch (e) {
      if (mounted && _reads.isCurrent(ticket)) setState(() => _error = e);
    }
  }

  void _onDecided(WorkflowDecision d, String option) {
    setState(() => _decided[d.run] = option);
    approvalsOpen.value = (_open ?? const []).where((x) => !_decided.containsKey(x.run)).length;
    _reload?.cancel();
    _reload = Timer(const Duration(seconds: 3), () => unawaited(_load()));
  }

  @override
  Widget build(BuildContext context) {
    final open = _open;
    final waiting = (open ?? const <WorkflowDecision>[]).where((d) => !_decided.containsKey(d.run)).length;
    // By workflow, in the order the first of each appears.
    final groups = <String, List<WorkflowDecision>>{};
    for (final d in open ?? const <WorkflowDecision>[]) {
      groups.putIfAbsent(d.name.isEmpty ? d.workflow : d.name, () => []).add(d);
    }
    return SpaceScroll(
      title: context.t('mobile.freigaben'),
      subtitle: open == null || waiting == 0 ? null : context.t('mobile.freigabenOffen', count: waiting),
      actions: widget.actions,
      onRefresh: _load,
      bottomClearance: widget.bottomClearance,
      compact: widget.compact,
      slivers: [
        if (open == null && _error != null)
          SliverToBoxAdapter(child: EmptyNote(context.t('mobile.freigabenFehler')))
        else if (open != null && open.isEmpty)
          SliverToBoxAdapter(child: _Empty())
        else
          for (final g in groups.entries) ...[
            SliverToBoxAdapter(
              child: SectionTitle(
                g.key,
                trailing: Text(
                  '${g.value.length}',
                  style: context.type.labelLarge?.copyWith(color: context.colors.textMuted),
                ),
              ),
            ),
            SliverPadding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              sliver: SliverList.separated(
                itemCount: g.value.length,
                separatorBuilder: (_, _) => const SizedBox(height: 10),
                itemBuilder: (context, i) {
                  final d = g.value[i];
                  return WorkflowDecisionCard(
                    key: ValueKey('workflow:${d.run}'),
                    api: widget.api,
                    decision: d,
                    decided: _decided[d.run],
                    onDecided: (option) => _onDecided(d, option),
                  );
                },
              ),
            ),
          ],
      ],
    );
  }
}

/// Nothing waits: said once, plainly, instead of an empty screen.
class _Empty extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Padding(
      padding: const EdgeInsets.fromLTRB(24, 48, 24, 24),
      child: Column(
        children: [
          Icon(Icons.check_circle_outline_rounded, size: 44, color: c.textMuted),
          const SizedBox(height: 14),
          Text(context.t('mobile.freigabenLeer'), textAlign: TextAlign.center, style: context.type.titleMedium),
          const SizedBox(height: 6),
          Text(
            context.t('mobile.freigabenLeerHinweis'),
            textAlign: TextAlign.center,
            style: context.type.bodyMedium?.copyWith(color: c.textSecondary),
          ),
        ],
      ),
    );
  }
}
