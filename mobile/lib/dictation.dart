import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:record/record.dart';
import 'package:whisper_ggml/whisper_ggml.dart';

import 'api.dart';
import 'diagnostics.dart';
import 'parakeet.dart';
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

/// The microphone cut into segments at the speaker's pauses (#350), for any
/// recogniser: 16 kHz mono PCM16 from `record`; a segment ends at a pause of
/// ~0.7 s once it holds speech (or at 15 s regardless), its final text is
/// appended, and the next segment starts with the end of the text so far as
/// its context — which keeps sentences, names and the language going across
/// the cut. Audio arriving while one segment is finished and the next one
/// opened is held and fed to the next, so nothing is dropped.
///
/// A recogniser implements the three steps of a segment: [openSegment],
/// [feedSegment], [closeSegment]; while a segment grows it reports what it
/// has so far through [segmentPartial].
abstract class SegmentedEngine implements SpeechEngine {
  final _recorder = AudioRecorder();
  StreamSubscription<Uint8List>? _mic;

  late String modelPath;
  late String language;
  late void Function(String) _partials;

  bool _open = false;
  final List<Uint8List> _held = [];
  bool _switching = false;
  bool _stopping = false;
  Future<void>? _cut;

  String _committed = '';
  String _current = '';

  // The segmenter's state, in bytes of 16 kHz PCM16 (32 000 per second).
  int _segBytes = 0;
  int _silentBytes = 0;
  int _voicedBytes = 0;
  bool _segVoiced = false;
  int Function() _audioBytes = () => 0;
  double _floor = 0.005;

  static const _bytesPerSecond = 32000;
  static const _pauseBytes = _bytesPerSecond * 7 ~/ 10;
  static const _minSegBytes = _bytesPerSecond * 2;
  static const _maxSegBytes = _bytesPerSecond * 15;

  /// Which recogniser, for the log.
  String get kind;

  /// Keep the model loaded after a dictation, for the next one to start at
  /// once — dictate-anywhere on the desktop (#355). Freed on [dispose].
  bool keepLoaded = false;

  /// Loads what the recogniser needs once per dictation.
  Future<void> prepare() async {}

  /// Opens a segment; [context] is the end of the text so far, or null.
  Future<void> openSegment(String? context);

  /// One chunk of the open segment's audio.
  void feedSegment(Uint8List chunk);

  /// Finishes the open segment and returns its text.
  Future<String> closeSegment();

  /// Frees what [prepare] loaded.
  Future<void> release() async {}

  /// What the open segment says so far.
  void segmentPartial(String text) {
    _current = text.trim();
    _partials(_joined());
  }

  @override
  Future<bool> hasPermission() => _recorder.hasPermission();

