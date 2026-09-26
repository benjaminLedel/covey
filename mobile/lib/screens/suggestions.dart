import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:intl/intl.dart';

import '../activity.dart';
import '../api.dart';
import '../chrome.dart';
import '../i18n.dart';
import '../icons.dart';
import '../theme.dart';
import '../ui.dart';

/// Agent suggestions (#370): recurring work from the last two weeks of the
/// activity log that an agent could take over, each with the job posting
/// that makes it one. The evaluation is one control-plane turn and takes a
/// moment; its result is kept until it is run again.
class SuggestionsScreen extends StatefulWidget {
  const SuggestionsScreen({super.key, required this.api, required this.canHire});

  final CoveyApi api;

  /// Whether the seat may manage agents — the hiring flow needs it.
  final bool canHire;

  @override
  State<SuggestionsScreen> createState() => _SuggestionsScreenState();
}

class _SuggestionsScreenState extends State<SuggestionsScreen> {
  AgentSuggestions? _data;
  bool _loaded = false;
  bool _running = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final d = await widget.api.suggestions();
      if (mounted) {
        setState(() {
          _data = d;
          _loaded = true;
        });
      }
    } on ApiException catch (e) {
      if (mounted) {
        setState(() {
          _error = e.message;
          _loaded = true;
        });
      }
    }
  }

  Future<void> _run() async {
    final lang = Strings.of(context).language;
    setState(() {
      _running = true;
      _error = null;
    });
    try {
      if (ActivityRecorder.supported) await ActivityRecorder.instance.flush();
      final zone = CoveyApi.zoneQuery(tz: ActivityRecorder.supported ? ActivityRecorder.instance.timeZone : null);
      final d = await widget.api.suggest(zone, lang: lang);
      if (mounted) setState(() => _data = d);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _running = false);
    }
  }

  /// The posting, to edit before it goes — the suggestion is a starting
  /// point, the person knows the rules behind the work.
  Future<void> _hire(AgentSuggestion s) async {
    final t = context.t;
    final messenger = ScaffoldMessenger.of(context);
    final lang = Strings.of(context).language;
    final text = TextEditingController(text: s.brief);
    final go = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(s.title),
        content: SizedBox(
          width: 520,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(t('mobile.vorschlagAusschreibungHinweis'), style: context.type.bodySmall),
              const SizedBox(height: 12),
              TextField(controller: text, minLines: 6, maxLines: 14),
            ],
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(t('team.abbrechen'))),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(t('mobile.vorschlagAnlegen'))),
        ],
      ),
    );
    final description = text.text.trim();
    text.dispose();
    if (go != true || description.isEmpty) return;
    try {
      await widget.api.hiringBrief(description, lang: lang);
      messenger.showSnackBar(SnackBar(content: Text(t('mobile.vorschlagUnterwegs'))));
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  Future<void> _copy(AgentSuggestion s) async {
    final messenger = ScaffoldMessenger.of(context);
    final done = context.t('mobile.vorschlagKopiert');
    await Clipboard.setData(ClipboardData(text: s.brief));
    messenger.showSnackBar(SnackBar(content: Text(done)));
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final d = _data;
    final small = context.type.bodySmall?.copyWith(color: c.textMuted);
    return Scaffold(
      appBar: ChromeAppBar(
        title: Text(context.t('mobile.vorschlaege')),
        actions: [
          if (d != null) TextButton(onPressed: _running ? null : _run, child: Text(context.t('mobile.vorschlaegeNeu'))),
          const SizedBox(width: 8),
        ],
      ),
      body: !_loaded
          ? Center(child: Text(context.t('common.loading')))
          : ListView(
              padding: const EdgeInsets.fromLTRB(0, 12, 0, 40),
              children: [
                if (_running)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
                    child: Row(
                      children: [
                        const SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2)),
                        const SizedBox(width: 12),
                        Expanded(child: Text(context.t('mobile.vorschlaegeLaeuft'), style: context.type.bodyMedium)),
                      ],
                    ),
                  ),
                if (_error != null)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(20, 0, 20, 16),
                    child: Text(_error!, style: context.type.bodyMedium?.copyWith(color: c.textDanger)),
                  ),
                if (d == null && !_running)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(20, 8, 20, 0),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(context.t('mobile.vorschlaegeErklaerung'), style: context.type.bodyLarge),
                        const SizedBox(height: 16),
                        FilledButton(onPressed: _run, child: Text(context.t('mobile.vorschlaegeStarten'))),
                      ],
                    ),
                  ),
                if (d != null) ...[
                  Padding(
                    padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
                    child: Text(
                      context.t(
                        'mobile.vorschlaegeStand',
                        args: {
                          'days': d.days,
                          'sessions': d.sessions,
                          'date': DateFormat.yMMMd(Strings.of(context).language).add_Hm().format(d.createdAt),
                        },
                      ),
                      style: small,
                    ),
                  ),
                  if (d.items.isEmpty)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(20, 8, 20, 0),
                      child: Text(context.t('mobile.vorschlaegeKeine'), style: context.type.bodyLarge),
                    ),
                  for (final s in d.items) ...[
                    const SizedBox(height: 12),
                    _SuggestionCard(s: s, canHire: widget.canHire, onHire: () => _hire(s), onCopy: () => _copy(s)),
                  ],
                  Padding(
                    padding: const EdgeInsets.fromLTRB(32, 16, 32, 0),
                    child: Text(context.t('mobile.vorschlaegeHinweis'), style: small),
                  ),
                ],
              ],
            ),
    );
  }
}

