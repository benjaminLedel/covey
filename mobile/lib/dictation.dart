import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:record/record.dart';

import 'api.dart';
import 'diagnostics.dart';
import 'sherpa.dart';
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

/// The seam to the recogniser, so tests can stand in for sherpa-onnx and the
/// microphone.
abstract class SpeechEngine {
  /// Starts listening; [partials] carry the whole text so far, [level] the
  /// loudness of what the microphone hears, 0–1.
  Future<void> start({
    required String modelPath,
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
/// appended, and the next segment opens. Audio arriving while one segment is finished and the next one
/// opened is held and fed to the next, so nothing is dropped.
///
/// A recogniser implements the three steps of a segment: [openSegment],
/// [feedSegment], [closeSegment]; while a segment grows it reports what it
/// has so far through [segmentPartial].
abstract class SegmentedEngine implements SpeechEngine {
  final _recorder = AudioRecorder();
  StreamSubscription<Uint8List>? _mic;

  late String modelPath;
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

  /// Where the audio comes from: the microphone unless set — a meeting's
  /// second source is the Mac's own audio (#364). 16 kHz mono PCM16.
  Stream<Uint8List> Function()? source;

  /// Each finished segment's text with the moment its speech began, for a
  /// transcript merged from two sources (#364), and its speaker embedding
  /// where the voices are told apart (#367).
  void Function(String text, DateTime at, Float32List? voice)? onSegment;

  /// The embedding of the segment just closed, if the recogniser makes one.
  Float32List? get lastEmbedding => null;
  DateTime? _segSpeechAt;

  /// Loads what the recogniser needs once per dictation.
  Future<void> prepare() async {}

  /// Opens a segment.
  Future<void> openSegment();

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
  Future<bool> hasPermission() async => source != null || await _recorder.hasPermission();

  @override
  Future<void> start({
    required String modelPath,
    required void Function(String) partials,
    void Function(double)? level,
  }) async {
    this.modelPath = modelPath;
    _partials = partials;
    _committed = '';
    _current = '';
    _held.clear();
    _resetSegment();
    _log('listening with $kind');

    await prepare();
    await _openNext();
    // Raw: the platform's voice processing (echoCancel) was tried against
    // background noise (#359) and on macOS left the stream without sound —
    // record's converter does not follow the input format it switches to.
    final raw =
        source?.call() ??
        await _recorder.startStream(
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
      _segSpeechAt ??= DateTime.now().subtract(Duration(milliseconds: chunk.length * 1000 ~/ _bytesPerSecond));
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
    _segSpeechAt = null;
    _segBytes = 0;
    _silentBytes = 0;
    _voicedBytes = 0;
    _segVoiced = false;
  }

  /// Less voice than this in a segment is a knock, a rustle, a cough — not
  /// a word; its text is dropped (#359).
  static const _minVoicedBytes = _bytesPerSecond ~/ 4;

  Future<void> _openNext() async {
    await openSegment();
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
    final at = _segSpeechAt;
    if (_segVoiced && !hadVoice) _log('segment dropped: ${_voicedBytes * 1000 ~/ _bytesPerSecond} ms of voice');
    _resetSegment();
    try {
      await _close(keep: hadVoice, at: at);
      if (!_stopping) await _openNext();
    } catch (e) {
      _log('segment switch failed: $e');
    } finally {
      _switching = false;
    }
  }

  Future<void> _close({required bool keep, DateTime? at}) async {
    if (!_open) return;
    _open = false;
    final watch = Stopwatch()..start();
    final text = (await closeSegment()).trim();
    _current = '';
    if (keep && text.isNotEmpty && !_isHallucination(text)) {
      _committed = _committed.isEmpty ? text : '$_committed $text';
      onSegment?.call(text, at ?? DateTime.now(), lastEmbedding);
    }
    _log('segment done, ${text.length} characters, finished in ${watch.elapsedMilliseconds} ms');
    _partials(_joined());
  }

  String _joined() => [_committed, _current].where((s) => s.isNotEmpty).join(' ');

  @override
  Future<String> stop() async {
    _stopping = true;
    if (source == null) await _recorder.stop();
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
    await _close(keep: _voicedBytes >= _minVoicedBytes, at: _segSpeechAt);
    if (!keepLoaded) await release();
    _stopping = false;
    _log(
      'stopped, ${_committed.length} characters from ${(_audioBytes() / _bytesPerSecond).toStringAsFixed(1)} s of audio',
    );
    return _committed;
  }

  @override
  void dispose() {
    unawaited(stop().catchError((_) => '').then((_) => release()));
    unawaited(_recorder.dispose());
  }
}

/// What a recogniser makes of noise rather than speech — a bracketed event
/// ("[Musik]"), subtitle credits from training data — never a dictated
/// sentence.
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

/// Speech to text on the device (#336, #366): sherpa-onnx turns speech into
/// text on the device, with a model that came from the covey instance. Only
/// text leaves the device; no audio is kept or sent.
///
/// The first dictation may have to fetch the model (hundreds of MB);
/// [preparing] and [progress] say so while it does. The model recognises the
/// language itself.
class Dictation extends ChangeNotifier {
  Dictation({
    this.api,
    this.engine,
    SpeechModel? model,
    this.keepModelLoaded = false,
    this.source,
    this.onSegment,
    this.diarize = false,
  }) : _model = model ?? SpeechModel.instance;

  /// Tell the voices apart (#367): the speakers' model is fetched and each
  /// segment carries an embedding. Without the model, dictation goes on
  /// without it.
  final bool diarize;

  /// Another audio source than the microphone (#364).
  final Stream<Uint8List> Function()? source;

  /// Each finished segment with the moment its speech began (#364) and its
  /// voice (#367).
  final void Function(String text, DateTime at, Float32List? voice)? onSegment;

  /// Keep the recogniser's model loaded between dictations (#355).
  final bool keepModelLoaded;

  final CoveyApi? api;

  /// The recogniser; sherpa-onnx unless a test stands in.
  SpeechEngine? engine;
  final SpeechModel _model;
  String _text = '';
  bool _running = false;
  bool _preparing = false;
  DictationFailure? failure;

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

  SpeechEngine get _eng => engine ??= SherpaEngine(_model.engine);

  /// Whether [engine] was picked here (and may be swapped when the model's
  /// engine changes) rather than handed in by a test.
  bool _picked = false;

  /// The recogniser for the model in use (#366): a test's stand-in as it
  /// is, otherwise sherpa-onnx with Parakeet or SenseVoice, whichever the
  /// model is.
  SpeechEngine _engineFor(String kind) {
    final e = engine;
    if (e != null && !_picked) return e;
    if (e is SherpaEngine && e.model == kind) return e;
    e?.dispose();
    _picked = true;
    return engine = SherpaEngine(kind)
      ..keepLoaded = keepModelLoaded
      ..source = source
      ..onSegment = onSegment;
  }

  void _modelChanged() => notifyListeners();

  /// Starts listening. Returns false and sets [failure] when it cannot.
  /// [continuous] is kept for the callers: the segmentation runs until
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

    final eng = _engineFor(_model.engine);
    if (diarize && eng is SherpaEngine && eng.speakerModelPath == null) {
      eng.speakerModelPath = await SpeechModel.speaker.ensure(api);
      if (eng.speakerModelPath == null) {
        diag('dictation', 'voices not told apart: ${SpeechModel.speaker.detail ?? 'no model'}');
      }
    }
    try {
      if (!await eng.hasPermission()) return _fail(DictationFailure.denied, null);
      _running = true;
      notifyListeners();
      await eng.start(
        modelPath: path,
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
