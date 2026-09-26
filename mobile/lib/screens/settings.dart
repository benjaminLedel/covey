import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:hotkey_manager/hotkey_manager.dart';

import '../chrome.dart';
import '../api.dart';
import '../activity.dart';
import '../anywhere.dart';
import '../diagnostics.dart';
import '../dictation.dart';
import '../dictation_view.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../photo.dart';
import '../speech_model.dart';
import '../theme.dart';
import '../ui.dart';
import 'notes.dart' show NotePage, dictationFailure;
import 'review.dart';
import 'suggestions.dart';

/// Settings (#349): who is signed in where, the speech model on this
/// device and the language it listens for, a place to try dictation, and
/// the way out. Reached from the person's initials.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key, required this.api, required this.me, required this.onDisconnect, this.onChanged});

  final CoveyApi api;
  final Me me;
  final VoidCallback onDisconnect;

  /// Told about a change to the seat — a new photo (#377).
  final ValueChanged<Me>? onChanged;

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  final _model = SpeechModel.instance;

  /// The seat after a change made here; null while it is as it came.
  Me? _me;

  @override
  void initState() {
    super.initState();
    _model.addListener(_changed);
    _model.loadPrefs().then((_) => _model.refresh(widget.api));
    Diagnostics.instance.addListener(_changed);
    if (ActivityRecorder.supported) {
      ActivityRecorder.instance.addListener(_changed);
      ActivityRecorder.instance.refreshCount();
    }
    if (DictateAnywhere.supported) {
      DictateAnywhere.instance.addListener(_changed);
      DictateAnywhere.instance.refresh();
    }
    _measureLog();
  }

  @override
  void dispose() {
    Diagnostics.instance.removeListener(_changed);
    if (DictateAnywhere.supported) DictateAnywhere.instance.removeListener(_changed);
    if (ActivityRecorder.supported) ActivityRecorder.instance.removeListener(_changed);
    _model.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  int? _logBytes;

  Future<void> _measureLog() async {
    final n = await Diagnostics.instance.size();
    if (mounted) setState(() => _logBytes = n);
  }

  Future<void> _copyLog() async {
    final messenger = ScaffoldMessenger.of(context);
    final done = context.t('mobile.protokollKopiert');
    final text = await Diagnostics.instance.read();
    await Clipboard.setData(ClipboardData(text: text));
    messenger.showSnackBar(SnackBar(content: Text(done)));
  }

  Future<void> _clearLog() async {
    await Diagnostics.instance.clear();
    await _measureLog();
  }

  /// The model as people know it.
  String _modelName(SpeechModelInfo m) => switch (m.engine) {
    'parakeet' => 'Parakeet',
    'sensevoice' => 'SenseVoice',
    _ => m.name,
  };

  String _mb(int bytes) => '${(bytes / 1000000).round()} MB';

  String _modelLine(BuildContext context) {
    final i = _model.info;
    if (_model.problem == SpeechModelProblem.off) return context.t('mobile.spracheAus');
    if (i == null) return context.t('common.loading');
    final name = '${_modelName(i)} · ${_mb(i.size)}';
    final p = _model.progress;
    final pct = p == null ? '…' : '${(p * 100).floor()} %';
    if (_model.onInstance) return context.t('mobile.instanzLaedtModell', args: {'pct': pct});
    if (_model.downloading) return '$name · $pct';
    if (_model.onDevice) return context.t('mobile.modellAufGeraet', args: {'name': name});
    if (_model.problem != null && _model.detail != null) return '$name · ${_model.detail}';
    return context.t('mobile.modellNichtGeladen', args: {'name': name});
  }

  /// The models the instance offers, smallest first, each with what it is
  /// good for; picking one starts its download right away.
  Future<void> _pickModel() async {
    final i = _model.info;
    // Speech models only: the speakers' model is not a choice (#367).
    final models = [
      for (final m in i?.models ?? const <SpeechModelInfo>[])
        if (m.engine != 'speaker') m,
    ];
    if (i == null || models.isEmpty) return;
    // The model in use — chosen, or picked for the app's language.
    final current = i.name;
    String label(SpeechModelInfo m) {
      final name = '${_modelName(m)} · ${_mb(m.size)}';
      return m.name == i.defaultName ? '$name (${context.t('mobile.vorgabe')})' : name;
    }

    String? picked;
    if (isApple(context)) {
      await showCupertinoModalPopup<void>(
        context: context,
        builder: (context) => CupertinoActionSheet(
          title: Text(context.t('mobile.sprachmodell')),
          message: Text(context.t('mobile.modellWahlHinweis')),
          actions: [
            for (final m in models)
              CupertinoActionSheetAction(
                isDefaultAction: m.name == current,
                onPressed: () {
                  picked = m.name;
                  Navigator.pop(context);
                },
                child: Column(
                  children: [
                    Text(label(m)),
                    Text(
                      context.t('mobile.modell_${m.name}'),
                      style: context.type.bodySmall?.copyWith(color: context.colors.textMuted),
                    ),
                  ],
                ),
              ),
          ],
          cancelButton: CupertinoActionSheetAction(
            onPressed: () => Navigator.pop(context),
            child: Text(context.t('team.abbrechen')),
          ),
        ),
      );
    } else {
      await showModalBottomSheet<void>(
        context: context,
        builder: (context) => SafeArea(
          child: ListView(
            shrinkWrap: true,
            children: [
              for (final m in models)
                ListTile(
                  title: Text(label(m)),
                  subtitle: Text(context.t('mobile.modell_${m.name}')),
                  trailing: m.name == current ? const Icon(Icons.check_rounded) : null,
                  onTap: () {
                    picked = m.name;
                    Navigator.pop(context);
                  },
                ),
            ],
          ),
        ),
      );
    }
    final name = picked;
    if (name == null || name == current) return;
    await _model.choose(name == i.defaultName ? null : name);
    // Fetched now, not at the next dictation: that is what somebody who
    // just picked a model expects to see happen.
    unawaited(_model.ensure(widget.api).then((_) => _model.refresh(widget.api)));
  }

  Future<void> _disconnect() async {
    final t = context.t;
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(t('mobile.trennen')),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(t('team.abbrechen'))),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(t('mobile.trennen'))),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    Navigator.of(context).popUntil((r) => r.isFirst);
    widget.onDisconnect();
  }

  /// The activity log (#363): on and off, pause, today's review, deleting —
  /// Mac only.
  List<Widget> _activity(BuildContext context, TextStyle? small) {
    final a = ActivityRecorder.instance;
    final c = context.colors;
    return [
      SectionTitle(context.t('mobile.aktivitaeten')),
      InsetGroup(
        dividerIndent: 14,
        children: [
          GroupRow(
            title: context.t('mobile.aktivMitschreiben'),
            subtitle: !a.enabled
                ? null
                : a.paused
                ? context.t('mobile.aktivPausiert')
                : context.t('mobile.aktivHeute', args: {'count': a.today}),
            trailing: Switch.adaptive(value: a.enabled, onChanged: a.setEnabled),
          ),
          // Window, page and field come through the Accessibility
          // permission; without it only the apps' names are recorded.
          if (a.enabled && DictateAnywhere.supported && !DictateAnywhere.instance.trusted)
            GroupRow(
              title: context.t('mobile.bedienungshilfen'),
              subtitle: context.t('mobile.nichtFreigegeben'),
              trailing: TextButton(
                onPressed: DictateAnywhere.instance.askTrust,
                child: Text(context.t('mobile.freigeben')),
              ),
            ),
          if (a.enabled)
            GroupRow(
              title: a.paused ? context.t('mobile.aktivFortsetzen') : context.t('mobile.aktivPause'),
              onTap: a.togglePause,
            ),
          GroupRow(
            title: context.t('mobile.aktivRueckblick'),
            trailing: Icon(AppIcons.chevron.of(context), color: c.textMuted, size: 18),
            onTap: _review,
          ),
          GroupRow(
            title: context.t('mobile.vorschlaege'),
            trailing: Icon(AppIcons.chevron.of(context), color: c.textMuted, size: 18),
            onTap: () => Navigator.of(context).push(
              MaterialPageRoute<void>(
                builder: (_) => SuggestionsScreen(api: widget.api, canHire: widget.me.canWrite),
              ),
            ),
          ),
          GroupRow(title: context.t('mobile.aktivLoeschenHeute'), onTap: () => _deleteActivity(all: false)),
          GroupRow(title: context.t('mobile.aktivLoeschenAlles'), onTap: () => _deleteActivity(all: true)),
        ],
      ),
      Padding(
        padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
        child: Text(context.t('mobile.aktivHinweis'), style: small),
      ),
    ];
  }

  /// The daily reviews, any recorded day (#368).
  Future<void> _review() async {
    final nav = Navigator.of(context);
    await nav.push<void>(
      MaterialPageRoute(
        builder: (_) => ReviewScreen(
          api: widget.api,
          onOpen: (note) => nav.push(
            MaterialPageRoute<void>(
              builder: (_) => NotePage(api: widget.api, note: note),
            ),
          ),
        ),
      ),
    );
    await ActivityRecorder.instance.refreshCount();
  }

  Future<void> _deleteActivity({required bool all}) async {
    final t = context.t;
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(all ? t('mobile.aktivLoeschenAlles') : t('mobile.aktivLoeschenHeute')),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(t('team.abbrechen'))),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(t('mobile.loeschen'))),
        ],
      ),
    );
    if (ok != true) return;
    try {
      await (all ? ActivityRecorder.instance.deleteAll() : ActivityRecorder.instance.deleteToday());
    } on ApiException catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  /// A new shortcut, recorded as it is pressed. The current one is let go
  /// meanwhile, so pressing it records it rather than starting a dictation.
  Future<void> _recordHotKey() async {
    final a = DictateAnywhere.instance;
    await a.pause();
    HotKey? recorded;
    if (!mounted) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setDialog) => AlertDialog(
          title: Text(context.t('mobile.tastenkuerzel')),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(context.t('mobile.kuerzelAufnehmen')),
              const SizedBox(height: 16),
              HotKeyRecorder(initalHotKey: a.hotKey, onHotKeyRecorded: (k) => setDialog(() => recorded = k)),
            ],
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(context, false), child: Text(context.t('team.abbrechen'))),
            FilledButton(
              onPressed: (recorded?.modifiers ?? const []).isEmpty ? null : () => Navigator.pop(context, true),
              child: Text(context.t('mobile.uebernehmen')),
            ),
          ],
        ),
      ),
    );
    final k = recorded;
    if (ok == true && k != null) {
      await a.setHotKey(k);
    } else {
      await a.resume();
    }
  }

  /// Dictate anywhere (#355): the shortcut, the permission it needs, and
  /// the cleanup — Mac only.
  List<Widget> _anywhere(BuildContext context, TextStyle? small) {
    final a = DictateAnywhere.instance;
    final c = context.colors;
    return [
      SectionTitle(context.t('mobile.ueberall')),
      InsetGroup(
        dividerIndent: 14,
        children: [
          GroupRow(
            title: context.t('mobile.ueberallSchalter'),
            trailing: Switch.adaptive(value: a.enabled, onChanged: a.setEnabled),
          ),
          GroupRow(
            title: context.t('mobile.tastenkuerzel'),
            subtitle: a.label(space: context.t('mobile.leertaste')),
            trailing: Icon(AppIcons.chevron.of(context), color: c.textMuted, size: 18),
            onTap: _recordHotKey,
          ),
          GroupRow(
            title: context.t('mobile.bedienungshilfen'),
            subtitle: a.trusted ? context.t('mobile.freigegeben') : context.t('mobile.nichtFreigegeben'),
            trailing: a.trusted
                ? Icon(Icons.check_circle_rounded, color: c.textSuccess, size: 20)
                : TextButton(onPressed: a.askTrust, child: Text(context.t('mobile.freigeben'))),
          ),
          GroupRow(
            title: context.t('mobile.aufraeumen'),
            subtitle: a.cleanAvailable ? context.t('mobile.aufraeumenHinweis') : context.t('mobile.aufraeumenNicht'),
            trailing: Switch.adaptive(
              value: a.clean && a.cleanAvailable,
              onChanged: a.cleanAvailable ? a.setClean : null,
            ),
          ),
          GroupRow(
            title: context.t('mobile.umgebung'),
            subtitle: context.t('mobile.umgebungHinweis'),
            trailing: Switch.adaptive(
              value: a.useContext && a.clean && a.cleanAvailable,
              onChanged: a.clean && a.cleanAvailable ? a.setContext : null,
            ),
          ),
        ],
      ),
      Padding(
        padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
        child: Text(context.t('mobile.ueberallHinweis'), style: small),
      ),
    ];
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final me = _me ?? widget.me;
    final small = context.type.bodySmall?.copyWith(color: c.textMuted);
    return Scaffold(
      appBar: ChromeAppBar(title: Text(context.t('mobile.einstellungen'))),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 40),
        children: [
          SectionTitle(context.t('mobile.konto')),
          InsetGroup(
            children: [
              GroupRow(
                leading: PersonPhoto(api: widget.api, humanId: me.id, photoId: me.photoId, name: me.displayName),
                title: me.displayName,
                subtitle: me.email,
              ),
              // The profile photo (#377): taken here, or chosen, and cropped.
              GroupRow(
                title: me.photoId == null ? context.t('mobile.fotoHinzufuegen') : context.t('mobile.fotoAendern'),
                trailing: Icon(AppIcons.camera.of(context), color: c.textMuted, size: 20),
                onTap: () async {
                  final changed = await changePhoto(context, widget.api, me);
                  if (changed == null || !mounted) return;
                  setState(() => _me = changed);
                  widget.onChanged?.call(changed);
                },
              ),
              GroupRow(
                title: context.t('mobile.instanz'),
                trailing: Text(widget.api.base.host, style: context.type.bodyMedium),
              ),
              GroupRow(
                title: context.t('mobile.rolle'),
                trailing: Text(me.role, style: context.type.bodyMedium),
              ),
            ],
          ),
          if (!me.teamSurface)
            Padding(
              padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
              child: Text(context.t('mobile.nurNotizen'), style: small),
            ),

          SectionTitle(context.t('mobile.spracherkennung')),
          InsetGroup(
            dividerIndent: 14,
            children: [
              GroupRow(
                title: context.t('mobile.sprachmodell'),
                subtitle: _modelLine(context),
                onTap: (_model.info?.models.where((m) => m.engine != 'speaker').length ?? 0) > 1 ? _pickModel : null,
                tabularSubtitle: true,
                trailing: _model.problem == SpeechModelProblem.off || _model.info == null || _model.downloading
                    ? null
                    : _model.onDevice
                    ? TextButton(onPressed: _model.remove, child: Text(context.t('mobile.modellEntfernen')))
                    : TextButton(
                        onPressed: () => _model.ensure(widget.api),
                        child: Text(context.t('mobile.modellLaden')),
                      ),
              ),
              GroupRow(
                title: context.t('mobile.diktatTesten'),
                trailing: Icon(AppIcons.chevron.of(context), color: c.textMuted, size: 18),
                onTap: () => Navigator.of(
                  context,
                ).push(MaterialPageRoute<void>(builder: (_) => DictationTestScreen(api: widget.api))),
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(
              // The model's licence asks to be named with it (#353).
              [context.t('mobile.sprachHinweis'), ?_model.info?.credit].join('\n'),
              style: small,
            ),
          ),

          if (DictateAnywhere.supported) ..._anywhere(context, small),
          if (ActivityRecorder.supported) ..._activity(context, small),

          SectionTitle(context.t('mobile.diagnose')),
          InsetGroup(
            dividerIndent: 14,
            children: [
              GroupRow(
                title: context.t('mobile.protokollAufzeichnen'),
                trailing: Switch.adaptive(
                  value: Diagnostics.instance.enabled,
                  onChanged: (on) async {
                    await Diagnostics.instance.setEnabled(on);
                    await _measureLog();
                  },
                ),
              ),
              GroupRow(
                title: context.t('mobile.protokollKopieren'),
                subtitle: _logBytes == null ? null : '${(_logBytes! / 1000).ceil()} KB',
                tabularSubtitle: true,
                onTap: (_logBytes ?? 0) > 0 ? _copyLog : null,
              ),
              GroupRow(title: context.t('mobile.protokollLoeschen'), onTap: (_logBytes ?? 0) > 0 ? _clearLog : null),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(context.t('mobile.diagnoseHinweis'), style: small),
          ),

          const SizedBox(height: 28),
          InsetGroup(
            children: [
              InkWell(
                onTap: _disconnect,
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 18),
                  child: Center(
                    child: Text(
                      context.t('mobile.trennen'),
                      style: context.type.titleMedium?.copyWith(color: c.textDanger),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Dictation to try, and nothing is saved: the waveform shows whether the
/// microphone delivers sound, the text what the model makes of it. Where a
/// dictation in a note fails, this is where to find out why.
class DictationTestScreen extends StatefulWidget {
  const DictationTestScreen({super.key, required this.api, this.dictation});

  final CoveyApi api;
  final Dictation? dictation;

  @override
  State<DictationTestScreen> createState() => _DictationTestScreenState();
}

class _DictationTestScreenState extends State<DictationTestScreen> {
  late final Dictation _d = widget.dictation ?? Dictation(api: widget.api);

  @override
  void initState() {
    super.initState();
    _d.addListener(_changed);
  }

  @override
  void dispose() {
    _d.removeListener(_changed);
    if (widget.dictation == null) _d.dispose();
    super.dispose();
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  Future<void> _toggle() async {
    if (_d.preparing) return;
    if (_d.running) {
      await _d.stop();
      return;
    }
    await _d.start();
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final failed = !_d.running && !_d.preparing && _d.failure != null;
    final text = _d.text;
    return Scaffold(
      appBar: ChromeAppBar(title: Text(context.t('mobile.diktatTesten'))),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(context.t('mobile.diktatTestHinweis'), style: context.type.bodyMedium),
              const SizedBox(height: 16),
              Expanded(
                child: Container(
                  padding: const EdgeInsets.all(18),
                  decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(20)),
                  child: SingleChildScrollView(
                    reverse: true,
                    child: Text(
                      failed
                          ? dictationFailure(context, _d.failure, _d.detail)
                          : _d.preparing
                          ? modelLoadingText(context, _d)
                          : text.isEmpty
                          ? (_d.running ? context.t('mobile.hoertZu') : context.t('mobile.nochNichtsGehoert'))
                          : text,
                      style: context.type.bodyLarge?.copyWith(
                        color: failed
                            ? c.textDanger
                            : text.isEmpty
                            ? c.textMuted
                            : c.textPrimary,
                      ),
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              Waveform(levels: _d.levels, height: 48, active: _d.running),
              const SizedBox(height: 16),
              FilledButton.icon(
                onPressed: _d.preparing ? null : _toggle,
                icon: Icon(_d.running ? AppIcons.stop.of(context) : AppIcons.mic.of(context)),
                label: Text(_d.running ? context.t('mobile.anhalten') : context.t('mobile.zuhoeren')),
                style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(56), shape: const StadiumBorder()),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