class _SuggestionCard extends StatelessWidget {
  const _SuggestionCard({required this.s, required this.canHire, required this.onHire, required this.onCopy});

  final AgentSuggestion s;
  final bool canHire;
  final VoidCallback onHire;
  final VoidCallback onCopy;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final meta = [
      if (s.pattern.isNotEmpty) s.pattern,
      if (s.minutesPerWeek > 0) context.t('mobile.vorschlagMinuten', args: {'min': s.minutesPerWeek}),
    ].join(' · ');
    return InsetGroup(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 14, 16, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(s.title, style: context.type.titleMedium),
              if (meta.isNotEmpty) ...[
                const SizedBox(height: 2),
                Text(meta, style: context.type.bodySmall?.copyWith(color: c.textMuted)),
              ],
              if (s.description.isNotEmpty) ...[
                const SizedBox(height: 10),
                Text(s.description, style: context.type.bodyMedium),
              ],
              if (s.agent.isNotEmpty) ...[
                const SizedBox(height: 10),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Icon(AppIcons.summary.of(context), size: 16, color: c.textAccent),
                    const SizedBox(width: 8),
                    Expanded(child: Text(s.agent, style: context.type.bodyMedium)),
                  ],
                ),
              ],
              if (s.systems.isNotEmpty) ...[
                const SizedBox(height: 10),
                Wrap(
                  spacing: 6,
                  runSpacing: 6,
                  children: [
                    for (final sys in s.systems)
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                        decoration: BoxDecoration(color: c.surface1, borderRadius: BorderRadius.circular(999)),
                        child: Text(sys, style: context.type.labelMedium),
                      ),
                  ],
                ),
              ],
              if (s.evidence.isNotEmpty) ...[
                const SizedBox(height: 10),
                for (final e in s.evidence) Text('· $e', style: context.type.bodySmall?.copyWith(color: c.textMuted)),
              ],
              const SizedBox(height: 8),
              Row(
                children: [
                  if (canHire)
                    FilledButton.tonal(onPressed: onHire, child: Text(context.t('mobile.vorschlagAnlegen')))
                  else
                    Expanded(child: Text(context.t('mobile.vorschlagOhneRecht'), style: context.type.bodySmall)),
                  const SizedBox(width: 8),
                  TextButton(onPressed: onCopy, child: Text(context.t('mobile.vorschlagKopieren'))),
                ],
              ),
            ],
          ),
        ),
      ],
    );
  }
}
