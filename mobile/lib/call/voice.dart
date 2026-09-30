import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../diagnostics.dart';
import '../speech_model.dart';
import 'speech_text.dart';
import 'synth.dart';
import 'voice_choice.dart';

/// What the voice reports while it speaks (#494): it began, it reached a
/// word (the system's synthesis only; the face's mouth moves on each), it
/// finished or was stopped.
enum SpeakingEvent { started, word, finished, cancelled }

/// The seam between a call and whatever speaks for the agent, so a test
/// stands in.
abstract class Speaker {
  /// Gets ready to speak: the voices there are, and the agent's own voice
  /// for [language] fetched and loaded when it has one. Returns a line for
  /// the diagnostics log.
  Future<String> prepare({required String language});

  /// The language [text] is written in, BCP 47, when it can be told with
  /// some confidence; null otherwise.
  Future<String?> language(String text);

  /// Speaks [text]; the returned future completes when speaking ends,
  /// finished or stopped.
  Future<void> speak(String text, {required String language});

  /// Stops at once, mid-word.
  Future<void> stop();

  Stream<SpeakingEvent> get events;

  /// How open the mouth is, 0–1, frame by frame, while a voice of covey's
  /// own speaks: taken from the synthesised audio. Null while the system's
  /// synthesis speaks, which reports words instead ([SpeakingEvent.word]).
  ValueListenable<double?> get level;

  void dispose();
}

/// Where speech comes out on the Mac: the system's synthesis, and the
/// playback of samples synthesised on the device (#497) — both through the
/// `covey/voice` channel (MainFlutterWindow.swift). Each utterance carries
/// an id; events name the id they belong to.
abstract class VoiceOutput implements StreamDecoding {
  Future<List<SystemVoice>> voices();
  Future<String?> language(String text);

  /// The system's synthesis speaks [text].
  Future<void> say(int id, String text, {String? voiceId, required String language, double rate = 0});

  /// Plays [pcm] after what utterance [id] already queued; [last] says
  /// nothing more comes for it, so its end is reported.
  Future<void> play(int id, Pcm pcm, {required bool last});

  /// Stops speaking and playing at once.
  Future<void> stop();

  Stream<(SpeakingEvent, int)> get events;

  void dispose();
}

/// The Mac's side of [VoiceOutput].
class MacVoiceOutput implements VoiceOutput {
  MacVoiceOutput() {
    _channel.setMethodCallHandler(_onCall);
  }

  static const _channel = MethodChannel('covey/voice');
  final _events = StreamController<(SpeakingEvent, int)>.broadcast();

  Future<Object?> _onCall(MethodCall call) async {
    final e = switch (call.method) {
      'started' => SpeakingEvent.started,
      'word' => SpeakingEvent.word,
      'finished' => SpeakingEvent.finished,
      'cancelled' => SpeakingEvent.cancelled,
      _ => null,
    };
    final id = (call.arguments as Map?)?['id'];
    if (e != null && id is int) _events.add((e, id));
    return null;
  }

  @override
  Stream<(SpeakingEvent, int)> get events => _events.stream;

  @override
  Future<List<SystemVoice>> voices() async {
    try {
      final list = await _channel.invokeListMethod<Object?>('voices') ?? const [];
      return [for (final v in list.whereType<Map<Object?, Object?>>()) SystemVoice.fromJson(v)];
    } on PlatformException catch (e) {
      diag('call', 'no voices: ${e.message}');
      return const [];
    }
  }

  @override
  Future<String?> language(String text) async {
    try {
      final r = await _channel.invokeMapMethod<String, Object?>('language', {'text': text});
      final p = (r?['confidence'] as num?)?.toDouble() ?? 0;
      final lang = r?['language'];
      return p >= 0.6 && lang is String ? lang : null;
    } on PlatformException {
      return null;
    }
  }

  @override
  Future<void> say(int id, String text, {String? voiceId, required String language, double rate = 0}) =>
      _channel.invokeMethod<void>('speak', {
        'id': id,
        'text': text,
        'voice': voiceId,
        'language': language,
        if (rate > 0) 'rate': rate,
      });

