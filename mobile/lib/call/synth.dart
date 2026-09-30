import 'dart:async';
import 'dart:io';
import 'dart:isolate';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:sherpa_onnx/sherpa_onnx.dart' as sherpa;

/// Speech synthesis on the device (#497), apart from any one engine: text
/// and a voice in, samples out. The first implementation is sherpa-onnx's
/// offline TTS ([SherpaSynthesiser]); which models it runs is the covey
/// instance's catalogue, so a better voice is a catalogue entry, not a new
/// path through the app.
abstract class Synthesiser {
  /// [text] spoken by speaker [speaker] of the loaded model, at [rate]
  /// (1 is the model's own pace).
  Future<Pcm> synthesise(String text, {int speaker = 0, double rate = 1});

  /// Frees the model.
  void close();
}

/// Mono samples in −1…1 at [sampleRate].
class Pcm {
  const Pcm(this.samples, this.sampleRate);
  final Float32List samples;
  final int sampleRate;

  Duration get duration =>
      sampleRate == 0 ? Duration.zero : Duration(microseconds: samples.length * 1000000 ~/ sampleRate);
}

/// How loud [pcm] is, frame by frame at [fps]: 0–1 per frame, the face's
/// mouth while the voice speaks. Perceived loudness, as the microphone's
/// level is: −50 dB is closed, −10 dB fully open.
Float32List levels(Pcm pcm, {int fps = 60}) {
  final per = math.max(1, pcm.sampleRate ~/ fps);
  final n = (pcm.samples.length + per - 1) ~/ per;
  final out = Float32List(n);
  for (var i = 0; i < n; i++) {
    final end = math.min(pcm.samples.length, (i + 1) * per);
    var sum = 0.0;
    for (var j = i * per; j < end; j++) {
      final v = pcm.samples[j];
      sum += v * v;
    }
    final rms = math.sqrt(sum / math.max(1, end - i * per));
    if (rms <= 0) continue;
    final db = 20 * math.log(rms) / math.ln10;
    out[i] = ((db + 50) / 40).clamp(0.0, 1.0);
  }
  return out;
}

/// Where the files of an unpacked model lie, as sherpa-onnx wants them.
class TtsFiles {
  const TtsFiles({required this.model, required this.tokens, this.dataDir = ''});
  final String model;
  final String tokens;

  /// espeak-ng's data, for the models that phonemise with it (Piper).
  final String dataDir;

  /// The files of a `vits` model unpacked into [dir]: sherpa-onnx's
  /// archives hold one directory with the `.onnx`, `tokens.txt` and
  /// `espeak-ng-data`. Null when they are not there.
  static TtsFiles? find(String dir) {
    final root = Directory(dir);
    if (!root.existsSync()) return null;
    for (final d in [root, ...root.listSync().whereType<Directory>()]) {
      final onnx = d.listSync().whereType<File>().where((f) => f.path.endsWith('.onnx')).toList();
      final tokens = File('${d.path}/tokens.txt');
      if (onnx.length != 1 || !tokens.existsSync()) continue;
      final data = Directory('${d.path}/espeak-ng-data');
      return TtsFiles(model: onnx.single.path, tokens: tokens.path, dataDir: data.existsSync() ? data.path : '');
    }
    return null;
  }
}

/// sherpa-onnx's offline TTS in a worker isolate: loading takes a moment
/// and synthesis a fraction of the speech's length, neither of which may
/// hold the UI.
class SherpaSynthesiser implements Synthesiser {
  SherpaSynthesiser._(this._isolate, this._send, this._replies, this.speakers);

  final Isolate _isolate;
  final SendPort _send;
  final Stream<dynamic> _replies;
  int _next = 0;

  /// How many speakers the model holds.
  final int speakers;

  /// Loads a model of [family] (`vits`) unpacked in [dir].
  static Future<SherpaSynthesiser> load(String dir, String family) async {
    if (family != 'vits') throw UnsupportedError('voices of the family "$family" are not known to this app');
    final files = TtsFiles.find(dir);
    if (files == null) throw StateError('no voice model in $dir');
    final inbox = ReceivePort();
    final isolate = await Isolate.spawn(_main, [inbox.sendPort, files.model, files.tokens, files.dataDir]);
    final replies = inbox.asBroadcastStream();
    final first = await replies.first;
    if (first is String) {
      inbox.close();
      isolate.kill();
      throw Exception(first);
    }
    final ready = first as List;
    return SherpaSynthesiser._(isolate, ready[0] as SendPort, replies, ready[1] as int);
  }

  @override
  Future<Pcm> synthesise(String text, {int speaker = 0, double rate = 1}) async {
    final id = _next++;
    final reply = _replies.firstWhere((m) => m is List && m[0] == id);
    _send.send([id, text, speaker, rate]);
    final m = await reply as List;
    if (m.length > 3 && m[3] == true) throw Exception(m[1]);
    final samples = (m[1] as TransferableTypedData).materialize().asFloat32List();
    return Pcm(samples, m[2] as int);
  }

