import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../api.dart';
import '../chat_text.dart';
import '../i18n.dart';
import '../live.dart';
import '../models.dart';
import '../theme.dart';

// What waits for a person's decision, decided where it is shown (#622):
// an approval (spec/06), an open point of a review (spec/21), a
// configuration proposal drafted from the chat (#491) and a workflow's
// approval step (#564). The cards call the routes the web's cards call,
// with the same verbs, so that deciding here is deciding there.
//
// Which buttons a card offers follows the seat's role from /auth/me and the
// flags the server sends; the server checks again, and its 403 is final —
// the card then says so and offers nothing more.

/// The roles the decide routes take (internal/httpapi/server.go: manage and
/// security); `platform_admin` is the old name of `org_admin`.
const _decidingRoles = {'org_admin', 'platform_admin', 'agent_owner', 'security'};

/// Who may accept a proposal that touches ACCESS.md or EGRESS.md.
const _restrictedRoles = {'org_admin', 'platform_admin', 'security'};

bool mayDecide(Me me) => _decidingRoles.contains(me.role);

/// Controlling decides costs, nothing else (as the web's inbox): it sees the
/// approvals, not the open points of a review.
bool showsEntry(Me me, InboxEntry e) => e.type == 'approval' || me.role != 'controlling';

/// What a failed decision says. A 403 is the instance refusing this seat; a
/// 409 on an open point is a configuration that changed since the draft —
/// it has to be rewritten or discarded — unless it was decided meanwhile.
String decisionError(Strings s, Object e, {bool improvement = false}) {
  if (e is! ApiException) return '$e';
  if (e.status == 403) return s.t('mobile.darfNichtEntscheiden');
  if (e.status == 409) {
    if (improvement && !e.message.contains('already')) return s.t('improvements.blockedConflict');
    return s.t('mobile.schonEntschieden');
  }
  return e.message;
}

/// The open approvals and open points about one agent, as cards above the
/// input of its conversation — the web's "Waiting for you" (#391). A
/// proposal that stands in the conversation as its own card ([inThread]) is
/// not shown a second time. Draws nothing while nothing waits.
class DecisionPanel extends StatefulWidget {
  const DecisionPanel({
    super.key,
    required this.api,
    required this.me,
    required this.agentId,
    this.inThread = const {},
    this.onDecided,
  });

  final CoveyApi api;
  final Me me;
  final String agentId;
  final Set<String> inThread;

  /// Told after a decision went through: the agent wakes, and the thread has
  /// something new to read.
  final VoidCallback? onDecided;

  @override
  State<DecisionPanel> createState() => _DecisionPanelState();
}

class _DecisionPanelState extends State<DecisionPanel> {
  List<InboxEntry> _open = const [];
  StreamSubscription<void>? _live;
  Timer? _poll;
  var _reads = 0;

  @override
  void initState() {
    super.initState();
    _load();
    _live = LiveEvents.instance.of({'approval', 'task', 'chat'}, agentId: widget.agentId).listen((_) => _load());
    _poll = Timer.periodic(const Duration(minutes: 1), (_) => _load());
  }

  @override
  void dispose() {
    _live?.cancel();
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    final read = ++_reads;
    try {
      final page = await widget.api.waitingFor(widget.agentId);
      if (mounted && read == _reads) setState(() => _open = page.items.where((e) => e.pending).toList());
    } catch (_) {
      // The conversation stands without the cards; the next look asks again.
    }
  }

  void _decided(InboxEntry e) {
    setState(() => _open = _open.where((x) => x.id != e.id).toList());
    widget.onDecided?.call();
    unawaited(_load());
  }

  @override
  Widget build(BuildContext context) {
    final shown = _open.where((e) => showsEntry(widget.me, e) && !widget.inThread.contains(e.id)).toList();
    if (shown.isEmpty) return const SizedBox.shrink();
    final c = context.colors;
    return ConstrainedBox(
      constraints: BoxConstraints(maxHeight: MediaQuery.sizeOf(context).height * 0.45),
      child: DecoratedBox(
        decoration: BoxDecoration(
          border: Border(top: BorderSide(color: c.hairline)),
        ),
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(14, 10, 14, 4),
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 760),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Padding(
                    padding: const EdgeInsets.only(left: 4, bottom: 6),
                    child: Text(context.t('team.wartet'), style: context.type.titleSmall),
                  ),
                  for (final e in shown)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: e.type == 'approval' && e.approval != null
                          ? ApprovalCard(
                              key: ValueKey('approval:${e.id}'),
                              api: widget.api,
                              me: widget.me,
                              entry: e,
                              onDecided: () => _decided(e),
                            )
                          : e.item != null
                          ? ReviewPointCard(
                              key: ValueKey('item:${e.id}'),
                              api: widget.api,
                              me: widget.me,
                              entry: e,
                              onDecided: () => _decided(e),
                            )
                          : _Plain(entry: e),
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// The frame every decision card shares: the waiting tone, a kind chip and
/// a title.
class _Frame extends StatelessWidget {
  const _Frame({required this.kind, required this.title, required this.children});

  final String kind;
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Container(
      padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
      decoration: BoxDecoration(
        color: c.surface2,
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: c.bgWait, width: 1.4),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 1),
                decoration: BoxDecoration(color: c.bgWait, borderRadius: BorderRadius.circular(99)),
                child: Text(
                  kind,
                  style: context.type.labelSmall?.copyWith(color: c.textWait, fontWeight: FontWeight.w600),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(child: Text(title, style: context.type.titleSmall)),
            ],
          ),
          ...children,
        ],
      ),
    );
  }
}

