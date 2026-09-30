import 'dart:math' as math;
import 'dart:typed_data';

import 'spoken_voice.dart';

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

/// How loud [pcm] is, frame by frame at [fps], in dB of full scale; −100
/// for a silent frame (#511).
Float32List levelsDb(Pcm pcm, {int fps = 60}) {
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
    out[i] = rms <= 1e-5 ? -100 : 20 * math.log(rms) / math.ln10;
  }
  return out;
}

/// Opens a stream of speech at the instance: its content type and its
/// bytes as they arrive ([CoveyApi.synthesizeSpeechStream]).
typedef SpeechStreamOpen =
    Future<(String, Stream<List<int>>)> Function(
      String text, {
      String voice,
      double speed,
      String language,
      String instructions,
    });

/// Turns the chunks of a compressed stream into samples as they arrive: the
/// Mac decodes MP3 in the voice channel ([VoiceOutput]).
abstract class StreamDecoding {
  Future<bool> openDecoder(int id);
  Future<Pcm> decode(int id, Uint8List bytes);
  Future<void> closeDecoder(int id);
}

/// The organisation's voice provider speaking [voice] (#497): the one
/// source of covey's voices. The instance calls the provider — an
/// OpenAI-compatible `/v1/audio/speech`, educa AI by default — with the
/// organisation's key and passes its MP3 through as it comes, so the first
/// sentence plays while the rest is still being synthesised. The key never
/// reaches the app.
class ProviderVoice {
  ProviderVoice(this.open, this.decoder, {this.voice = const SpokenVoice()});

  final SpeechStreamOpen open;
  final StreamDecoding decoder;
  final SpokenVoice voice;

  static int _streams = 0;

  /// [text] as samples, piece by piece as the provider's stream arrives.
  /// [language] is what the reply is written in.
  Stream<Pcm> stream(String text, {String language = ''}) async* {
    final (type, bytes) = await open(
      text,
      voice: voice.voice,
      speed: voice.speed,
      language: language,
      instructions: voice.instructions,
    );
    final kind = type.split(';').first.trim().toLowerCase();
    if (kind != 'audio/mpeg' && kind != 'audio/mp3') {
      throw FormatException('the voice provider answered with $type');
    }
    final id = ++_streams;
    if (!await decoder.openDecoder(id)) throw const FormatException('this Mac cannot decode MP3');
    try {
      await for (final chunk in bytes) {
        final pcm = await decoder.decode(id, chunk is Uint8List ? chunk : Uint8List.fromList(chunk));
        if (pcm.samples.isNotEmpty) yield pcm;
      }
    } finally {
      await decoder.closeDecoder(id);
    }
  }
}
