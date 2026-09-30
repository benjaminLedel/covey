import 'dart:convert';

import '../api.dart';
import '../face.dart' show faceHash;
import '../prefs.dart';
import 'speech_text.dart';

/// A voice an agent speaks with in a call (#497): a model of the instance's
/// voice catalogue run on this device, a voice of the organisation's own
/// speech server, or the Mac's speech synthesis.
///
/// The shape of the `speech` field of a covey voice (spec/24) — source,
/// model, voice, speaker, pace — which is where an agent's spoken voice
/// belongs: it is part of how the agent speaks, like its chat tone. The
/// app's own per-agent choice ([CallVoicePrefs]) is a device's override on
/// top of it, and only there can the Mac's own voice be named.
class SpokenVoice {
  const SpokenVoice.device(this.model, {this.speaker = 0, this.rate = 0}) : source = sourceDevice, voice = '';
  const SpokenVoice.server({this.model = '', this.voice = '', this.rate = 0}) : source = sourceServer, speaker = 0;
  const SpokenVoice.system({this.rate = 0}) : source = sourceSystem, model = '', voice = '', speaker = 0;

  static const sourceDevice = 'device';
  static const sourceServer = 'server';
  static const sourceSystem = 'system';

  /// `device`, `server` or `system`.
  final String source;

  /// The catalogue name for `device`; the server's model for `server`.
  final String model;

  /// The server's voice name, for `server`.
  final String voice;

  /// A speaker of the model, for `device`.
  final int speaker;

  /// 1 is the voice's own pace; 0 leaves it at that.
  final double rate;

  bool get onDevice => source == sourceDevice && model.isNotEmpty;
  bool get onServer => source == sourceServer;

  /// Null for what is not a voice: an unknown source, a device model
  /// without a name.
  static SpokenVoice? fromJson(Object? j) {
    if (j is! Map) return null;
    final rate = (j['rate'] as num?)?.toDouble() ?? 0;
    final model = j['model'] is String ? j['model'] as String : '';
    switch (j['source']) {
      case sourceDevice:
        if (model.isEmpty) return null;
        return SpokenVoice.device(model, speaker: (j['speaker'] as num?)?.toInt() ?? 0, rate: rate);
      case sourceServer:
        return SpokenVoice.server(model: model, voice: j['voice'] is String ? j['voice'] as String : '', rate: rate);
      case sourceSystem:
        return SpokenVoice.system(rate: rate);
    }
    return null;
  }

  Map<String, Object?> toJson() => {
    'source': source,
    if (model.isNotEmpty) 'model': model,
    if (voice.isNotEmpty) 'voice': voice,
    if (onDevice) 'speaker': speaker,
    if (rate != 0) 'rate': rate,
  };

  @override
  bool operator ==(Object other) =>
      other is SpokenVoice &&
      other.source == source &&
      other.model == model &&
      other.voice == voice &&
      other.speaker == speaker &&
      other.rate == rate;

  @override
  int get hashCode => Object.hash(source, model, voice, speaker, rate);

  @override
  String toString() => switch (source) {
    sourceDevice => 'device $model#$speaker',
    sourceServer => 'server ${model.isEmpty ? 'default' : model}/${voice.isEmpty ? 'default' : voice}',
    _ => source,
  };
}

/// What the instance offers to speak with (#497): the catalogue's voices
/// for the device, and whether the organisation has a speech server.
class VoiceOffer {
  const VoiceOffer({this.voices = const [], this.server = false});

  factory VoiceOffer.of(SpeechModelInfo i) => VoiceOffer(voices: i.voices, server: i.synthesize);

  final List<SpeechModelInfo> voices;
  final bool server;
}

/// What speaks one utterance: a model of the catalogue on the device, the
/// organisation's speech server, or the system's synthesis with one of its
/// voices (null: the system picks).
class VoicePlan {
  const VoicePlan.device(this.model, this.speaker, this.rate) : server = null, systemVoice = null;
  VoicePlan.server(SpokenVoice this.server) : model = null, speaker = 0, systemVoice = null, rate = server.rate;
  const VoicePlan.system(this.systemVoice, this.rate) : model = null, speaker = 0, server = null;

  final SpeechModelInfo? model;
  final int speaker;
  final SpokenVoice? server;
  final SystemVoice? systemVoice;
  final double rate;

