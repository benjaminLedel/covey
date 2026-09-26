import 'dart:async';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:sherpa_onnx/sherpa_onnx.dart' as sherpa;

import 'diagnostics.dart';
import 'dictation.dart';

/// Speech recognition through sherpa-onnx (#353, #366), the one engine the
/// app runs: NVIDIA's Parakeet TDT 0.6B v3 (25 European languages) or
/// FunASR's SenseVoice Small (Chinese, Cantonese, Japanese, Korean,
/// English). Both detect the language themselves and set punctuation.
///
/// Neither is a streaming model: each decodes a segment as a whole. That
/// fits the pause segmentation — a segment is decoded once it ends, and
/// about once a second while it grows, for the preview; both decode a
/// segment of a few seconds in a fraction of that on a phone. The
/// recogniser lives in a worker isolate, so decoding never blocks the UI,
/// and is loaded per dictation and freed after it unless [keepLoaded] — the
/// models are hundreds of MB, which a phone should not hold while nobody
/// dictates.
class SherpaEngine extends SegmentedEngine {
  SherpaEngine(this.model);

  /// `parakeet` or `sensevoice`.
  final String model;

  /// The speaker-embedding model's directory, when the voices are to be told
  /// apart (#367): each finished segment then carries an embedding.
  String? speakerModelPath;
  Float32List? _embedding;

  /// Less than this is too little voice for a reliable embedding: 1.5 s.
  static const _minEmbedBytes = 48000;

  @override
  Float32List? get lastEmbedding => _embedding;

  _Worker? _worker;
  final _segment = BytesBuilder();
  int _segmentId = 0;
  int _decodedBytes = 0;
  Future<String>? _inFlight;
  Timer? _tick;

  /// Re-decode a growing segment at most this often, and only once it has
  /// grown by at least half a second.
  static const _interval = Duration(milliseconds: 1000);
  static const _minNewBytes = 16000;

  @override
  String get kind => model;

  @override
  Future<void> prepare() async {
    if (_worker != null) return;
    final watch = Stopwatch()..start();
    _worker = await _Worker.spawn(modelPath, model, speakerModelPath);
    diag(
      'dictation',
      '$model loaded in ${watch.elapsedMilliseconds} ms${speakerModelPath == null ? '' : ', with the speakers\' model'}',
    );
  }

  @override
  Future<void> openSegment() async {
    _segmentId++;
    _segment.clear();
    _decodedBytes = 0;
    _tick?.cancel();
    final id = _segmentId;
    _tick = Timer.periodic(_interval, (_) => _preview(id));
  }

  void _preview(int id) {
    final w = _worker;
    if (w == null || _inFlight != null || _segment.length - _decodedBytes < _minNewBytes) return;
    final pcm = _segment.toBytes();
    _decodedBytes = pcm.length;
    final f = w.decode(pcm);
    _inFlight = f;
    f
        .then((text) {
          // A preview that arrives after its segment ended is stale.
          if (id == _segmentId && _tick != null) segmentPartial(text);
        })
        .catchError((Object e) {
          diag('dictation', '$model preview failed: $e');
        })
        .whenComplete(() => _inFlight = null);
  }

  @override
  void feedSegment(Uint8List chunk) => _segment.add(chunk);

  @override
  Future<String> closeSegment() async {
    _tick?.cancel();
    _tick = null;
    try {
      await _inFlight;
    } catch (_) {
      // The final decode below is what counts.
    }
    final w = _worker;
    final pcm = _segment.takeBytes();
    _embedding = null;
    if (w == null || pcm.length < 8000) return '';
    final text = await w.decode(pcm);
    if (speakerModelPath != null && pcm.length >= _minEmbedBytes && text.trim().isNotEmpty) {
      try {
        _embedding = await w.embed(pcm);
      } catch (e) {
        diag('dictation', 'no embedding: $e');
      }
    }
    return text;
  }

  @override
  Future<void> release() async {
    _tick?.cancel();
    _tick = null;
    _worker?.close();
    _worker = null;
  }
}

/// The recogniser in its own isolate: one request at a time, PCM16 in, text
/// out.
class _Worker {
  _Worker._(this._isolate, this._send, this._replies);

  final Isolate _isolate;
  final SendPort _send;
  final Stream<dynamic> _replies;
  int _next = 0;

