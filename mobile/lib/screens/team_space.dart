import 'dart:async';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../api.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../push.dart';
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

  /// What the person has not read, per agent (#378). Empty on an instance
  /// that does not keep it.
  Map<String, ThreadState> _threads = const {};
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _load();
    // New answers turn up without a pull; a thread just read drops its badge.
    _poll = Timer.periodic(const Duration(seconds: 20), (_) => _loadThreads());
    threadsRead.addListener(_loadThreads);
  }

  @override
  void dispose() {
    _poll?.cancel();
    threadsRead.removeListener(_loadThreads);
    super.dispose();
  }

  Future<void> _loadThreads() async {
    try {
      final t = await widget.api.threads();
      // The app icon carries the same number as the list (#379).
      unawaited(PushNotices.instance.badge(t.values.fold<int>(0, (n, e) => n + e.unread)));
      if (mounted) setState(() => _threads = t);
    } catch (_) {
      // No badges rather than an error: the list itself is what matters.
    }
  }

  Future<void> _load() async {
    try {
      unawaited(_loadThreads());
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
    // Who has something unread comes first, the newest on top; then the
    // rest by name.
    int unread(Agent a) => _threads[a.id]?.unread ?? 0;
    final colleagues = data.agents.where((a) => !a.isApplicant && matches(a)).toList()
      ..sort((a, b) {
        final ua = unread(a) > 0, ub = unread(b) > 0;
        if (ua != ub) return ua ? -1 : 1;
        if (ua) return _threads[b.id]!.lastAt?.compareTo(_threads[a.id]!.lastAt ?? DateTime(0)) ?? 0;
        return a.displayName.toLowerCase().compareTo(b.displayName.toLowerCase());
      });
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
                _AgentRow(
                  agent: a,
                  thread: _threads[a.id],
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

/// A colleague in the list. With unread entries (#378) the row says so three
/// ways — the name in bold, the newest line as its subtitle with its time,
/// and a count — so that it does not rest on colour alone (spec/27).
class _AgentRow extends StatelessWidget {
  const _AgentRow({required this.agent, required this.thread, required this.onTap});

  final Agent agent;
  final ThreadState? thread;
  final VoidCallback? onTap;

  String _when(BuildContext context, DateTime at) {
    final lang = Strings.of(context).language;
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    if (!at.isBefore(today)) return DateFormat.Hm(lang).format(at);
    if (!at.isBefore(today.subtract(const Duration(days: 6)))) return DateFormat.E(lang).format(at);
    return DateFormat.MMMd(lang).format(at);
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final a = agent;
    final n = thread?.unread ?? 0;
    final state = faceStateOf(killed: a.killed, status: a.status);
    // The state in words, not in a colour alone (spec/27).
    final status = Text(context.t('status.${a.killed ? 'killed' : a.status}'), style: context.type.labelSmall);
    if (n == 0) {
      return GroupRow(
        leading: Face(slug: a.slug, state: state, size: 36),
        title: a.displayName,
        subtitle: a.jobTitle.isEmpty ? a.slug : a.jobTitle,
        trailing: status,
        onTap: onTap,
      );
    }
    final t = thread!;
    final line = t.lastText.isEmpty ? (a.jobTitle.isEmpty ? a.slug : a.jobTitle) : t.lastText;
    return Semantics(
      label: context.t('team.ungelesen', count: n),
      child: InkWell(
        onTap: onTap,
        child: ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 62),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
            child: Row(
              children: [
                SizedBox(
                  width: 36,
                  child: Center(
                    child: Face(slug: a.slug, state: state, size: 36),
                  ),
                ),
                const SizedBox(width: 14),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Expanded(
                            child: Text(
                              a.displayName,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: context.type.titleMedium?.copyWith(fontWeight: FontWeight.w700),
                            ),
                          ),
                          if (t.lastAt != null)
                            Text(
                              _when(context, t.lastAt!),
                              style: context.type.labelSmall?.copyWith(
                                color: c.textAccent,
                                fontFeatures: const [FontFeature.tabularFigures()],
                              ),
                            ),
                        ],
                      ),
                      const SizedBox(height: 2),
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Expanded(
                            child: Text(
                              line,
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: context.type.bodyMedium?.copyWith(color: c.textPrimary),
                            ),
                          ),
                          const SizedBox(width: 10),
                          Column(
                            crossAxisAlignment: CrossAxisAlignment.end,
                            children: [
                              Container(
                                constraints: const BoxConstraints(minWidth: 22),
                                height: 22,
                                padding: const EdgeInsets.symmetric(horizontal: 7),
                                alignment: Alignment.center,
                                decoration: BoxDecoration(
                                  color: c.textPrimary,
                                  borderRadius: BorderRadius.circular(11),
                                ),
                                child: Text(
                                  n > 99 ? '99+' : '$n',
                                  style: context.type.labelMedium?.copyWith(
                                    color: c.surface2,
                                    fontFeatures: const [FontFeature.tabularFigures()],
                                  ),
                                ),
                              ),
                              const SizedBox(height: 4),
                              status,
                            ],
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
