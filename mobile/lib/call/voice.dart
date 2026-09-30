import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../diagnostics.dart';
import 'provider.dart';
import 'speech_text.dart';
import 'spoken_voice.dart';

/// What the voice reports while it speaks (#494): it began, it reached a
/// word (the Mac's synthesis only; the face's mouth moves on each), it
/// finished or was stopped.
enum SpeakingEvent { started, word, finished, cancelled }

/// The seam between a call and whatever speaks for the agent, so a test
/// stands in.
abstract class Speaker {
  /// Gets ready to speak: the Mac's voices, whether the voice provider can
  /// be asked, and how the agent sounds there. Returns a line for the
  /// diagnostics log.
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

  /// How open the mouth is, 0–1, frame by frame, while the voice provider
  /// speaks: taken from its audio. Null while the Mac's synthesis speaks,
  /// which reports words instead ([SpeakingEvent.word]).
  ValueListenable<double?> get level;

  /// True while the Mac's own voice speaks because the voice provider is
  /// not set or could not be reached: the call says so in a line.
  ValueListenable<bool> get fallback;

  void dispose();
}

/// Where speech comes out on the Mac: the system's synthesis, and the
/// playback of the voice provider's audio (#497) — both through the
/// `covey/voice` channel (MainFlutterWindow.swift). Each utterance carries
/// an id; events name the id they belong to.
abstract class VoiceOutput implements StreamDecoding {
  Future<List<SystemVoice>> voices();
  Future<String?> language(String text);

  /// The system's synthesis speaks [text], at [rate] times its own pace
  /// (0 leaves it).
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

/// The agent's voice in a call (#497): the organisation's voice provider,
/// its audio played as it streams in, the face's mouth following its
/// level. Only when the provider is not set, or fails before anything was
/// heard, the Mac's own synthesis speaks — in a voice of the language
/// picked per agent ([chooseVoice]) — and [fallback] says so. A provider
/// that failed is not asked again in the same call.
class AgentSpeaker implements Speaker {
  AgentSpeaker({
    required this.output,
    required this.agentId,
    required this.available,
    required this.spoken,
    this.provider,
  }) {
    _sub = output.events.listen(_onEvent);
  }

  final VoiceOutput output;
  final String agentId;

  /// Whether the organisation has a voice provider (GET /speech/model).
  final Future<bool> Function() available;

  /// How the agent sounds at the provider.
  final Future<SpokenVoice> Function() spoken;

  /// The provider speaking a voice; null when this app cannot reach one.
  final ProviderVoice Function(SpokenVoice voice)? provider;

  late final StreamSubscription<(SpeakingEvent, int)> _sub;
  final _events = StreamController<SpeakingEvent>.broadcast();
  final _level = ValueNotifier<double?>(null);
  final _fallback = ValueNotifier<bool>(false);

  List<SystemVoice> _system = const [];
  ProviderVoice? _voice;

  /// The agent's speed, which the Mac's voice keeps too.
  double _speed = 0;
  bool _prepared = false;

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
  ValueListenable<bool> get fallback => _fallback;

  @override
  Future<String> prepare({required String language}) async {
    _system = await output.voices();
    var set = false;
    try {
      set = await available();
    } catch (e) {
      diag('call', 'whether there is a voice provider could not be read: $e');
    }
    var voice = const SpokenVoice();
    if (set) {
      try {
        voice = await spoken();
      } catch (e) {
        diag('call', 'the agent\'s spoken voice could not be read: $e');
      }
    }
    final make = provider;
    _voice = set && make != null ? make(voice) : null;
    _speed = voice.speed;
    _fallback.value = _voice == null;
    _prepared = true;
    return '${_system.length} system voices, '
        '${_voice == null ? 'no voice provider, the Mac speaks' : '$voice'}';
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
    _frames.clear();
    _clock = null;
    final voice = _voice;
    if (voice == null) {
      await _say(id, text, language);
    } else {
      unawaited(_play(id, voice, text, language));
    }
    return done.future;
  }

  Future<void> _say(int id, String text, String language) async {
    _level.value = null;
    await output.say(id, text, voiceId: chooseVoice(_system, language, agentId)?.id, language: language, rate: _speed);
  }

  /// Plays what the provider makes of [text] piece by piece as it comes. A
  /// provider that fails before anything was heard hands the text to the
  /// Mac's voice, for this reply and the rest of the call.
  Future<void> _play(int id, ProviderVoice voice, String text, String language) async {
    var played = 0;
    var sampleRate = 0;
    final watch = Stopwatch()..start();
    try {
      await for (final pcm in voice.stream(text, language: language)) {
        if (id != _id) return;
        if (pcm.samples.isEmpty || pcm.sampleRate <= 0) continue;
        if (played == 0) diag('call', 'first audio after ${watch.elapsedMilliseconds} ms');
        _frames.addAll(levels(pcm, fps: _fps));
        sampleRate = pcm.sampleRate;
        played++;
        await output.play(id, pcm, last: false);
      }
    } catch (e) {
      diag('call', 'the voice provider failed: $e');
      if (id != _id) return;
      _voice = null;
      _fallback.value = true;
      if (played == 0) return _say(id, text, language);
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
    // Whatever the provider still sends for the utterance is dropped.
    _id++;
    _end(SpeakingEvent.cancelled);
    await output.stop();
  }

  @override
  void dispose() {
    _tick?.cancel();
    unawaited(_sub.cancel());
    output.dispose();
    unawaited(_events.close());
    _level.dispose();
    _fallback.dispose();
  }
}

/// The organisation's voice provider speaking [voice] for [agentId]
/// (#497): the instance streams its MP3, which the Mac decodes as it comes.
ProviderVoice providerVoice(CoveyApi api, SpokenVoice voice, {required String agentId, StreamDecoding? decoder}) =>
    ProviderVoice(
      (text, {voice = '', speed = 0, language = '', instructions = ''}) => api.synthesizeSpeechStream(
        text,
        voice: voice,
        speed: speed,
        language: language,
        instructions: instructions,
        agent: agentId,
      ),
      decoder ?? _decoder,
      voice: voice,
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
