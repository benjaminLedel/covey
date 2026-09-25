import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:record/record.dart';
import 'package:whisper_ggml/whisper_ggml.dart';

import 'api.dart';
import 'diagnostics.dart';
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

/// whisper.cpp on the microphone: 16 kHz mono PCM16 from `record`, cut into
/// segments at the speaker's pauses (#349).
///
/// whisper_ggml's live session re-decodes its whole window every 1.5 s and
/// commits only at 25 s. On a phone's CPU a window of 20 s takes longer to
/// decode than 1.5 s, so a long dictation falls behind, and words straddling
/// the 25 s boundary are lost. Here a segment ends at a pause of ~0.7 s once
/// it holds speech (or at 15 s regardless): its session is stopped, its final
/// text appended, and the next segment's session starts with the end of the
/// text so far as its prompt — which keeps sentences, spelling of names and
/// the language going across the cut. Audio arriving during the switch is
/// held and fed to the next session, so nothing is dropped. The model stays
/// loaded between segments.
class WhisperEngine implements SpeechEngine {
  final _recorder = AudioRecorder();
  StreamSubscription<Uint8List>? _mic;

  late String _modelPath;
  late String _language;
  late void Function(String) _partials;

  WhisperLiveSession? _session;
  StreamSubscription<String>? _sub;
  final List<Uint8List> _held = [];
  bool _switching = false;
  bool _stopping = false;
  Future<void>? _cut;

  String _committed = '';
  String _current = '';

  // The segmenter's state, in bytes of 16 kHz PCM16 (32 000 per second).
  int _segBytes = 0;
  int _silentBytes = 0;
  bool _segVoiced = false;
  double _floor = 0.005;

  static const _bytesPerSecond = 32000;
  static const _pauseBytes = _bytesPerSecond * 7 ~/ 10;
  static const _minSegBytes = _bytesPerSecond * 2;
  static const _maxSegBytes = _bytesPerSecond * 15;

  @override
  Future<bool> hasPermission() => _recorder.hasPermission();

  @override
  Future<void> start({
    required String modelPath,
    required String language,
    required void Function(String) partials,
    void Function(double)? level,
  }) async {
    _modelPath = modelPath;
    _language = language;
    _partials = partials;
    _committed = '';
    _current = '';
    _held.clear();
    _resetSegment();
    _log('listening, language $language');

    await _open();
    final raw = await _recorder.startStream(
      const RecordConfig(encoder: AudioEncoder.pcm16bits, sampleRate: 16000, numChannels: 1),
    );
    var bytes = 0;
    final began = DateTime.now();
    var last = began;
    _mic = raw.listen((chunk) {
      bytes += chunk.length;
      final rms = _linearRms(chunk);
      level?.call(_loudness(rms));
      final now = DateTime.now();
      if (now.difference(last) > const Duration(seconds: 3)) {
        last = now;
        _log(
          '${(bytes / _bytesPerSecond).toStringAsFixed(1)} s audio in '
          '${(now.difference(began).inMilliseconds / 1000).toStringAsFixed(1)} s, rms ${rms.toStringAsFixed(4)}',
        );
      }
      _take(chunk, rms);
    });
  }

  /// Routes one chunk: to the session, or held while segments switch; and
  /// decides whether the segment ends here.
  void _take(Uint8List chunk, double rms) {
    // No session between two segments: held, and fed to the next one.
    if (_session == null) {
      _held.add(chunk);
    } else {
      _session!.feed(chunk);
    }
    // An adaptive floor, as whisper_ggml's own gate keeps one: it falls
    // quickly and rises slowly, so room tone does not count as speech.
    _floor += (rms < _floor ? 0.5 : 0.0005) * (rms - _floor);
    _floor = _floor.clamp(0.0005, 0.01);
    final voiced = rms >= math.max(3 * _floor, 0.004);
    _segBytes += chunk.length;
    if (voiced) {
      _segVoiced = true;
      _silentBytes = 0;
    } else {
      _silentBytes += chunk.length;
    }
    if (_switching || _stopping) return;
    final pause = _segVoiced && _silentBytes >= _pauseBytes && _segBytes >= _minSegBytes;
    if (pause || _segBytes >= _maxSegBytes) _cut = _nextSegment();
  }

