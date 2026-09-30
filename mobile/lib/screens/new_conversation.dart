import 'package:flutter/material.dart';

import '../api.dart';
import '../chrome.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../photo.dart';
import '../theme.dart';
import '../ui.dart';

/// Starting a conversation (#440), as the web's dialog does it
/// (web/src/team/NeuesGespraech.tsx): one person or colleague picked is a
/// direct conversation — the one that exists, or a new one; several are a
/// group, which needs a name. The agents offered are the ones the
/// organisation's reach lets the person write to; a group with agents needs
/// the right to hand over work by hand besides (the server says the same).
///
/// Pops with the conversation it opened.
class NewConversationScreen extends StatefulWidget {
  const NewConversationScreen({super.key, required this.api, required this.me});

  final CoveyApi api;
  final Me me;

  @override
  State<NewConversationScreen> createState() => _NewConversationScreenState();
}

class _NewConversationScreenState extends State<NewConversationScreen> {
  OrgChart? _chart;
  Set<String> _reachable = const {};
  Object? _loadError;
  String _query = '';
  final _title = TextEditingController();
  final _picked = <MemberRef>[];
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _title.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final chart = await widget.api.orgChart();
      // Without an answer about the reach no agent is offered, as on the
      // web: a door the server then closes is worse than none.
      var reachable = <String>{};
      try {
        reachable = await widget.api.reachableAgents();
      } on ApiException catch (_) {}
      if (mounted) {
        setState(() {
          _chart = chart;
          _reachable = reachable;
        });
      }
    } catch (e) {
      if (mounted) setState(() => _loadError = e);
    }
  }

  bool get _group => _picked.length > 1;

  /// A group with an agent is the manage roles' (conversations.go).
  bool get _groupLocked => _group && _picked.any((p) => !p.human) && !widget.me.canWrite;

  bool get _ready => _picked.isNotEmpty && (!_group || _title.text.trim().isNotEmpty) && !_groupLocked && !_busy;

  void _toggle(MemberRef r) => setState(() {
    if (!_picked.remove(r)) _picked.add(r);
    _error = null;
  });

  ({String name, String slug}) _nameOf(MemberRef r) {
    final chart = _chart;
    if (chart == null) return (name: '', slug: '');
    if (r.human) return (name: chart.humans.where((h) => h.id == r.id).firstOrNull?.displayName ?? '', slug: '');
    final a = chart.agents.where((a) => a.id == r.id).firstOrNull;
    return (name: a?.displayName ?? '', slug: a?.slug ?? '');
  }

  Future<void> _start() async {
    if (!_ready) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final c = _group
          ? await widget.api.createGroup(_title.text.trim(), List.of(_picked))
          : await widget.api.openDirect(_picked.single);
      if (mounted) Navigator.of(context).pop(c);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final chart = _chart;
    final q = _query.trim().toLowerCase();
    bool matches(String name, [String extra = '']) => q.isEmpty || '$name $extra'.toLowerCase().contains(q);
    final people = chart == null
        ? const <OrgHuman>[]
        : chart.humans.where((h) => h.id != widget.me.id && matches(h.displayName, h.email)).toList();
    final agents = chart == null
        ? const <Agent>[]
        : chart.agents
              .where((a) => a.isColleague && !a.killed && _reachable.contains(a.id) && matches(a.displayName, a.slug))
              .toList();

    Widget row(MemberRef r, Widget leading, String name, String below) {
      final on = _picked.contains(r);
      return Semantics(
        checked: on,
        child: GroupRow(
          leading: leading,
          title: name,
          subtitle: below,
          trailing: Icon(
            (on ? AppIcons.picked : AppIcons.unpicked).of(context),
            color: on ? c.textPrimary : c.textMuted,
          ),
          onTap: () => _toggle(r),
        ),
      );
    }

    return Scaffold(
      appBar: ChromeAppBar(title: Text(context.t('conversation.newTitle'))),
      body: SafeArea(
        child: Column(
          children: [
            Expanded(
              child: ListView(
                padding: const EdgeInsets.only(bottom: 16),
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
                    child: Text(context.t('conversation.newLead'), style: context.type.bodyMedium),
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: SearchField(
                      hint: _picked.isEmpty ? context.t('conversation.search') : context.t('conversation.searchMore'),
                      onChanged: (v) => setState(() => _query = v),
                    ),
                  ),
                  // What is picked stays in sight while one keeps searching,
                  // the way a mail's To line works.
                  if (_picked.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
                      child: Wrap(
                        spacing: 6,
                        runSpacing: 6,
                        children: [
                          for (final p in _picked)
                            Builder(
                              builder: (context) {
                                final n = _nameOf(p);
                                return InputChip(
                                  avatar: p.human
                                      ? PersonPhoto(api: null, humanId: p.id, photoId: null, name: n.name, size: 22)
                                      : Face(slug: n.slug, size: 22),
                                  label: Text(n.name, overflow: TextOverflow.ellipsis),
                                  onDeleted: () => _toggle(p),
                                  deleteButtonTooltipMessage: context.t(
                                    'conversation.removePicked',
                                    args: {'name': n.name},
                                  ),
                                  backgroundColor: c.surface2,
                                  side: BorderSide(color: c.hairline),
                                  shape: const StadiumBorder(),
                                );
                              },
                            ),
                        ],
                      ),
                    ),
                  // The name only once there is a group to name.
                  if (_group)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
                      child: TextField(
                        controller: _title,
                        maxLength: 120,
                        textCapitalization: TextCapitalization.sentences,
                        onChanged: (_) => setState(() {}),
                        decoration: InputDecoration(
                          labelText: context.t('conversation.groupTitle'),
                          hintText: context.t('conversation.groupTitlePlaceholder'),
                          helperText: _title.text.trim().isEmpty ? context.t('conversation.groupTitleNeeded') : null,
                          counterText: '',
                        ),
                      ),
                    ),
                  if (_loadError != null)
                    EmptyNote(context.t('mobile.fehler', args: {'error': '$_loadError'}))
                  else if (chart == null)
                    EmptyNote(context.t('common.loading')),
                  if (people.isNotEmpty) ...[
                    SectionTitle(context.t('conversation.people')),
                    InsetGroup(
                      children: [
                        for (final h in people)
                          row(
                            MemberRef('human', h.id),
                            PersonPhoto(
                              api: widget.api,
                              humanId: h.id,
                              photoId: h.photoId,
                              name: h.displayName,
                              size: 36,
                            ),
                            h.displayName,
                            h.jobTitle.isEmpty ? h.email : h.jobTitle,
                          ),
                      ],
                    ),
                  ],
                  if (agents.isNotEmpty) ...[
                    SectionTitle(context.t('conversation.agents')),
                    InsetGroup(
                      children: [
                        for (final a in agents)
                          row(
                            MemberRef('agent', a.id),
                            Face(
                              slug: a.slug,
                              state: faceStateOf(killed: a.killed, status: a.status),
                              size: 36,
                            ),
                            a.displayName,
                            a.jobTitle.isEmpty ? a.slug : a.jobTitle,
                          ),
                      ],
                    ),
                  ],
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (_groupLocked)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: Text(context.t('conversation.groupAgentsManage'), style: context.type.bodySmall),
                    ),
                  if (_error != null)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: Text(_error!, style: context.type.bodyMedium?.copyWith(color: c.textDanger)),
                    ),
                  FilledButton(
                    onPressed: _ready ? _start : null,
                    child: Text(_group ? context.t('conversation.startGroup') : context.t('conversation.startDirect')),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
