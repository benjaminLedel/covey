import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../chrome.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../models.dart';
import '../speech_model.dart';
import '../theme.dart';
import '../ui.dart';
import 'call.dart';
import 'recording.dart';
import 'synth.dart';
import 'voice.dart';
import 'voice_choice.dart';

/// A call's settings on this Mac (#497, #498): the pause that ends a turn,
/// how long the person must speak to interrupt, how long a turn stands as
/// understood, the diagnostics recording, and a voice per agent.
class CallSettingsScreen extends StatefulWidget {
  const CallSettingsScreen({super.key, required this.api});

  final CoveyApi api;

  @override
  State<CallSettingsScreen> createState() => _CallSettingsScreenState();
}

class _CallSettingsScreenState extends State<CallSettingsScreen> {
  List<Agent> _agents = const [];
  VoiceOffer _offer = const VoiceOffer();
  final _chosen = <String, SpokenVoice?>{};
  (int, int)? _recorded;

  static const _pauses = [500, 700, 1000, 1500];
  static const _bargeIns = [200, 400, 800];
  static const _windows = [0, 1000, 1500, 3000];

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  Future<void> _load() async {
    try {
      final agents = (await widget.api.agents()).where((a) => a.isColleague).toList()
        ..sort((a, b) => a.displayName.compareTo(b.displayName));
      final offer = await ApiVoiceCatalogue(widget.api).offered();
      for (final a in agents) {
        _chosen[a.id] = await CallVoicePrefs.read(a.id);
      }
      if (!mounted) return;
      setState(() {
        _agents = agents;
        _offer = offer;
      });
    } on ApiException {
      // The rows that need the instance stay empty.
    }
    await _measure();
  }

  Future<void> _measure() async {
    if (CallSettings.record.value) {
      try {
        await (await CallRecording.open()).prune();
      } catch (_) {}
    }
    final r = await CallRecording.size();
    if (mounted) setState(() => _recorded = r);
  }

  String _seconds(BuildContext context, int ms) =>
      ms == 0 ? context.t('call.windowOff') : context.t('call.seconds', args: {'n': (ms / 1000).toStringAsFixed(1)});