  @override
  Future<void> play(int id, Pcm pcm, {required bool last}) => _channel.invokeMethod<void>('play', {
    'id': id,
    'samples': pcm.samples,
    'sampleRate': pcm.sampleRate,
    'last': last,
  });

  @override
  Future<void> stop() => _channel.invokeMethod<void>('stop');

  @override
  Future<bool> openDecoder(int id) => _decoder.openDecoder(id);

  @override
  Future<Pcm> decode(int id, Uint8List bytes) => _decoder.decode(id, bytes);

  @override
  Future<void> closeDecoder(int id) => _decoder.closeDecoder(id);

  @override
  void dispose() {
    unawaited(stop().catchError((_) {}));
    _channel.setMethodCallHandler(null);
    unawaited(_events.close());
  }
}

/// Where a voice model of the catalogue lies on the device, fetching it
/// when it is not there. [wait] false only starts the fetch and answers
/// null at once, so a call is not held up by a download.
typedef VoiceModelFetch = Future<String?> Function(SpeechModelInfo model, {required bool wait});

/// Loads a synthesiser for a model of [family] unpacked in [dir].
typedef SynthLoader = Future<Synthesiser> Function(String dir, String family);

/// The agent's voice in a call (#497): the voice [planVoice] picks for each
/// utterance — one of covey's own, synthesised on the device or by the
/// organisation's speech server sentence by sentence and played while the
/// next is synthesised, or the system's synthesis when no own voice is
/// chosen, it is not on the device yet, or the server fails.
///
/// Which voice is chosen comes from [chosen]: the device's override for the
/// agent, else the `speech` of its covey voice — the natural home of the
/// spoken voice, since it belongs to how the agent speaks — else nothing.
class AgentSpeaker implements Speaker {
  AgentSpeaker({
    required this.output,
    required this.agentId,
    required this.chosen,
    required this.offered,
    required this.fetch,
    this.server,
    this.loadSynth = SherpaSynthesiser.load,
  }) {
    _sub = output.events.listen(_onEvent);
  }

  final VoiceOutput output;
  final String agentId;
  final Future<SpokenVoice?> Function() chosen;
  final Future<VoiceOffer> Function() offered;
  final VoiceModelFetch fetch;

  /// A synthesiser on the organisation's speech server for the chosen
  /// voice; null when this app cannot reach one.
  final Synthesiser Function(SpokenVoice voice)? server;
  final SynthLoader loadSynth;

  late final StreamSubscription<(SpeakingEvent, int)> _sub;
  final _events = StreamController<SpeakingEvent>.broadcast();
  final _level = ValueNotifier<double?>(null);

  List<SystemVoice> _system = const [];
  VoiceOffer _offer = const VoiceOffer();
  SpokenVoice? _chosen;
  bool _prepared = false;

  /// One synthesiser per device model, loaded once: switching languages
  /// mid-call keeps the first, and a model takes a moment to load.
  final _synths = <String, Future<Synthesiser>>{};

  /// Set once the speech server failed in this call: the device speaks
  /// from then on rather than trying again with every reply.
  bool _serverFailed = false;

  int _id = 0;
  Completer<void>? _done;

  /// The level of what is being played, frame by frame from when it started.
  final _frames = <double>[];
  static const _fps = 60;
  Stopwatch? _clock;
  Timer? _tick;

  @override
  Stream<SpeakingEvent> get events => _events.stream;

  @override
  ValueListenable<double?> get level => _level;

  @override
  Future<String> prepare({required String language}) async {
    _system = await output.voices();
    try {
      _chosen = await chosen();
    } catch (e) {
      diag('call', 'the chosen voice could not be read: $e');
    }
    try {
      _offer = await offered();
    } catch (e) {
      diag('call', 'the instance lists no voices: $e');
    }
    _prepared = true;
    final plan = _plan(language);
    // The voice for the call's first language is fetched and loaded now,
    // while the call opens; one for another language when first needed.
    if (plan.onDevice) unawaited(_deviceSynth(plan.model!, wait: true).then((_) {}, onError: (_) {}));
    return '${_system.length} system voices, ${_offer.voices.length} own'
        '${_offer.server ? ', a speech server' : ''}, chosen ${_chosen ?? 'none'}, $plan';
  }