class _Note extends StatelessWidget {
  const _Note(this.text, {this.danger = false});

  final String text;
  final bool danger;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Text(
      text,
      style: context.type.bodySmall?.copyWith(color: danger ? context.colors.textDanger : context.colors.textSecondary),
    ),
  );
}

/// An entry the app has no card for (a kind newer than the app): its title,
/// and the web decides.
class _Plain extends StatelessWidget {
  const _Plain({required this.entry});

  final InboxEntry entry;

  @override
  Widget build(BuildContext context) =>
      _Frame(kind: context.t('inbox.type.${entry.type}'), title: entry.title, children: const []);
}

/// The buttons of a card: one primary, one secondary, spaced and big enough
/// for a thumb.
Widget _buttons(BuildContext context, List<Widget> buttons) => Padding(
  padding: const EdgeInsets.only(top: 10),
  child: Wrap(spacing: 8, runSpacing: 8, children: buttons),
);

ButtonStyle _primary(BuildContext context) => FilledButton.styleFrom(
  minimumSize: const Size(44, 40),
  padding: const EdgeInsets.symmetric(horizontal: 18),
  shape: const StadiumBorder(),
);

ButtonStyle _secondary(BuildContext context) => OutlinedButton.styleFrom(
  minimumSize: const Size(44, 40),
  padding: const EdgeInsets.symmetric(horizontal: 18),
  shape: const StadiumBorder(),
);

/// Runs one decision for a card: busy while it runs, the error as the card
/// says it, and a 403 that ends the card's offer for good.
mixin _Deciding<T extends StatefulWidget> on State<T> {
  bool busy = false;
  String? error;
  bool refused = false;

  Future<bool> decide(Future<void> Function() call, {bool improvement = false}) async {
    final strings = Strings.of(context);
    setState(() {
      busy = true;
      error = null;
    });
    try {
      await call();
      return true;
    } catch (e) {
      if (mounted) {
        setState(() {
          error = decisionError(strings, e, improvement: improvement);
          refused = e is ApiException && e.status == 403;
        });
      }
      return false;
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }
}

/// An approval: here an agent waits. The action stands with its parameters,
/// not a summary — exactly this is what gets approved. A single passage of
/// prose may be corrected in place; approving then approves that version
/// (spec/24).
class ApprovalCard extends StatefulWidget {
  const ApprovalCard({super.key, required this.api, required this.me, required this.entry, required this.onDecided});

  final CoveyApi api;
  final Me me;
  final InboxEntry entry;
  final VoidCallback onDecided;

  @override
  State<ApprovalCard> createState() => _ApprovalCardState();
}

class _ApprovalCardState extends State<ApprovalCard> with _Deciding {
  late final String _prose = loneProse(widget.entry.approval!.params);
  TextEditingController? _edit;

  @override
  void dispose() {
    _edit?.dispose();
    super.dispose();
  }

  bool get _changed {
    final t = _edit?.text.trim() ?? '';
    return t.isNotEmpty && t != _prose.trim();
  }

  Future<void> _decide(bool approve) async {
    final text = approve && _changed ? _edit!.text : null;
    if (await decide(() => widget.api.decideApproval(widget.entry.approval!.id, approve, text: text))) {
      widget.onDecided();
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final a = widget.entry.approval!;
    final may = mayDecide(widget.me) && !refused;
    final editing = _edit != null;
    return _Frame(
      kind: context.t('inbox.type.approval'),
      title: widget.entry.title.isNotEmpty ? widget.entry.title : a.action,
      children: [
        _Note(context.t('inbox.agentWaiting')),
        const SizedBox(height: 8),
        if (editing) ...[
          TextField(
            key: const ValueKey('approval-text'),
            controller: _edit,
            minLines: 3,
            maxLines: 10,
            onChanged: (_) => setState(() {}),
            decoration: const InputDecoration(border: OutlineInputBorder(), isDense: true),
          ),
          _Note(context.t('approvals.editHint')),
        ] else
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: c.textPrimary.withValues(alpha: 0.04),
              borderRadius: BorderRadius.circular(10),
            ),
            child: SelectableText(
              const JsonEncoder.withIndent('  ').convert(a.params),
              style: context.type.bodySmall?.copyWith(fontFamily: 'monospace'),
            ),
          ),
        if (may)
          _buttons(context, [
            FilledButton(
              style: _primary(context),
              onPressed: busy ? null : () => _decide(true),
              child: Text(context.t(_changed ? 'approvals.approveCorrected' : 'approvals.approve')),
            ),
            OutlinedButton(
              style: _secondary(context),
              onPressed: busy ? null : () => _decide(false),
              child: Text(context.t('approvals.deny')),
            ),
            if (_prose.isNotEmpty && !editing)
              TextButton(
                onPressed: busy ? null : () => setState(() => _edit = TextEditingController(text: _prose)),
                child: Text(context.t('mobile.textKorrigieren')),
              ),
          ])
        else if (error == null)
          _Note(context.t('mobile.darfNichtEntscheiden')),
        if (error != null) _Note(error!, danger: true),
      ],
    );
  }
}

