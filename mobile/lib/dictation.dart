import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:record/record.dart';
import 'package:whisper_ggml/whisper_ggml.dart';

import 'api.dart';
import 'speech_model.dart';

/// Why dictation could not start.
enum DictationFailure {
  /// Recording or recognition failed; [Dictation.detail] has the words.
  unavailable,

  /// The person has not allowed the microphone.
  denied,

  /// The instance is still fetching the speech model — minutes after an
  /// install, once.
  modelNotReady,

  /// Speech recognition is switched off on the instance.
  off,
}

/// The seam to the recogniser, so tests can stand in for whisper.cpp and the
/// microphone.
abstract class SpeechEngine {
  /// Starts listening; [partials] carry the whole text so far, [level] the
  /// loudness of what the microphone hears, 0–1.
  Future<void> start({
    required String modelPath,
    required String language,
    required void Function(String) partials,
    void Function(double)? level,
  });

  /// Stops and returns the final text.
  Future<String> stop();

  Future<bool> hasPermission();

  void dispose();
}

/// whisper.cpp on the microphone: 16 kHz mono PCM16 from `record`, into a
/// live whisper session that re-decodes the current window and commits it
/// every ~25 s, so a meeting of an hour costs what a minute does.
class WhisperEngine implements SpeechEngine {
  final _recorder = AudioRecorder();
  final _whisper = WhisperController();
  WhisperLiveSession? _session;
  StreamSubscription<String>? _sub;

  @override
  Future<bool> hasPermission() => _recorder.hasPermission();

  @override
  Future<void> start({
    required String modelPath,
    required String language,
    required void Function(String) partials,
    void Function(double)? level,
  }) async {
    final raw = await _recorder.startStream(
      const RecordConfig(encoder: AudioEncoder.pcm16bits, sampleRate: 16000, numChannels: 1),
    );
    // The level is measured on the way to whisper: it is what says whether
    // the microphone delivers sound at all, which no transcript can.
    var bytes = 0;
    var last = DateTime.now();
    final Stream<Uint8List> pcm = raw.map((chunk) {
      bytes += chunk.length;
      final rms = _rms(chunk);
      level?.call(rms);
      final now = DateTime.now();
      if (now.difference(last) > const Duration(seconds: 3)) {
        last = now;
        debugPrint('dictation: ${bytes ~/ 32000} s of audio, rms ${rms.toStringAsFixed(4)}');
      }
      return chunk;
    });
    final session = await _whisper.transcribeLive(
      modelPath: modelPath,
      pcm16Stream: pcm,
      lang: language,
      // The model stays loaded between two dictations in one note: loading
      // it takes seconds, and the second sentence should not wait for that.
      keepModelLoaded: true,
    );
    _session = session;
    _sub = session.partials.listen((t) {
      debugPrint('dictation: partial, ${t.length} characters');
      partials(t);
    }, onError: (Object e) => debugPrint('dictation: whisper failed: $e'));
  }

  @override
  Future<String> stop() async {
    await _recorder.stop();
    final text = await _session?.stop() ?? '';
    await _sub?.cancel();
    _session = null;
    _sub = null;
    return text;
  }

  @override
  void dispose() {
    unawaited(stop().catchError((_) => ''));
    unawaited(_recorder.dispose());
  }
}

/// Root mean square of 16-bit little-endian PCM, scaled so speech at a
/// normal distance lands around the middle: 0–1.
double _rms(Uint8List b) {
  final data = ByteData.sublistView(b);
  final n = b.length ~/ 2;
  if (n == 0) return 0;
  var sum = 0.0;
  for (var i = 0; i < n; i++) {
    final v = data.getInt16(i * 2, Endian.little) / 32768.0;
    sum += v * v;
  }
  final rms = math.sqrt(sum / n);
  // Perceived loudness is logarithmic: -60 dB → 0, 0 dB → 1.
  if (rms <= 0) return 0;
  final db = 20 * math.log(rms) / math.ln10;
  return ((db + 60) / 60).clamp(0.0, 1.0);
}