  @override
  Future<void> start({
    required String modelPath,
    required String language,
    required void Function(String) partials,
    void Function(double)? level,
  }) async {
    this.modelPath = modelPath;
    this.language = language;
    _partials = partials;
    _committed = '';
    _current = '';
    _held.clear();
    _resetSegment();
    _log('listening with $kind, language $language');

    await prepare();
    await _openNext();
    // Raw: the platform's voice processing (echoCancel) was tried against
    // background noise (#359) and on macOS left the stream without sound —
    // record's converter does not follow the input format it switches to.
    final raw = await _recorder.startStream(
      const RecordConfig(encoder: AudioEncoder.pcm16bits, sampleRate: 16000, numChannels: 1),
    );
    var bytes = 0;
    final began = DateTime.now();
    var last = began;
    _audioBytes = () => bytes;
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

  /// Routes one chunk: to the open segment, or held while segments switch;
  /// and decides whether the segment ends here.
  void _take(Uint8List chunk, double rms) {
    if (!_open) {
      _held.add(chunk);
    } else {
      feedSegment(chunk);
    }
    // An adaptive floor: it falls quickly and rises slowly, so room tone
    // does not count as speech.
    _floor += (rms < _floor ? 0.5 : 0.0005) * (rms - _floor);
    _floor = _floor.clamp(0.0005, 0.01);
    final voiced = rms >= math.max(3 * _floor, 0.004);
    _segBytes += chunk.length;
    if (voiced) {
      _segVoiced = true;
      _voicedBytes += chunk.length;
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
    _voicedBytes = 0;
    _segVoiced = false;
  }

  /// Less voice than this in a segment is a knock, a rustle, a cough — not
  /// a word; its text is dropped (#359).
  static const _minVoicedBytes = _bytesPerSecond ~/ 4;

  Future<void> _openNext() async {
    await openSegment(_committed.isEmpty ? null : _promptTail(_committed));
    _open = true;
    _current = '';
    // What arrived while the previous segment was being finished.
    for (final c in _held) {
      feedSegment(c);
    }
    _held.clear();
  }

  /// Ends the current segment and opens the next one.
  Future<void> _nextSegment() async {
    _switching = true;
    final hadVoice = _segVoiced && _voicedBytes >= _minVoicedBytes;
    if (_segVoiced && !hadVoice) _log('segment dropped: ${_voicedBytes * 1000 ~/ _bytesPerSecond} ms of voice');
    _resetSegment();
    try {
      await _close(keep: hadVoice);
      if (!_stopping) await _openNext();
    } catch (e) {
      _log('segment switch failed: $e');
    } finally {
      _switching = false;
    }
  }

  Future<void> _close({required bool keep}) async {
    if (!_open) return;
    _open = false;
    final watch = Stopwatch()..start();
    final text = (await closeSegment()).trim();
    _current = '';
    if (keep && text.isNotEmpty && !_isHallucination(text)) {
      _committed = _committed.isEmpty ? text : '$_committed $text';
    }
    _log('segment done, ${text.length} characters, finished in ${watch.elapsedMilliseconds} ms');
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
    if (_open) {
      for (final c in _held) {
        feedSegment(c);
      }
      _held.clear();
    }
    await _close(keep: _voicedBytes >= _minVoicedBytes);
    if (!keepLoaded) await release();
    _stopping = false;
    _log('stopped, ${_committed.length} characters from ${(_audioBytes() / _bytesPerSecond).toStringAsFixed(1)} s of audio');
    return _committed;
  }

  @override
  void dispose() {
    unawaited(stop().catchError((_) => '').then((_) => release()));
    unawaited(_recorder.dispose());
  }
}

/// whisper.cpp (#348): one whisper_ggml live session per segment. The live
/// session re-decodes its window every 1.5 s of voiced audio, which is the
/// preview; the segment's end is its final text. whisper_ggml alone commits
/// only at 25 s, and on a phone's CPU a window that long decodes slower than
/// it grows — the segments keep it short. The model stays loaded between
/// segments, and the context goes in as whisper's prompt.
class WhisperEngine extends SegmentedEngine {
  WhisperLiveSession? _session;
  StreamSubscription<String>? _sub;

  @override
  String get kind => 'whisper';

  @override
  Future<void> openSegment(String? context) async {
    final session = await startWhisperLiveSession(
      modelPath: modelPath,
      lang: language,
      initialPrompt: context,
      keepModelLoaded: true,
    );
    _session = session;
    _sub = session.partials.listen(segmentPartial, onError: (Object e) => _log('whisper failed: $e'));
  }

  @override
  void feedSegment(Uint8List chunk) => _session?.feed(chunk);

  @override
  Future<String> closeSegment() async {
    final session = _session;
    _session = null;
    if (session == null) return '';
    final text = await session.stop();
    await _sub?.cancel();
    _sub = null;
    return text;
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
  Dictation({this.api, this.engine, SpeechModel? model, this.keepModelLoaded = false})
    : _model = model ?? SpeechModel.instance;

  /// Keep the recogniser's model loaded between dictations (#355).
  final bool keepModelLoaded;

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

  /// Whether [engine] was picked here (and may be swapped when the model's
  /// engine changes) rather than handed in by a test.
  bool _picked = false;

  /// The recogniser for the model in use (#353): a test's stand-in as it
  /// is, otherwise whisper.cpp or Parakeet, whichever the model is for.
  SpeechEngine _engineFor(String kind) {
    final e = engine;
    if (e != null && !_picked) return e;
    final wanted = kind == 'parakeet' ? 'parakeet' : 'whisper';
    if (e is SegmentedEngine && e.kind == wanted) return e;
    e?.dispose();
    _picked = true;
    final next = wanted == 'parakeet' ? ParakeetEngine() : WhisperEngine();
    next.keepLoaded = keepModelLoaded;
    return engine = next;
  }

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

    final kind = _model.engine;
    final eng = _engineFor(kind);
    // whisper.cpp opens one file; sherpa-onnx the model's directory.
    final modelPath = kind == 'whisper' ? (_model.singleFile ?? path) : path;
    try {
      if (!await eng.hasPermission()) return _fail(DictationFailure.denied, null);
      _running = true;
      notifyListeners();
      await eng.start(
        modelPath: modelPath,
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