  static Future<_Worker> spawn(String dir, String model, String? speakerDir) async {
    final inbox = ReceivePort();
    final isolate = await Isolate.spawn(_main, [inbox.sendPort, dir, model, speakerDir]);
    final replies = inbox.asBroadcastStream();
    final first = await replies.first;
    if (first is String) {
      inbox.close();
      isolate.kill();
      throw Exception(first);
    }
    return _Worker._(isolate, first as SendPort, replies);
  }

  Future<String> decode(Uint8List pcm16) async => await _ask('text', pcm16) as String;

  /// The speaker embedding of [pcm16] (#367).
  Future<Float32List> embed(Uint8List pcm16) async => await _ask('embed', pcm16) as Float32List;

  Future<Object?> _ask(String what, Uint8List pcm16) async {
    final id = _next++;
    final reply = _replies.firstWhere((m) => m is List && m[0] == id);
    _send.send([
      id,
      what,
      TransferableTypedData.fromList([pcm16]),
    ]);
    final m = await reply as List;
    if (m.length > 2 && m[2] == true) throw Exception(m[1]);
    return m[1];
  }

  void close() {
    _send.send(null);
    Future<void>.delayed(const Duration(seconds: 2), _isolate.kill);
  }

  static void _main(List<Object?> args) {
    final out = args[0]! as SendPort;
    final dir = args[1]! as String;
    final model = args[2]! as String;
    final speakerDir = args[3] as String?;
    final sherpa.OfflineRecognizer rec;
    sherpa.SpeakerEmbeddingExtractor? voices;
    try {
      sherpa.initBindings();
      rec = sherpa.OfflineRecognizer(
        sherpa.OfflineRecognizerConfig(
          model: model == 'sensevoice'
              ? sherpa.OfflineModelConfig(
                  senseVoice: sherpa.OfflineSenseVoiceModelConfig(
                    model: '$dir/model.int8.onnx',
                    // Empty: the model detects the language.
                    useInverseTextNormalization: true,
                  ),
                  tokens: '$dir/tokens.txt',
                  numThreads: 4,
                  debug: false,
                )
              : sherpa.OfflineModelConfig(
                  transducer: sherpa.OfflineTransducerModelConfig(
                    encoder: '$dir/encoder.int8.onnx',
                    decoder: '$dir/decoder.int8.onnx',
                    joiner: '$dir/joiner.int8.onnx',
                  ),
                  tokens: '$dir/tokens.txt',
                  modelType: 'nemo_transducer',
                  numThreads: 4,
                  debug: false,
                ),
        ),
      );
      if (speakerDir != null) {
        voices = sherpa.SpeakerEmbeddingExtractor(
          config: sherpa.SpeakerEmbeddingExtractorConfig(model: '$speakerDir/model.onnx', numThreads: 2, debug: false),
        );
      }
    } catch (e) {
      out.send('$model: $e');
      return;
    }
    final inbox = ReceivePort();
    out.send(inbox.sendPort);
    inbox.listen((msg) {
      if (msg == null) {
        rec.free();
        voices?.free();
        inbox.close();
        return;
      }
      final m = msg as List;
      final id = m[0] as int;
      final what = m[1] as String;
      try {
        final bytes = (m[2] as TransferableTypedData).materialize().asUint8List();
        final pcm = bytes.buffer.asInt16List(bytes.offsetInBytes, bytes.length ~/ 2);
        final samples = Float32List(pcm.length);
        for (var i = 0; i < pcm.length; i++) {
          samples[i] = pcm[i] / 32768.0;
        }
        if (what == 'embed') {
          final v = voices;
          if (v == null) throw StateError('no speakers\' model loaded');
          final stream = v.createStream();
          stream.acceptWaveform(samples: samples, sampleRate: 16000);
          stream.inputFinished();
          if (!v.isReady(stream)) {
            stream.free();
            throw StateError('segment too short for an embedding');
          }
          final embedding = v.compute(stream);
          stream.free();
          out.send([id, embedding]);
          return;
        }
        final stream = rec.createStream();
        stream.acceptWaveform(samples: samples, sampleRate: 16000);
        rec.decode(stream);
        final text = rec.getResult(stream).text;
        stream.free();
        out.send([id, text]);
      } catch (e) {
        out.send([id, '$e', true]);
      }
    });
  }
}