/// Speech to text on the device (#336, #348): whisper.cpp turns speech into
/// text on the phone, with a model that came from the covey instance. Only
/// text leaves the phone; no audio is kept or sent.
///
/// The first dictation may have to fetch the model (≈150 MB); [preparing]
/// and [progress] say so while it does. [language] is what whisper listens
/// for — the app's language unless set otherwise.
class Dictation extends ChangeNotifier {
  Dictation({this.api, this.engine, SpeechModel? model}) : _model = model ?? SpeechModel.instance;

  final CoveyApi? api;

  /// The recogniser; whisper.cpp unless a test stands in.
  SpeechEngine? engine;
  final SpeechModel _model;
  String _text = '';
  bool _running = false;
  bool _preparing = false;
  DictationFailure? failure;
  String language = 'de';

  /// What went wrong in words, for the message.
  String? detail;

  bool get running => _running;

  /// True while the model is being fetched, before listening starts.
  bool get preparing => _preparing;

  /// The model download's fraction, 0–1, while [preparing].
  double? get progress => _model.progress;

  /// How loud the microphone is right now, 0–1, while [running].
  double level = 0;
  DateTime _levelAt = DateTime.fromMillisecondsSinceEpoch(0);

  /// The recent levels, oldest first, one every 50 ms: the waveform that
  /// runs along under the preview.
  final List<double> levels = [];
  static const historyLength = 120;

  /// Everything recognised so far.
  String get text => _text.trim();

  SpeechEngine get _eng => engine ??= WhisperEngine();

  void _modelChanged() => notifyListeners();

  /// Starts listening. Returns false and sets [failure] when it cannot.
  /// [continuous] is kept for the callers: whisper's live session runs until
  /// [stop] either way.
  Future<bool> start({bool continuous = false}) async {
    failure = null;
    detail = null;
    _text = '';
    levels.clear();
    final api = this.api;
    if (api == null) return _fail(DictationFailure.unavailable, 'no instance');

    _preparing = true;
    _model.addListener(_modelChanged);
    notifyListeners();
    final String? path;
    try {
      path = await _model.ensure(api);
    } finally {
      _model.removeListener(_modelChanged);
      _preparing = false;
    }
    if (path == null) {
      return _fail(switch (_model.problem) {
        SpeechModelProblem.off => DictationFailure.off,
        SpeechModelProblem.notReady => DictationFailure.modelNotReady,
        _ => DictationFailure.unavailable,
      }, _model.detail);
    }

    try {
      if (!await _eng.hasPermission()) return _fail(DictationFailure.denied, null);
      _running = true;
      notifyListeners();
      await _eng.start(
        modelPath: path,
        language: language,
        partials: (t) {
          _text = t;
          notifyListeners();
        },
        level: (l) {
          level = l;
          // A level meter at ~20 frames a second is enough to look alive.
          final now = DateTime.now();
          if (now.difference(_levelAt) > const Duration(milliseconds: 50)) {
            _levelAt = now;
            levels.add(l);
            if (levels.length > historyLength) levels.removeRange(0, levels.length - historyLength);
            notifyListeners();
          }
        },
      );
    } catch (e) {
      _running = false;
      return _fail(DictationFailure.unavailable, '$e');
    }
    return true;
  }

  bool _fail(DictationFailure f, String? d) {
    failure = f;
    detail = d;
    notifyListeners();
    return false;
  }

  /// Stops and returns the whole text.
  Future<String> stop() async {
    level = 0;
    if (_running) {
      _running = false;
      notifyListeners();
      try {
        final last = await _eng.stop();
        if (last.trim().isNotEmpty) _text = last;
      } catch (e) {
        detail = '$e';
      }
    }
    notifyListeners();
    return text;
  }

  @override
  void dispose() {
    _running = false;
    engine?.dispose();
    super.dispose();
  }
}