  void _resetSegment() {
    _segBytes = 0;
    _silentBytes = 0;
    _segVoiced = false;
  }

  Future<void> _open() async {
    final session = await startWhisperLiveSession(
      modelPath: _modelPath,
      lang: _language,
      // The end of what has been said so far: whisper continues it rather
      // than starting a new text, in the same language and spelling.
      initialPrompt: _committed.isEmpty ? null : _promptTail(_committed),
      keepModelLoaded: true,
    );
    _session = session;
    _current = '';
    _sub = session.partials.listen((t) {
      _current = t.trim();
      _partials(_joined());
    }, onError: (Object e) => _log('whisper failed: $e'));
    // What arrived while the previous segment was being finished.
    for (final c in _held) {
      session.feed(c);
    }
    _held.clear();
  }

  /// Ends the current segment and opens the next one.
  Future<void> _nextSegment() async {
    _switching = true;
    final hadVoice = _segVoiced;
    _resetSegment();
    try {
      await _close(keep: hadVoice);
      if (!_stopping) await _open();
    } catch (e) {
      _log('segment switch failed: $e');
    } finally {
      _switching = false;
    }
  }

  Future<void> _close({required bool keep}) async {
    final session = _session;
    _session = null;
    if (session == null) return;
    final text = (await session.stop()).trim();
    await _sub?.cancel();
    _sub = null;
    _current = '';
    if (keep && text.isNotEmpty && !_isHallucination(text)) {
      _committed = _committed.isEmpty ? text : '$_committed $text';
    }
    _log('segment done, ${text.length} characters');
    _partials(_joined());
  }

  String _joined() => [_committed, _current].where((s) => s.isNotEmpty).join(' ');

  @override
  Future<String> stop() async {
    _stopping = true;
    await _recorder.stop();
    await _mic?.cancel();
    _mic = null;
    await _cut;
    // The rest of the audio into the last segment, then finish it.
    final s = _session;
    if (s != null) {
      for (final c in _held) {
        s.feed(c);
      }
      _held.clear();
    }
    await _close(keep: true);
    _stopping = false;
    _log('stopped, ${_committed.length} characters');
    return _committed;
  }

  @override
  void dispose() {
    unawaited(stop().catchError((_) => ''));
    unawaited(_recorder.dispose());
  }
}

/// The last ~200 characters of [text], from a word boundary: whisper's
/// prompt holds 224 tokens, and the most recent words matter most.
String _promptTail(String text) {
  if (text.length <= 200) return text;
  final cut = text.substring(text.length - 200);
  final space = cut.indexOf(' ');
  return space >= 0 ? cut.substring(space + 1) : cut;
}

/// Whisper's well-known inventions on silence or noise — subtitle credits
/// from its training data — never a dictated sentence.
bool _isHallucination(String text) {
  final t = text.toLowerCase();
  return RegExp(r'^\W*(untertitel|subtitles|sous-titres|sottotitoli|ondertiteling|napisy)\b').hasMatch(t) ||
      RegExp(r'^\W*(\[.*\]|\(.*\)|\*.*\*)\W*$').hasMatch(t);
}

/// Into the diagnostic log (#352) and onto stderr: lengths and levels,
/// never what was said.
void _log(String line) => diag('dictation', line);

/// Root mean square of 16-bit little-endian PCM, 0–1 linear.
double _linearRms(Uint8List b) {
  final data = ByteData.sublistView(b);
  final n = b.length ~/ 2;
  if (n == 0) return 0;
  var sum = 0.0;
  for (var i = 0; i < n; i++) {
    final v = data.getInt16(i * 2, Endian.little) / 32768.0;
    sum += v * v;
  }
  return math.sqrt(sum / n);
}

/// Perceived loudness, 0–1: -60 dB → 0, 0 dB → 1 — for the waveform.
double _loudness(double rms) {
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

  /// Whether the instance itself is still fetching the model — before the
  /// phone can download it.
  bool get modelOnInstance => _model.onInstance;

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
