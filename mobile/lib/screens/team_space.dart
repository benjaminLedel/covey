import 'package:flutter/material.dart';

import '../api.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../theme.dart';
import '../ui.dart';

/// The Team space: who waits, then who works. One scroll, where the web's
/// team shell has two lists — on a phone the question "does anybody need
/// me?" and "who is there?" are read in one glance, not two taps.
class TeamSpace extends StatefulWidget {
  const TeamSpace({
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

  /// The list pane of a wide window: no bar row above the title.
  final bool compact;

  @override
  State<TeamSpace> createState() => _TeamSpaceState();
}

class _TeamSpaceState extends State<TeamSpace> {
  ({InboxPage waiting, List<Agent> agents, List<Department> departments})? _data;
  Object? _error;
  String _query = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await Future.wait([widget.api.waiting(), widget.api.agents(), widget.api.departments()]);
      if (!mounted) return;
      setState(() {
        _data = (waiting: r[0] as InboxPage, agents: r[1] as List<Agent>, departments: r[2] as List<Department>);
        _error = null;
      });
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final data = _data;
    final pending = data?.waiting.pending ?? 0;
    return SpaceScroll(
      title: context.t('team.workspace'),
      subtitle: data == null
          ? null
          : pending == 0
          ? context.t('team.wartetNichts')
          : context.t('team.wartetLead', count: pending),
      actions: widget.actions,
      bottomClearance: widget.bottomClearance,
      compact: widget.compact,
      onRefresh: _load,
      search: SearchField(hint: context.t('team.suche'), onChanged: (v) => setState(() => _query = v)),
      slivers: [
        if (_error != null)
          SliverToBoxAdapter(child: EmptyNote(context.t('mobile.fehler', args: {'error': '$_error'}))),
        if (data == null && _error == null) SliverToBoxAdapter(child: EmptyNote(context.t('common.loading'))),
        if (data != null) ..._content(context, data),
      ],
    );
  }

  List<Widget> _content(
    BuildContext context,
    ({InboxPage waiting, List<Agent> agents, List<Department> departments}) data,
  ) {
    final byId = {for (final a in data.agents) a.id: a};
    // A seat that may only read sees who waits and who works, and opens
    // nothing: a conversation it cannot write in is a dead end (#339).
    final open = widget.me.canWrite;
    // Typing narrows what is already here — name, slug, role, department —
    // without asking the instance again.
    final q = _query.trim().toLowerCase();
    final deptName = {for (final d in data.departments) d.id: d.name.toLowerCase()};
    bool matches(Agent a) =>
        q.isEmpty ||
        a.displayName.toLowerCase().contains(q) ||
        a.slug.toLowerCase().contains(q) ||
        a.jobTitle.toLowerCase().contains(q) ||
        (deptName[a.departmentId] ?? '').contains(q);
    final waiting = data.waiting.items
        .where((e) => q.isEmpty || e.agentName.toLowerCase().contains(q) || e.title.toLowerCase().contains(q))
        .toList();
    final colleagues = data.agents.where((a) => !a.isApplicant && matches(a)).toList()
      ..sort((a, b) => a.displayName.toLowerCase().compareTo(b.displayName.toLowerCase()));
    final known = {for (final d in data.departments) d.id};
    final groups = [
      for (final d in data.departments)
        (name: d.name, members: colleagues.where((a) => a.departmentId == d.id).toList()),
      (
        name: context.t('team.ohneAbteilung'),
        members: colleagues.where((a) => a.departmentId == null || !known.contains(a.departmentId)).toList(),
      ),
    ].where((g) => g.members.isNotEmpty);

    return [
      if (q.isNotEmpty && waiting.isEmpty && colleagues.isEmpty)
        SliverToBoxAdapter(child: EmptyNote(context.t('team.nichtsGefunden'))),
      if (waiting.isNotEmpty) ...[
        SliverToBoxAdapter(child: SectionTitle(context.t('team.wartet'))),
        SliverList.separated(
          itemCount: waiting.length,
          separatorBuilder: (_, _) => const SizedBox(height: 10),
          itemBuilder: (context, i) {
            final e = waiting[i];
            final a = byId[e.agentId];
            final state = a == null ? FaceState.working : faceStateOf(killed: a.killed, status: a.status);
            return _WaitCard(
              entry: e,
              state: state,
              onTap: e.agentId.isEmpty || !open
                  ? null
                  : () => widget.onOpen(e.agentId, e.agentName, e.agentSlug, state),
            );
          },
        ),
      ],
      if (q.isEmpty && colleagues.isEmpty) SliverToBoxAdapter(child: EmptyNote(context.t('chat.noAgents'))),
      for (final g in groups) ...[
        SliverToBoxAdapter(child: SectionTitle(g.name)),
        SliverToBoxAdapter(
          child: InsetGroup(
            children: [
              for (final a in g.members)
                GroupRow(
                  leading: Face(
                    slug: a.slug,
                    state: faceStateOf(killed: a.killed, status: a.status),
                    size: 36,
                  ),
                  title: a.displayName,
                  subtitle: a.jobTitle.isEmpty ? a.slug : a.jobTitle,
                  // The state in words, not in a colour alone (spec/27).
                  trailing: Text(context.t('status.${a.killed ? 'killed' : a.status}'), style: context.type.labelSmall),
                  onTap: !open
                      ? null
                      : () =>
                            widget.onOpen(a.id, a.displayName, a.slug, faceStateOf(killed: a.killed, status: a.status)),
                ),
            ],
          ),
        ),
      ],
    ];
  }
}

/// A question or an approval that holds an agent still — a card, because it
/// is the one thing on the screen that asks for the person.
class _WaitCard extends StatelessWidget {
  const _WaitCard({required this.entry, required this.state, this.onTap});

  final InboxEntry entry;
  final FaceState state;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Material(
        color: c.surface2,
        borderRadius: BorderRadius.circular(20),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 12, 16),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (entry.agentSlug.isNotEmpty) Face(slug: entry.agentSlug, state: state, size: 42),
                const SizedBox(width: 14),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(entry.agentName, style: context.type.titleMedium),
                      const SizedBox(height: 4),
                      Text(entry.title, maxLines: 3, overflow: TextOverflow.ellipsis, style: context.type.bodyLarge),
                      const SizedBox(height: 8),
                      Text(context.t('inbox.type.${entry.type}'), style: context.type.labelSmall),
                    ],
                  ),
                ),
                if (onTap != null) Icon(AppIcons.chevron.of(context), color: c.textMuted),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
