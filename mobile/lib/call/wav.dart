import 'dart:typed_data';

import 'provider.dart';

/// 16-bit PCM in a WAV file, mono (#500): the call's sounds as bundled, and
/// the fillers as they are cached on this Mac.
Uint8List encodeWav(Pcm pcm) {
  final data = pcm.samples.length * 2;
  final b = ByteData(44 + data);
  void tag(int at, String s) {
    for (var i = 0; i < 4; i++) {
      b.setUint8(at + i, s.codeUnitAt(i));
    }
  }

  tag(0, 'RIFF');
  b.setUint32(4, 36 + data, Endian.little);
  tag(8, 'WAVE');
  tag(12, 'fmt ');
  b.setUint32(16, 16, Endian.little);
  b.setUint16(20, 1, Endian.little);
  b.setUint16(22, 1, Endian.little);
  b.setUint32(24, pcm.sampleRate, Endian.little);
  b.setUint32(28, pcm.sampleRate * 2, Endian.little);
  b.setUint16(32, 2, Endian.little);
  b.setUint16(34, 16, Endian.little);
  tag(36, 'data');
  b.setUint32(40, data, Endian.little);
  for (var i = 0; i < pcm.samples.length; i++) {
    b.setInt16(44 + i * 2, (pcm.samples[i].clamp(-1.0, 1.0) * 32767).round(), Endian.little);
  }
  return b.buffer.asUint8List();
}

/// Little-endian 16-bit mono PCM as it is, in a WAV file (#516): a call's
/// turn as the voice detector cut it, for the voice provider's recognition.
Uint8List wavOfPcm16(Uint8List pcm16, {int sampleRate = 16000}) {
  final data = pcm16.length - pcm16.length % 2;
  final out = Uint8List(44 + data);
  final b = ByteData.sublistView(out);
  void tag(int at, String s) {
    for (var i = 0; i < 4; i++) {
      b.setUint8(at + i, s.codeUnitAt(i));
    }
  }

  tag(0, 'RIFF');
  b.setUint32(4, 36 + data, Endian.little);
  tag(8, 'WAVE');
  tag(12, 'fmt ');
  b.setUint32(16, 16, Endian.little);
  b.setUint16(20, 1, Endian.little);
  b.setUint16(22, 1, Endian.little);
  b.setUint32(24, sampleRate, Endian.little);
  b.setUint32(28, sampleRate * 2, Endian.little);
  b.setUint16(32, 2, Endian.little);
  b.setUint16(34, 16, Endian.little);
  tag(36, 'data');
  b.setUint32(40, data, Endian.little);
  out.setRange(44, 44 + data, pcm16);
  return out;
}

/// The samples of a 16-bit PCM WAV; the first channel of more than one.
/// Throws a [FormatException] on anything else.
Pcm decodeWav(Uint8List bytes) {
  final b = ByteData.sublistView(bytes);
  String tag(int at) => String.fromCharCodes(bytes.sublist(at, at + 4));
  if (bytes.length < 12 || tag(0) != 'RIFF' || tag(8) != 'WAVE') throw const FormatException('not a WAV file');
  var at = 12;
  int? rate, channels, bits;
  while (at + 8 <= bytes.length) {
    final id = tag(at);
    final size = b.getUint32(at + 4, Endian.little);
    final body = at + 8;
    if (id == 'fmt ') {
      if (b.getUint16(body, Endian.little) != 1) throw const FormatException('not PCM');
      channels = b.getUint16(body + 2, Endian.little);
      rate = b.getUint32(body + 4, Endian.little);
      bits = b.getUint16(body + 14, Endian.little);
    } else if (id == 'data') {
      if (rate == null || channels == null || channels == 0 || bits != 16) {
        throw const FormatException('not 16-bit PCM');
      }
      final end = (body + size).clamp(body, bytes.length);
      final frames = (end - body) ~/ (2 * channels);
      final out = Float32List(frames);
      for (var i = 0; i < frames; i++) {
        out[i] = b.getInt16(body + i * 2 * channels, Endian.little) / 32768;
      }
      return Pcm(out, rate);
    }
    at = body + size + (size.isOdd ? 1 : 0);
  }
  throw const FormatException('no audio in the WAV file');
}