  @override
  void close() {
    _send.send(null);
    Future<void>.delayed(const Duration(seconds: 2), _isolate.kill);
  }

  static void _main(List<Object?> args) {
    final out = args[0]! as SendPort;
    final sherpa.OfflineTts tts;
    try {
      sherpa.initBindings();
      tts = sherpa.OfflineTts(
        sherpa.OfflineTtsConfig(
          model: sherpa.OfflineTtsModelConfig(
            vits: sherpa.OfflineTtsVitsModelConfig(
              model: args[1]! as String,
              tokens: args[2]! as String,
              dataDir: args[3]! as String,
            ),
            numThreads: math.min(4, math.max(1, Platform.numberOfProcessors ~/ 2)),
            debug: false,
          ),
        ),
      );
    } catch (e) {
      out.send('$e');
      return;
    }
    final inbox = ReceivePort();
    out.send([inbox.sendPort, tts.numSpeakers]);
    inbox.listen((msg) {
      if (msg == null) {
        tts.free();
        inbox.close();
        return;
      }
      final m = msg as List;
      final id = m[0] as int;
      try {
        final rate = (m[3] as num).toDouble();
        final audio = tts.generate(text: m[1] as String, sid: m[2] as int, speed: rate <= 0 ? 1 : rate);
        out.send([
          id,
          TransferableTypedData.fromList([audio.samples]),
          audio.sampleRate,
        ]);
      } catch (e) {
        out.send([id, '$e', 0, true]);
      }
    });
  }
}

/// The samples of a WAV file (#497): PCM of 16 or 32 bits, or 32-bit
/// float, any number of channels (the first is kept). Null for what is not
/// such a file.
Pcm? decodeWav(Uint8List bytes) {
  if (bytes.length < 12) return null;
  final d = ByteData.sublistView(bytes);
  String tag(int at) => String.fromCharCodes(bytes.sublist(at, at + 4));
  if (tag(0) != 'RIFF' || tag(8) != 'WAVE') return null;
  int? format, channels, rate, bits;
  var at = 12;
  while (at + 8 <= bytes.length) {
    final id = tag(at);
    var size = d.getUint32(at + 4, Endian.little);
    final body = at + 8;
    // A streamed WAV names its data as endless: the rest of the file.
    if (id == 'data' && (size == 0 || size == 0xFFFFFFFF || body + size > bytes.length)) size = bytes.length - body;
    if (id == 'fmt ' && size >= 16) {
      format = d.getUint16(body, Endian.little);
      channels = d.getUint16(body + 2, Endian.little);
      rate = d.getUint32(body + 4, Endian.little);
      bits = d.getUint16(body + 14, Endian.little);
      // WAVE_FORMAT_EXTENSIBLE carries the real format in its sub-format.
      if (format == 0xFFFE && size >= 26) format = d.getUint16(body + 24, Endian.little);
    } else if (id == 'data') {
      if (format == null || channels == null || rate == null || bits == null || channels < 1) return null;
      final step = channels * bits ~/ 8;
      if (step == 0) return null;
      final n = size ~/ step;
      final out = Float32List(n);
      for (var i = 0; i < n; i++) {
        final p = body + i * step;
        out[i] = switch ((format, bits)) {
          (1, 16) => d.getInt16(p, Endian.little) / 32768.0,
          (1, 32) => d.getInt32(p, Endian.little) / 2147483648.0,
          (3, 32) => d.getFloat32(p, Endian.little),
          _ => double.nan,
        };
        if (out[i].isNaN) return null;
      }
      return Pcm(out, rate);
    }
    at = body + size + (size.isOdd ? 1 : 0);
  }
  return null;
}

/// Speech synthesis by the organisation's own speech server (#497): the
/// instance calls it with the organisation's key — an OpenAI-compatible
/// `/v1/audio/speech` — and hands back a WAV file. No per-token cloud
/// service: the server is the organisation's.
class ServerSynthesiser implements Synthesiser {
  ServerSynthesiser(this.fetch, {this.model = '', this.voice = ''});

  /// The WAV of a text, as the instance answers `POST /speech/synthesize`.
  final Future<Uint8List> Function(String text, {String model, String voice, double rate}) fetch;
  final String model;
  final String voice;

  @override
  Future<Pcm> synthesise(String text, {int speaker = 0, double rate = 1}) async {
    final wav = await fetch(text, model: model, voice: voice, rate: rate == 1 ? 0 : rate);
    final pcm = decodeWav(wav);
    if (pcm == null) throw const FormatException('the speech server did not answer with a WAV file');
    return pcm;
  }

  @override
  void close() {}
}
