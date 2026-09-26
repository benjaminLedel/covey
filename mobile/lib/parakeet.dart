import 'dart:async';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:sherpa_onnx/sherpa_onnx.dart' as sherpa;

import 'diagnostics.dart';
import 'dictation.dart';

/// NVIDIA Parakeet TDT 0.6B v3 through sherpa-onnx (#353): 25 European
/// languages, detected by the model itself, with punctuation and capitals.
///
/// Parakeet is not a streaming model: it decodes a segment as a whole. That
/// fits the pause segmentation — each segment is decoded once it ends, and
/// about once a second while it grows, for the preview; a transducer decodes
/// a segment of a few seconds in a fraction of that on a phone. The
/// recogniser lives in a worker isolate, so decoding never blocks the UI,
/// and is loaded once per dictation and freed after it: the int8 encoder
/// alone is 650 MB, which a phone should not hold while nobody dictates.
///
/// The context of the previous segments is not used — Parakeet takes no
/// prompt — and neither is the language: it recognises it.
class ParakeetEngine extends SegmentedEngine {
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
  String get kind => 'parakeet';

  @override
  Future<void> prepare() async {
    if (_worker != null) return;
    final watch = Stopwatch()..start();
    _worker = await _Worker.spawn(modelPath);
    diag('dictation', 'parakeet loaded in ${watch.elapsedMilliseconds} ms');
  }

  @override
  Future<void> openSegment(String? context) async {
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
          diag('dictation', 'parakeet preview failed: $e');
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
    if (w == null || pcm.length < 8000) return '';
    return w.decode(pcm);
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

  static Future<_Worker> spawn(String dir) async {
    final inbox = ReceivePort();
    final isolate = await Isolate.spawn(_main, [inbox.sendPort, dir]);
    final replies = inbox.asBroadcastStream();
    final first = await replies.first;
    if (first is String) {
      inbox.close();
      isolate.kill();
      throw Exception(first);
    }
    return _Worker._(isolate, first as SendPort, replies);
  }

  Future<String> decode(Uint8List pcm16) async {
    final id = _next++;
    final reply = _replies.firstWhere((m) => m is List && m[0] == id);
    _send.send([
      id,
      TransferableTypedData.fromList([pcm16]),
    ]);
    final m = await reply as List;
    if (m[1] is String && m.length > 2 && m[2] == true) throw Exception(m[1]);
    return m[1] as String;
  }

  void close() {
    _send.send(null);
    Future<void>.delayed(const Duration(seconds: 2), _isolate.kill);
  }

  static void _main(List<Object?> args) {
    final out = args[0]! as SendPort;
    final dir = args[1]! as String;
    final sherpa.OfflineRecognizer rec;
    try {
      sherpa.initBindings();
      rec = sherpa.OfflineRecognizer(
        sherpa.OfflineRecognizerConfig(
          model: sherpa.OfflineModelConfig(
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
    } catch (e) {
      out.send('parakeet: $e');
      return;
    }
    final inbox = ReceivePort();
    out.send(inbox.sendPort);
    inbox.listen((msg) {
      if (msg == null) {
        rec.free();
        inbox.close();
        return;
      }
      final m = msg as List;
      final id = m[0] as int;
      try {
        final bytes = (m[1] as TransferableTypedData).materialize().asUint8List();
        final pcm = bytes.buffer.asInt16List(bytes.offsetInBytes, bytes.length ~/ 2);
        final samples = Float32List(pcm.length);
        for (var i = 0; i < pcm.length; i++) {
          samples[i] = pcm[i] / 32768.0;
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
