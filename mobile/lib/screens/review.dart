import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../activity.dart';
import '../api.dart';
import '../chrome.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../theme.dart';
import '../ui.dart';

/// The daily reviews (#368): the days the Mac recorded, newest first, each
/// with its review or the offer to write one. A day's review is written into
/// its own note; written again, it replaces it. Tapping a day with a review
/// opens it; "Rewrite" writes it anew from the day's activity — for today,
/// with what has happened since.
class ReviewScreen extends StatefulWidget {
  const ReviewScreen({super.key, required this.api, required this.onOpen});

  final CoveyApi api;

  /// Opens a note — the caller knows where (a pane or a page).
  final void Function(Note note) onOpen;

  @override
  State<ReviewScreen> createState() => _ReviewScreenState();
}

class _ReviewScreenState extends State<ReviewScreen> {
  List<ActivityDay>? _days;
  String? _error;
  String? _busy;

  String get _zone => CoveyApi.zoneQuery(tz: ActivityRecorder.supported ? ActivityRecorder.instance.timeZone : null);

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      // What is still held on the Mac goes first, so today is complete.
      if (ActivityRecorder.supported) await ActivityRecorder.instance.flush();
      final days = await widget.api.activityDays(_zone);
      if (mounted) setState(() => _days = days);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    }
  }

  String _dayLabel(BuildContext context, String day) {
    final d = DateTime.parse(day);
    final today = DateTime.now();
    final t0 = DateTime(today.year, today.month, today.day);
    if (d == t0) return context.t('mobile.heute');
    if (d == t0.subtract(const Duration(days: 1))) return context.t('team.gestern');
    return DateFormat.yMMMMEEEEd(Strings.of(context).language).format(d);
  }

  Future<void> _write(ActivityDay d) async {
    final messenger = ScaffoldMessenger.of(context);
    final lang = Strings.of(context).language;
    final title = context.t(
      'mobile.aktivRueckblickTitel',
      args: {'date': DateFormat.yMMMMd(lang).format(DateTime.parse(d.day))},
    );
    setState(() => _busy = d.day);
    try {
      final note = await widget.api.activityReview(d.day, _zone, lang: lang, title: title);
      if (!mounted) return;
      setState(() => _busy = null);
      await _load();
      widget.onOpen(note);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _busy = null);
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  Future<void> _open(ActivityDay d) async {
    final id = d.review;
    if (id == null) return _write(d);
    try {
      widget.onOpen(await widget.api.note(id));
    } on ApiException catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final days = _days;
    return Scaffold(
      appBar: ChromeAppBar(title: Text(context.t('mobile.rueckblickTage'))),
      body: _error != null
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(_error!, style: context.type.bodyLarge?.copyWith(color: c.textDanger)),
              ),
            )
          : days == null
          ? Center(child: Text(context.t('common.loading')))
          : days.isEmpty
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(32),
                child: Text(
                  context.t('mobile.rueckblickLeer'),
                  textAlign: TextAlign.center,
                  style: context.type.bodyLarge?.copyWith(color: c.textMuted),
                ),
              ),
            )
          : ListView(
              padding: const EdgeInsets.only(top: 12, bottom: 40),
              children: [
                InsetGroup(
                  dividerIndent: 14,
                  children: [
                    for (final d in days)
                      GroupRow(
                        title: _dayLabel(context, d.day),
                        subtitle: [
                          context.t('mobile.rueckblickAbschnitte', args: {'count': d.sessions}),
                          if (d.review != null) context.t('mobile.rueckblickVorhanden'),
                        ].join(' · '),
                        onTap: _busy == null ? () => _open(d) : null,
                        trailing: _busy == d.day
                            ? const SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2))
                            : d.review == null
                            ? Icon(AppIcons.chevron.of(context), color: c.textMuted, size: 18)
                            : TextButton(
                                onPressed: _busy == null ? () => _write(d) : null,
                                child: Text(context.t('mobile.rueckblickNeu')),
                              ),
                      ),
                  ],
                ),
                Padding(
                  padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
                  child: Text(
                    context.t('mobile.rueckblickHinweis'),
                    style: context.type.bodySmall?.copyWith(color: c.textMuted),
                  ),
                ),
              ],
            ),
    );
  }
}