/// The one piece of prose in an action's parameters, or nothing — exactly
/// one string of twenty words or more, as the web decides it, so the field a
/// reviewer corrects is unambiguous.
String loneProse(Object? params) {
  final found = <String>[];
  void walk(Object? x) {
    if (x is String) {
      if (x.trim().split(RegExp(r'\s+')).length >= 20) found.add(x);
    } else if (x is List) {
      x.forEach(walk);
    } else if (x is Map) {
      x.values.forEach(walk);
    }
  }

  walk(params);
  return found.length == 1 ? found.single : '';
}

/// An open point of a review: proposal, finding, issue or tool request.
/// Accepting a proposal writes a configuration version; anything else is
/// marked done. Rejecting keeps the note as the reason.
class ReviewPointCard extends StatefulWidget {
  const ReviewPointCard({super.key, required this.api, required this.me, required this.entry, required this.onDecided});

  final CoveyApi api;
  final Me me;
  final InboxEntry entry;
  final VoidCallback onDecided;

  @override
  State<ReviewPointCard> createState() => _ReviewPointCardState();
}

class _ReviewPointCardState extends State<ReviewPointCard> with _Deciding {
  final _note = TextEditingController();

  @override
  void dispose() {
    _note.dispose();
    super.dispose();
  }

  Future<void> _decide(bool accept) async {
    final item = widget.entry.item!;
    if (await decide(() => widget.api.decideImprovement(item.id, accept, note: _note.text.trim()), improvement: true)) {
      widget.onDecided();
    }
  }

  @override
  Widget build(BuildContext context) {
    final item = widget.entry.item!;
    final conflict = item.conflicts.isNotEmpty;
    final may = mayDecide(widget.me) && !refused;
    final mayAccept = may && !conflict && (!item.needsSecurity || _restrictedRoles.contains(widget.me.role));
    return _Frame(
      kind: context.t('inbox.type.${item.kind}'),
      title: widget.entry.title,
      children: [
        if (item.rationale.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: ChatText(item.rationale, color: context.colors.textPrimary),
          ),
        if (item.link.isNotEmpty) _Note(item.link),
        if (item.needsSecurity) _Note(context.t('improvements.needsSecurity')),
        if (conflict)
          _Note(context.t('improvements.conflict', args: {'files': item.conflicts.join(', ')}), danger: true),
        if (may) ...[
          const SizedBox(height: 10),
          TextField(
            controller: _note,
            decoration: InputDecoration(
              hintText: context.t('improvements.notePlaceholder'),
              border: const OutlineInputBorder(),
              isDense: true,
            ),
          ),
          _buttons(context, [
            FilledButton(
              style: _primary(context),
              onPressed: busy || !mayAccept ? null : () => _decide(true),
              child: Text(context.t(item.kind == 'proposal' ? 'improvements.accept' : 'improvements.done')),
            ),
            OutlinedButton(
              style: _secondary(context),
              onPressed: busy ? null : () => _decide(false),
              child: Text(context.t('improvements.reject')),
            ),
          ]),
          if (!mayAccept) _Note(context.t(conflict ? 'improvements.blockedConflict' : 'improvements.blockedSecurity')),
        ] else if (error == null)
          _Note(context.t('mobile.darfNichtEntscheiden')),
        if (error != null) _Note(error!, danger: true),
      ],
    );
  }
}