  VoiceOffer get _effectiveOffer => _serverFailed || server == null ? VoiceOffer(voices: _offer.voices) : _offer;

  VoicePlan _plan(String language) =>
      planVoice(chosen: _chosen, offer: _effectiveOffer, system: _system, language: language, agentId: agentId);

  Future<Synthesiser?> _deviceSynth(SpeechModelInfo model, {required bool wait}) async {
    final loading = _synths[model.name];
    if (loading != null) return loading;
    final dir = await fetch(model, wait: wait);
    if (dir == null) return null;
    final again = _synths[model.name];
    if (again != null) return again;
    final watch = Stopwatch()..start();
    final f = _synths[model.name] = loadSynth(dir, model.voice?.family ?? '');
    try {
      final s = await f;
      diag('call', 'voice ${model.name} loaded in ${watch.elapsedMilliseconds} ms');
      return s;
    } catch (e) {
      _synths.remove(model.name);
      diag('call', 'voice ${model.name} did not load: $e');
      rethrow;
    }
  }

  /// The synthesiser for [plan], or null when the system has to speak.
  Future<Synthesiser?> _synthFor(VoicePlan plan) async {
    if (plan.onServer) return server!(plan.server!);
    if (!plan.onDevice) return null;
    try {
      // Not waited for mid-call: until the voice is on the device, the
      // system speaks.
      final s = await _deviceSynth(plan.model!, wait: false);
      if (s == null) diag('call', 'voice ${plan.model!.name} not on the device yet, the system speaks');
      return s;
    } catch (_) {
      return null;
    }
  }

  @override
  Future<String?> language(String text) => output.language(text);

  @override
  Future<void> speak(String text, {required String language}) async {
    if (!_prepared) await prepare(language: language);
    final prev = _done;
    if (prev != null && !prev.isCompleted) prev.complete();
    final id = ++_id;
    final done = _done = Completer<void>();
    await _speakWith(id, _plan(language), text, language);
    return done.future;
  }

  Future<void> _speakWith(int id, VoicePlan plan, String text, String language) async {
    final synth = await _synthFor(plan);
    if (id != _id) return;
    _frames.clear();
    _clock = null;
    if (synth == null) {
      final voice = plan.systemVoice ?? chooseVoice(_system, language, agentId);
      _level.value = null;
      await output.say(id, text, voiceId: voice?.id, language: language, rate: plan.rate);
      return;
    }
    unawaited(_play(id, synth, plan, text, language));
  }

  /// Plays what [synth] makes of [text] piece by piece as it comes — the
  /// device's voice sentence by sentence, the speech server as its stream
  /// arrives. A voice that fails before anything was heard hands the text
  /// on: the speech server's to the device's own voice for the language or
  /// the system's, the device's to the system's.
  Future<void> _play(int id, Synthesiser synth, VoicePlan plan, String text, String language) async {
    final rate = plan.rate <= 0 ? 1.0 : plan.rate;
    var played = 0;
    var sampleRate = 0;
    final watch = Stopwatch()..start();
    try {
      await for (final pcm in synth.stream(text, speaker: plan.speaker, rate: rate, language: language)) {
        if (id != _id) return;
        if (pcm.samples.isEmpty || pcm.sampleRate <= 0) continue;
        if (played == 0) diag('call', 'first audio after ${watch.elapsedMilliseconds} ms ($plan)');
        _frames.addAll(levels(pcm, fps: _fps));
        sampleRate = pcm.sampleRate;
        played++;
        await output.play(id, pcm, last: false);
      }
    } catch (e) {
      diag('call', 'synthesis failed ($plan): $e');
      if (id != _id) return;
      if (plan.onServer) _serverFailed = true;
      if (played == 0) {
        final next = plan.onServer
            ? fallbackPlan(
                offer: _effectiveOffer,
                system: _system,
                language: language,
                agentId: agentId,
                rate: plan.rate,
              )
            : VoicePlan.system(chooseVoice(_system, language, agentId), plan.rate);
        return _speakWith(id, next, text, language);
      }
    }
    if (id != _id) return;
    if (played == 0) return _end(SpeakingEvent.finished);
    // The end of what was queued is still to be heard: a sample of silence
    // marks it as the last.
    await output.play(id, Pcm(Float32List(1), sampleRate), last: true);
  }

