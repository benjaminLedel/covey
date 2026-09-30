import 'dart:async';
import 'dart:io' show Platform;
import 'dart:typed_data';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:record/record.dart';

import '../api.dart';
import '../diagnostics.dart';
import '../sherpa.dart';
import '../speech_model.dart';
import 'capture.dart';

/// Why a call cannot listen.
enum CallProblem {
  /// The person has not allowed the microphone.
  denied,

  /// The instance is still fetching a model — minutes after an install.
  modelNotReady,

  /// Speech recognition is switched off on the instance, or it is older
  /// than calls and offers no voice detector.
  off,

  /// Anything else; the words are in the exception.
  unavailable,
}

class CallException implements Exception {
  CallException(this.problem, [this.detail]);
  final CallProblem problem;
  final String? detail;

  @override
  String toString() => detail == null ? problem.name : '${problem.name}: $detail';
}

/// One window of the call's audio: 32 ms of 16 kHz mono PCM16, whether the
/// voice detector hears voice in it, and how loud it is, 0–1.
typedef CallWindow = void Function(Uint8List pcm, bool voiced, double level);

/// The seam to the microphone, the voice detector and the recogniser, so a
/// test feeds windows of its own.
abstract class CallEars {
  /// Fetches what is missing and loads it; throws [CallException].
  Future<void> prepare();

  /// Starts hearing; [onWindow] gets every window.
  Future<void> listen(CallWindow onWindow);

  /// Stops hearing, the microphone closed; [listen] opens it again.
  Future<void> pause();

  /// The text of one turn.
  Future<String> recognise(Uint8List pcm);

  /// Frees everything; the microphone is closed.
  Future<void> close();

  /// Whether the microphone is echo-cancelled (#507): the agent's own voice
  /// from the loudspeaker is taken out before the call hears it.
  bool get echoCancelled;

  /// When voice processing last started, while it is on (#511): the
  /// canceller needs a moment ([echoWarmUp]) before it holds. Null when it
  /// is off or not known.
  DateTime? get echoCancelledSince;
}

/// The Mac's microphone, Silero and Parakeet (or SenseVoice), all on the
/// device (#494): the models come from the covey instance the way
/// dictation's does, and no audio leaves the device — only the text of a
/// finished turn does, as a message.
class DeviceEars implements CallEars {
  DeviceEars(this.api, {List<CaptureSource>? sources, this.language = ''}) : _sources = sources ?? defaultSources();

  final CoveyApi api;

  /// The call's language, BCP 47: SenseVoice is pinned to it where it knows
  /// it (#511). Parakeet has no such option and detects the language itself;
  /// the call flags a turn recognised in another one in its diagnostics.
  final String language;

  /// Where the microphone comes from, the preferred first (#507): on the
  /// Mac the call's own audio engine with voice processing, then the
  /// `record` package.
  final List<CaptureSource> _sources;

  static List<CaptureSource> defaultSources() => [
    if (!kIsWeb && Platform.isMacOS) NativeVoiceCapture(),
    RecordCapture(),
  ];

  /// The source that started last; one that failed is not tried again in
  /// the same call.
  int _source = 0;
  CaptureInfo? _info;
  StreamSubscription<Float32List>? _mic;
  SherpaDecoder? _decoder;
  SileroDetector? _vad;
  final _chunker = FrameChunker(SileroDetector.window);

  @override
  bool get echoCancelled => _info?.echoCancelled ?? false;

  @override
  DateTime? get echoCancelledSince => echoCancelled ? _since : null;
  DateTime? _since;

  @override
  Future<void> prepare() async {
    final speech = SpeechModel.instance;
    final path = await speech.ensure(api);
    if (path == null) throw CallException(_problem(speech.problem), speech.detail);
    final vadPath = await SpeechModel.vad.ensure(api);
    if (vadPath == null) {
      // An instance from before calls offers no detector: a 404, which
      // reads as "off" here.
      throw CallException(
        SpeechModel.vad.problem == SpeechModelProblem.failed && (SpeechModel.vad.detail ?? '').contains('not offered')
            ? CallProblem.off
            : _problem(SpeechModel.vad.problem),
        SpeechModel.vad.detail,
      );
    }
    final asked = AudioRecorder();
    try {
      if (!await asked.hasPermission()) throw CallException(CallProblem.denied);
    } finally {
      unawaited(asked.dispose());
    }
    final watch = Stopwatch()..start();
    _decoder ??= await SherpaDecoder.load(path, speech.engine, language: language);
    _vad ??= SileroDetector.load(vadPath);
    final pinned = speech.engine == 'sensevoice' ? senseVoiceLanguage(language) : '';
    diag(
      'call',
      '${speech.engine} and silero loaded in ${watch.elapsedMilliseconds} ms, '
          'language ${pinned.isEmpty ? 'detected by the model' : pinned}',
    );
  }

  static CallProblem _problem(SpeechModelProblem? p) => switch (p) {
    SpeechModelProblem.off => CallProblem.off,
    SpeechModelProblem.notReady => CallProblem.modelNotReady,
    _ => CallProblem.unavailable,
  };

  @override
  Future<void> listen(CallWindow onWindow) async {
    if (_mic != null) return;
    final vad = _vad;
    if (vad == null) throw CallException(CallProblem.unavailable, 'not prepared');
    vad.reset();
    _chunker.reset();
    final (int, OpenCapture) opened;
    try {
      opened = await openCapture(_sources, from: _source);
    } on CaptureDeniedException {
      throw CallException(CallProblem.denied);
    } catch (e) {
      throw CallException(CallProblem.unavailable, 'the microphone did not start: $e');
    }
    final first = _info == null || _source != opened.$1;
    _source = opened.$1;
    _info = opened.$2.info;
    if (_info!.echoCancelled) {
      _since = DateTime.now();
      diag(
        'call',
        'echo cancellation on: barge-in at the tuned confirmation for ${echoWarmUp.inSeconds} s while it settles',
      );
    }
    _mic = opened.$2.frames.listen((chunk) {
      for (final w in _chunker.add(chunk)) {
        onWindow(floatsPcm16(w), vad.feed(w), loudness(w));
      }
    });
    diag('call', first ? 'listening, ${opened.$2.info}' : 'listening');
  }

  @override
  Future<void> pause() async {
    final mic = _mic;
    _mic = null;
    if (mic == null) return;
    await _sources[_source].stop();
    await mic.cancel();
    diag('call', 'microphone closed');
  }

  @override
  Future<String> recognise(Uint8List pcm) async {
    final d = _decoder;
    if (d == null) return '';
    return d.decode(pcm);
  }

  @override
  Future<void> close() async {
    await pause();
    _decoder?.close();
    _decoder = null;
    _vad?.free();
    _vad = null;
    for (final s in _sources) {
      try {
        await s.dispose();
      } catch (_) {}
    }
  }
}