  bool get onDevice => model != null;
  bool get onServer => server != null;

  @override
  String toString() => onDevice
      ? 'device voice ${model!.name}#$speaker'
      : onServer
      ? '$server'
      : 'system voice ${systemVoice?.id ?? 'default'}';
}

String _base(String language) => language.split(RegExp('[-_]')).first.toLowerCase();

/// A model of [offered] for [language], and a speaker of it, always the same
/// for the same agent (#497): the voices of that language with all their
/// speakers in a fixed order, and the agent's hash picking one — so two
/// agents sound different and one sounds like itself. Null when the
/// instance offers no voice for that language.
(SpeechModelInfo, int)? chooseOwnVoice(List<SpeechModelInfo> offered, String language, String agentId) {
  final base = _base(language);
  final of = offered.where((m) => m.voice != null && _base(m.voice!.language) == base).toList()
    ..sort((a, b) => a.name.compareTo(b.name));
  final picks = [
    for (final m in of)
      for (var s = 0; s < (m.voice!.speakers < 1 ? 1 : m.voice!.speakers); s++) (m, s),
  ];
  if (picks.isEmpty) return null;
  return picks[faceHash(agentId) % picks.length];
}

/// Which voice speaks [language] for [agentId] (#497), from what was chosen
/// for the agent — the device's override, else its covey voice's — and what
/// there is:
///
/// - nothing chosen, or the Mac's voice: the system's synthesis, in a voice
///   of that language picked per agent ([chooseVoice]). covey's own voices
///   are not the default while they are being chosen;
/// - the organisation's speech server, while it has one;
/// - a device model the instance offers for that language: that model;
/// - otherwise — a server that is gone, a model for another language — the
///   agent's own device voice for that language among the offered ones, so
///   it keeps speaking with covey's voices; the system's when there is none.
///
/// A server that fails mid-call falls back the same way ([fallbackPlan]).
VoicePlan planVoice({
  required SpokenVoice? chosen,
  required VoiceOffer offer,
  required List<SystemVoice> system,
  required String language,
  required String agentId,
}) {
  final rate = chosen?.rate ?? 0;
  if (chosen == null || chosen.source == SpokenVoice.sourceSystem) {
    return VoicePlan.system(chooseVoice(system, language, agentId), rate);
  }
  if (chosen.onServer && offer.server) return VoicePlan.server(chosen);
  if (chosen.onDevice) {
    final base = _base(language);
    for (final m in offer.voices) {
      if (m.name != chosen.model || m.voice == null) continue;
      if (_base(m.voice!.language) != base) break;
      final n = m.voice!.speakers < 1 ? 1 : m.voice!.speakers;
      return VoicePlan.device(m, chosen.speaker.clamp(0, n - 1), rate);
    }
  }
  return fallbackPlan(offer: offer, system: system, language: language, agentId: agentId, rate: rate);
}

/// The voice when the chosen one cannot speak: the agent's own device voice
/// for [language], else the system's.
VoicePlan fallbackPlan({
  required VoiceOffer offer,
  required List<SystemVoice> system,
  required String language,
  required String agentId,
  double rate = 0,
}) {
  final own = chooseOwnVoice(offer.voices, language, agentId);
  return own == null
      ? VoicePlan.system(chooseVoice(system, language, agentId), rate)
      : VoicePlan.device(own.$1, own.$2, rate);
}

/// The device's own choice of voice per agent (#497), kept in the app's
/// preferences. For now the place to pick a voice by ear; the lasting place
/// is the agent's covey voice on the instance, which every device follows.
class CallVoicePrefs {
  CallVoicePrefs._();

  static String _key(String agentId) => 'call.voice.$agentId';

  static Future<SpokenVoice?> read(String agentId) async {
    try {
      final raw = await Prefs.instance.read(_key(agentId));
      if (raw == null || raw.isEmpty) return null;
      return SpokenVoice.fromJson(jsonDecode(raw));
    } catch (_) {
      return null;
    }
  }

  /// Null clears the override: the covey voice decides again.
  static Future<void> write(String agentId, SpokenVoice? voice) async {
    try {
      await Prefs.instance.write(_key(agentId), voice == null ? null : jsonEncode(voice.toJson()));
    } catch (_) {
      // Not kept: the next call decides as before.
    }
  }
}