  void _onEvent((SpeakingEvent, int) e) {
    // Only the utterance asked for last counts: the end of one that was
    // replaced must not end the one that replaced it.
    if (e.$2 != _id) return;
    switch (e.$1) {
      case SpeakingEvent.started:
        _events.add(SpeakingEvent.started);
        if (_frames.isNotEmpty) {
          _clock = Stopwatch()..start();
          _tick?.cancel();
          _tick = Timer.periodic(const Duration(microseconds: 1000000 ~/ _fps), (_) => _frame());
        }
      case SpeakingEvent.word:
        _events.add(SpeakingEvent.word);
      case SpeakingEvent.finished || SpeakingEvent.cancelled:
        _end(e.$1);
    }
  }

  void _frame() {
    final c = _clock;
    if (c == null) return;
    final i = c.elapsedMicroseconds * _fps ~/ 1000000;
    _level.value = i < _frames.length ? _frames[i] : 0;
  }

  void _end(SpeakingEvent e) {
    _tick?.cancel();
    _tick = null;
    _clock = null;
    _level.value = null;
    final d = _done;
    _done = null;
    if (d == null) return;
    _events.add(e);
    if (!d.isCompleted) d.complete();
  }

  @override
  Future<void> stop() async {
    // Whatever is still being synthesised for the utterance is dropped.
    _id++;
    _end(SpeakingEvent.cancelled);
    await output.stop();
  }

  @override
  void dispose() {
    _tick?.cancel();
    unawaited(_sub.cancel());
    for (final s in _synths.values) {
      unawaited(s.then((s) => s.close(), onError: (_) {}));
    }
    _synths.clear();
    output.dispose();
    unawaited(_events.close());
    _level.dispose();
  }
}

/// Fetches voice models from [api] into the device (#497), each once: with
/// [wait] the call waits for it, as while it opens; without, a voice not
/// there yet is fetched in the background and the system speaks meanwhile.
VoiceModelFetch fetchVoiceModel(CoveyApi api) => (SpeechModelInfo model, {required bool wait}) async {
  final m = SpeechModel.voice(model.name);
  if (wait || m.onDevice) return m.ensure(api);
  // Maybe fetched in an earlier call: looked for on the disk, not fetched.
  await m.refresh(api);
  if (m.onDevice) return m.ensure(api);
  unawaited(m.ensure(api));
  return null;
};

/// The organisation's speech server speaking [voice] for [agentId] (#497):
/// the instance streams its MP3, which the Mac decodes as it comes.
Synthesiser serverVoice(CoveyApi api, SpokenVoice voice, {required String agentId, VoiceOutput? decoder}) =>
    ServerSynthesiser(
      (text, {model = '', voice = '', rate = 0, language = '', instructions = ''}) => api.synthesizeSpeechStream(
        text,
        model: model,
        voice: voice,
        rate: rate,
        language: language,
        instructions: instructions,
        agent: agentId,
      ),
      decoder ?? _decoder,
      model: voice.model,
      voice: voice.voice,
      instructions: voice.instructions,
    );

/// Decoding needs the channel only, not a speaker's events.
final _decoder = _ChannelDecoder();

class _ChannelDecoder implements StreamDecoding {
  static const _channel = MethodChannel('covey/voice');

  @override
  Future<bool> openDecoder(int id) async => await _channel.invokeMethod<bool>('decodeOpen', {'id': id}) ?? false;

  @override
  Future<Pcm> decode(int id, Uint8List bytes) async {
    final r = await _channel.invokeMapMethod<String, Object?>('decode', {'id': id, 'bytes': bytes});
    final samples = r?['samples'];
    return Pcm(samples is Float32List ? samples : Float32List(0), (r?['sampleRate'] as num?)?.toInt() ?? 0);
  }

  @override
  Future<void> closeDecoder(int id) => _channel.invokeMethod<void>('decodeClose', {'id': id});
}