/// A configuration proposal in the conversation it was drafted in (#491):
/// what changes and why, a line when it widens access, Accept and Decline
/// for whoever may decide — the server says who ([ProposalCard.canDecide],
/// [ProposalCard.canAccept]) — and "waits for …" for everybody else; once
/// decided, who and when.
class ProposalCardView extends StatefulWidget {
  const ProposalCardView({super.key, required this.api, required this.card, this.onDecided});

  final CoveyApi api;
  final ProposalCard card;
  final VoidCallback? onDecided;

  @override
  State<ProposalCardView> createState() => _ProposalCardViewState();
}

class _ProposalCardViewState extends State<ProposalCardView> with _Deciding {
  /// Decided here, before the conversation's next read brings the card
  /// anew.
  String? _decided;

  Future<void> _decide(bool accept) async {
    if (await decide(() => widget.api.decideImprovement(widget.card.id, accept), improvement: true)) {
      if (mounted) setState(() => _decided = accept ? 'accepted' : 'rejected');
      widget.onDecided?.call();
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = widget.card;
    final status = p.pending && _decided != null ? _decided! : p.status;
    final conflict = p.conflicts.isNotEmpty;
    final lang = Strings.of(context).language;
    final when = p.decidedAt == null ? '' : DateFormat.yMMMd(lang).add_Hm().format(p.decidedAt!);
    return _Frame(
      kind: context.t('chatProposal.label'),
      title: p.title,
      children: [
        if (p.rationale.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: ChatText(p.rationale, color: context.colors.textPrimary),
          ),
        if (p.widens.isNotEmpty) _Note(context.t('chatProposal.widens', args: {'files': p.widens.join(', ')})),
        if (conflict && status == 'pending')
          _Note(context.t('improvements.conflict', args: {'files': p.conflicts.join(', ')}), danger: true),
        if (status != 'pending')
          _Note(
            context.t(
              status == 'accepted' ? 'chatProposal.acceptedBy' : 'chatProposal.declinedBy',
              args: {'name': p.decidedBy.isEmpty ? '…' : p.decidedBy, 'when': when},
            ),
          )
        else if (p.canDecide && !refused) ...[
          _buttons(context, [
            FilledButton(
              style: _primary(context),
              onPressed: busy || !p.canAccept ? null : () => _decide(true),
              child: Text(context.t('chatProposal.accept')),
            ),
            OutlinedButton(
              style: _secondary(context),
              onPressed: busy ? null : () => _decide(false),
              child: Text(context.t('chatProposal.decline')),
            ),
          ]),
          if (!p.canAccept)
            _Note(context.t(conflict ? 'improvements.blockedConflict' : 'improvements.blockedSecurity')),
        ] else if (error == null)
          _Note(
            p.approvers.isEmpty
                ? context.t('chatProposal.waiting')
                : context.t('chatProposal.waitingFor', args: {'names': p.approvers.join(', ')}),
          ),
        if (error != null) _Note(error!, danger: true),
      ],
    );
  }
}

/// A workflow's approval step put to the person (#564): the question, and
/// one button per option. GET /workflows/mine lists only the steps that name
/// the person, so the buttons are offered; the server still checks.
class WorkflowDecisionCard extends StatefulWidget {
  const WorkflowDecisionCard({super.key, required this.api, required this.decision, this.onDecided});

  final CoveyApi api;
  final WorkflowDecision decision;
  final VoidCallback? onDecided;

  @override
  State<WorkflowDecisionCard> createState() => _WorkflowDecisionCardState();
}

class _WorkflowDecisionCardState extends State<WorkflowDecisionCard> with _Deciding {
  String? _chosen;

  Future<void> _decide(String option) async {
    final d = widget.decision;
    if (await decide(() => widget.api.decideWorkflow(d.workflow, d.run, option))) {
      if (mounted) setState(() => _chosen = option);
      widget.onDecided?.call();
    }
  }

  @override
  Widget build(BuildContext context) {
    final d = widget.decision;
    final question = d.question.isNotEmpty ? d.question : (d.title.isNotEmpty ? d.title : d.caseRef);
    return _Frame(
      kind: [context.t('workflows.chat.kindApproval'), if (d.caseRef.isNotEmpty) d.caseRef].join(' · '),
      title: question,
      children: [
        _Note([d.name, if (d.step.isNotEmpty) d.step].join(' · ')),
        if (_chosen != null)
          _Note(context.t('workflows.chat.decidedAs', args: {'choice': _chosen}))
        else if (!refused)
          _buttons(context, [
            for (final o in d.options)
              OutlinedButton(style: _secondary(context), onPressed: busy ? null : () => _decide(o), child: Text(o)),
          ]),
        if (error != null) _Note(error!, danger: true),
      ],
    );
  }
}
