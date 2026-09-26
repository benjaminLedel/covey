import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../diagnostics.dart';
import '../dictation.dart';
import '../dictation_view.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../speech_model.dart';
import '../theme.dart';
import '../ui.dart';
import 'notes.dart' show dictationFailure;

/// The languages whisper is offered in here: the app's ten, by their own
/// names — somebody looking for their language finds it in that language.
const speechLanguages = <String, String>{
  'de': 'Deutsch',
  'en': 'English',
  'es': 'Español',
  'fr': 'Français',
  'it': 'Italiano',
  'nl': 'Nederlands',
  'pl': 'Polski',
  'pt': 'Português',
  'ja': '日本語',
  'zh': '中文',
};

/// Settings (#349): who is signed in where, the speech model on this
/// device and the language it listens for, a place to try dictation, and
/// the way out. Reached from the person's initials.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key, required this.api, required this.me, required this.onDisconnect});

  final CoveyApi api;
  final Me me;
  final VoidCallback onDisconnect;

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  final _model = SpeechModel.instance;

  @override
  void initState() {
    super.initState();
    _model.addListener(_changed);
    _model.loadPrefs().then((_) => _model.refresh(widget.api));
    Diagnostics.instance.addListener(_changed);
    _measureLog();
  }

  @override
  void dispose() {
    Diagnostics.instance.removeListener(_changed);
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

  /// "Whisper small", "Parakeet": the engine's name, and the size where the
  /// engine has several.
  String _modelName(SpeechModelInfo m) => m.engine == 'parakeet' ? 'Parakeet' : 'Whisper ${m.name}';

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
    if (i == null || i.models.isEmpty) return;
    final current = _model.chosen ?? i.defaultName;
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
            for (final m in i.models)
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
              for (final m in i.models)
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

  Future<void> _pickLanguage() async {
    final auto = context.t('mobile.wieDieApp');
    final options = <String?, String>{null: auto, ...speechLanguages};
    String? picked;
    var chose = false;
    if (isApple(context)) {
      await showCupertinoModalPopup<void>(
        context: context,
        builder: (context) => CupertinoActionSheet(
          title: Text(context.t('mobile.erkannteSprache')),
          actions: [
            for (final e in options.entries)
              CupertinoActionSheetAction(
                isDefaultAction: e.key == _model.language,
                onPressed: () {
                  picked = e.key;
                  chose = true;
                  Navigator.pop(context);
                },
                child: Text(e.value),
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
              for (final e in options.entries)
                ListTile(
                  title: Text(e.value),
                  trailing: e.key == _model.language ? const Icon(Icons.check_rounded) : null,
                  onTap: () {
                    picked = e.key;
                    chose = true;
                    Navigator.pop(context);
                  },
                ),
            ],
          ),
        ),
      );
    }
    if (chose) await _model.setLanguage(picked);
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

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final me = widget.me;
    final lang = _model.language;
    final langLabel = lang == null
        ? '${context.t('mobile.wieDieApp')} · ${speechLanguages[Strings.of(context).language] ?? ''}'
        : speechLanguages[lang] ?? lang;
    final small = context.type.bodySmall?.copyWith(color: c.textMuted);
    return Scaffold(
      appBar: AppBar(title: Text(context.t('mobile.einstellungen'))),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 40),
        children: [
          SectionTitle(context.t('mobile.konto')),
          InsetGroup(
            children: [
              GroupRow(
                leading: CircleAvatar(
                  radius: 18,
                  backgroundColor: c.surface1,
                  child: Text(_initials(me.displayName), style: context.type.labelLarge),
                ),
                title: me.displayName,
                subtitle: me.email,
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
                onTap: (_model.info?.models.length ?? 0) > 1 ? _pickModel : null,
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
                title: context.t('mobile.erkannteSprache'),
                subtitle: langLabel,
                trailing: Icon(AppIcons.chevron.of(context), color: c.textMuted, size: 18),
                onTap: _pickLanguage,
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

String _initials(String name) =>
    name.split(RegExp(r'\s+')).where((p) => p.isNotEmpty).take(2).map((p) => p[0].toUpperCase()).join();

/// Dictation to try, and nothing is saved: the waveform shows whether the
/// microphone delivers sound, the text what whisper makes of it. Where a
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
    _d.language = SpeechModel.instance.language ?? Strings.of(context).language;
    await _d.start();
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final failed = !_d.running && !_d.preparing && _d.failure != null;
    final text = _d.text;
    return Scaffold(
      appBar: AppBar(title: Text(context.t('mobile.diktatTesten'))),
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