  Future<void> _pick(String title, List<int> choices, int current, Future<void> Function(Duration) set) async {
    await showModalBottomSheet<void>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
                child: Text(title, style: context.type.titleMedium),
              ),
              InsetGroup(
                dividerIndent: 14,
                children: [
                  for (final ms in choices)
                    GroupRow(
                      title: _seconds(context, ms),
                      trailing: ms == current ? Icon(Icons.check_rounded, color: context.colors.textAccent) : null,
                      onTap: () async {
                        Navigator.pop(context);
                        await set(Duration(milliseconds: ms));
                        if (mounted) setState(() {});
                      },
                    ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  String _voiceLine(BuildContext context, SpokenVoice? v) {
    if (v == null) return context.t('call.voiceAuto');
    if (v.onServer) return context.t('call.voiceServer');
    if (!v.onDevice) return context.t('call.voiceSystem');
    for (final m in _offer.voices) {
      if (m.name == v.model) return _label(m);
    }
    return v.model;
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final small = context.type.bodySmall?.copyWith(color: c.textMuted);
    final recorded = _recorded;
    return Scaffold(
      appBar: ChromeAppBar(title: Text(context.t('call.settingsTitle'))),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 40),
        children: [
          SectionTitle(context.t('call.turns')),
          InsetGroup(
            dividerIndent: 14,
            children: [
              GroupRow(
                title: context.t('call.pause'),
                subtitle: _seconds(context, CallSettings.pause.value.inMilliseconds),
                tabularSubtitle: true,
                onTap: () => _pick(
                  context.t('call.pause'),
                  _pauses,
                  CallSettings.pause.value.inMilliseconds,
                  CallSettings.setPause,
                ),
              ),
              GroupRow(
                title: context.t('call.bargeIn'),
                subtitle: _seconds(context, CallSettings.bargeIn.value.inMilliseconds),
                tabularSubtitle: true,
                onTap: () => _pick(
                  context.t('call.bargeIn'),
                  _bargeIns,
                  CallSettings.bargeIn.value.inMilliseconds,
                  CallSettings.setBargeIn,
                ),
              ),
              GroupRow(
                title: context.t('call.window'),
                subtitle: _seconds(context, CallSettings.window.value.inMilliseconds),
                tabularSubtitle: true,
                onTap: () => _pick(
                  context.t('call.window'),
                  _windows,
                  CallSettings.window.value.inMilliseconds,
                  CallSettings.setWindow,
                ),
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(context.t('call.turnsHint'), style: small),
          ),

          SectionTitle(context.t('call.voices')),
          if (_agents.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 32),
              child: Text(context.t('call.voicesNone'), style: small),
            )
          else
            InsetGroup(
              children: [
                for (final a in _agents)
                  GroupRow(
                    leading: Face(slug: a.slug, size: 32),
                    title: a.displayName,
                    subtitle: _voiceLine(context, _chosen[a.id]),
                    trailing: Icon(AppIcons.chevron.of(context), color: c.textMuted, size: 18),
                    onTap: () async {
                      final picked = await Navigator.of(context).push<VoicePick>(
                        MaterialPageRoute(
                          builder: (_) => VoicePickerScreen(
                            agent: a,
                            offer: _offer,
                            current: _chosen[a.id],
                            preview: VoicePreview(widget.api),
                          ),
                        ),
                      );
                      if (picked == null) return;
                      await CallVoicePrefs.write(a.id, picked.voice);
                      if (mounted) setState(() => _chosen[a.id] = picked.voice);
                    },
                  ),
              ],
            ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(context.t('call.voicesHint'), style: small),
          ),

          SectionTitle(context.t('call.diagnostics')),
          InsetGroup(
            dividerIndent: 14,
            children: [
              GroupRow(
                title: context.t('call.record'),
                trailing: ValueListenableBuilder<bool>(
                  valueListenable: CallSettings.record,
                  builder: (context, on, _) => Switch.adaptive(
                    value: on,
                    onChanged: (v) async {
                      await CallSettings.setRecord(v);
                      await _measure();
                    },
                  ),
                ),
              ),
              GroupRow(
                title: context.t('call.recordDelete'),
                subtitle: recorded == null
                    ? null
                    : context.t(
                        'call.recordSize',
                        args: {'turns': recorded.$2, 'mb': (recorded.$1 / 1000000).toStringAsFixed(1)},
                      ),
                tabularSubtitle: true,
                onTap: (recorded?.$1 ?? 0) > 0
                    ? () async {
                        await CallRecording.deleteAll();
                        await _measure();
                      }
                    : null,
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(context.t('call.recordHint'), style: small),
          ),
        ],
      ),
    );
  }
}

String _label(SpeechModelInfo m) {
  final v = m.voice;
  if (v == null) return m.name;
  return '${v.label.isEmpty ? m.name : v.label} · ${v.language}';
}

/// What the picker hands back: a choice, including "none" (null voice).
class VoicePick {
  const VoicePick(this.voice);
  final SpokenVoice? voice;
}

/// The voices the instance offers (#497).
class ApiVoiceCatalogue {
  ApiVoiceCatalogue(this.api);
  final CoveyApi api;

  /// Nothing on an instance from before voices, or with none offered.
  Future<VoiceOffer> offered() async {
    try {
      return VoiceOffer.of(await api.speechModel());
    } on ApiException {
      return const VoiceOffer();
    }
  }
}

/// Plays a sentence in a voice, to choose by ear.
class VoicePreview {
  VoicePreview(this.api);
  final CoveyApi? api;

  AgentSpeaker? _speaker;

  static const _sample = {
    'de': 'Hallo, ich bin {name}. So klinge ich in einem Anruf.',
    'en': "Hello, I'm {name}. This is how I sound in a call.",
  };

  /// Speaks the sample in [voice] (null: the Mac's) for [agent], in the
  /// voice's language or [language]. [model] is the device model it names.
  Future<void> play(
    Agent agent,
    SpokenVoice? voice,
    SpeechModelInfo? model,
    String language, {
    bool server = false,
  }) async {
    final api = this.api;
    if (api == null) return;
    await stop();
    final lang = model?.voice?.language ?? language;
    final base = lang.split(RegExp('[-_]')).first;
    final text = (_sample[base] ?? _sample['en']!).replaceAll('{name}', agent.displayName);
    final s = _speaker = AgentSpeaker(
      output: MacVoiceOutput(),
      agentId: agent.id,
      chosen: () async => voice,
      offered: () async => VoiceOffer(voices: model == null ? const [] : [model], server: server),
      fetch: fetchVoiceModel(api),
      server: (m, v) => ServerSynthesiser(api.synthesizeSpeech, model: m, voice: v),
    );
    // The voice is fetched first, so the preview is heard in it.
    if (model != null) await fetchVoiceModel(api)(model, wait: true);
    await s.prepare(language: lang);
    await s.speak(text, language: lang);
  }

  Future<void> stop() async {
    final s = _speaker;
    _speaker = null;
    if (s == null) return;
    await s.stop();
    s.dispose();
  }
}

/// A voice for one agent, on this device (#497): automatic — the agent's
/// covey voice, else the Mac's own voice —, the Mac's voice, or one of the
/// voices the instance offers, each with a button to hear it.
class VoicePickerScreen extends StatefulWidget {
  const VoicePickerScreen({
    super.key,
    required this.agent,
    required this.offer,
    required this.current,
    required this.preview,
    this.language,
  });

  final Agent agent;
  final VoiceOffer offer;
  final SpokenVoice? current;
  final VoicePreview preview;

  /// The language the Mac's voice previews in; the app's by default.
  final String? language;

  @override
  State<VoicePickerScreen> createState() => _VoicePickerScreenState();
}

class _VoicePickerScreenState extends State<VoicePickerScreen> {
  String? _playing;

  @override
  void dispose() {
    unawaited(widget.preview.stop());
    super.dispose();
  }

  Future<void> _preview(String key, SpokenVoice? v, SpeechModelInfo? m) async {
    setState(() => _playing = key);
    try {
      await widget.preview.play(
        widget.agent,
        v,
        m,
        widget.language ?? Strings.of(context).language,
        server: widget.offer.server,
      );
    } catch (_) {
      // Heard nothing; the choice still stands.
    }
    if (mounted && _playing == key) setState(() => _playing = null);
  }

  Widget _row(BuildContext context, String key, String title, String? subtitle, SpokenVoice? v, SpeechModelInfo? m) {
    final c = context.colors;
    final selected = widget.current == v;
    final model = m == null ? null : SpeechModel.voice(m.name);
    return ListenableBuilder(
      listenable: model ?? Listenable.merge(const []),
      builder: (context, _) {
        final p = model?.progress;
        final line = [
          ?subtitle,
          if (p != null) context.t('call.voiceFetching', args: {'percent': (p * 100).round()}),
        ].join('\n');
        return GroupRow(
          key: ValueKey('voice-$key'),
          title: title,
          subtitle: line.isEmpty ? null : line,
          leading: selected ? Icon(Icons.check_rounded, color: c.textAccent) : const SizedBox.shrink(),
          trailing: IconButton(
            key: ValueKey('preview-$key'),
            tooltip: context.t('call.voicePreview'),
            icon: Icon((_playing == key ? AppIcons.stop : AppIcons.play).of(context), color: c.textAccent),
            onPressed: _playing == key
                ? () async {
                    await widget.preview.stop();
                    if (mounted) setState(() => _playing = null);
                  }
                : () => _preview(key, v, m),
          ),
          onTap: () => Navigator.of(context).pop(VoicePick(v)),
        );
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final small = context.type.bodySmall?.copyWith(color: context.colors.textMuted);
    return Scaffold(
      appBar: ChromeAppBar(title: Text(widget.agent.displayName)),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 40),
        children: [
          SectionTitle(context.t('call.voiceFor')),
          InsetGroup(
            children: [
              _row(context, 'auto', context.t('call.voiceAuto'), context.t('call.voiceAutoHint'), null, null),
              _row(context, 'system', context.t('call.voiceSystem'), null, const SpokenVoice.system(), null),
              // The organisation's speech server, with its default model
              // and voice; which ones is set on the instance.
              if (widget.offer.server)
                _row(
                  context,
                  'server',
                  context.t('call.voiceServer'),
                  context.t('call.voiceServerHint'),
                  const SpokenVoice.server(),
                  null,
                ),
              for (final m in widget.offer.voices)
                for (var s = 0; s < (m.voice?.speakers ?? 1).clamp(1, 1 << 20); s++)
                  _row(
                    context,
                    '${m.name}#$s',
                    (m.voice?.speakers ?? 1) > 1 ? '${_label(m)} · ${s + 1}' : _label(m),
                    [
                      if (m.voice?.placeholder ?? false) context.t('call.voicePlaceholder'),
                      '${(m.size / 1000000).round()} MB',
                      ?m.voice?.licence,
                    ].join(' · '),
                    SpokenVoice.device(m.name, speaker: s),
                    m,
                  ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(
              [context.t('call.voicePickerHint'), for (final m in widget.offer.voices) ?m.credit].join('\n'),
              style: small,
            ),
          ),
        ],
      ),
    );
  }
}
