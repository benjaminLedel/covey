import 'dart:async';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:record/record.dart';

import '../api.dart';
import '../diagnostics.dart';
import '../sherpa.dart';
import '../speech_model.dart';

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
}

/// The Mac's microphone, Silero and Parakeet (or SenseVoice), all on the
/// device (#494): the models come from the covey instance the way
/// dictation's does, and no audio leaves the device — only the text of a
/// finished turn does, as a message.
class DeviceEars implements CallEars {
  DeviceEars(this.api);

  final CoveyApi api;
  final _recorder = AudioRecorder();
  StreamSubscription<Uint8List>? _mic;
  SherpaDecoder? _decoder;
  SileroDetector? _vad;
  final _pending = BytesBuilder();

  static const _windowBytes = SileroDetector.window * 2;

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
    if (!await _recorder.hasPermission()) throw CallException(CallProblem.denied);
    final watch = Stopwatch()..start();
    _decoder ??= await SherpaDecoder.load(path, speech.engine);
    _vad ??= SileroDetector.load(vadPath);
    diag('call', '${speech.engine} and silero loaded in ${watch.elapsedMilliseconds} ms');
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
    _pending.clear();
    // Raw, as dictation records (#359): record's voice processing left the
    // stream silent on macOS.
    final raw = await _recorder.startStream(
      const RecordConfig(encoder: AudioEncoder.pcm16bits, sampleRate: 16000, numChannels: 1),
    );
    _mic = raw.listen((chunk) {
      _pending.add(chunk);
      if (_pending.length < _windowBytes) return;
      final all = _pending.takeBytes();
      var at = 0;
      for (; at + _windowBytes <= all.length; at += _windowBytes) {
        final w = Uint8List.fromList(Uint8List.sublistView(all, at, at + _windowBytes));
        final samples = _floats(w);
        onWindow(w, vad.feed(samples), _loudness(samples));
      }
      if (at < all.length) _pending.add(Uint8List.sublistView(all, at));
    });
    diag('call', 'listening');
  }

  @override
  Future<void> pause() async {
    final mic = _mic;
    _mic = null;
    if (mic == null) return;
    await _recorder.stop();
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
    await _recorder.dispose();
  }
}

Float32List _floats(Uint8List pcm16) {
  final data = ByteData.sublistView(pcm16);
  final out = Float32List(pcm16.length ~/ 2);
  for (var i = 0; i < out.length; i++) {
    out[i] = data.getInt16(i * 2, Endian.little) / 32768.0;
  }
  return out;
}

/// Perceived loudness, 0–1: -60 dB → 0, 0 dB → 1.
double _loudness(Float32List s) {
  if (s.isEmpty) return 0;
  var sum = 0.0;
  for (final v in s) {
    sum += v * v;
  }
  final rms = math.sqrt(sum / s.length);
  if (rms <= 0) return 0;
  final db = 20 * math.log(rms) / math.ln10;
  return ((db + 60) / 60).clamp(0.0, 1.0);
}
