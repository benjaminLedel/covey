import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:path_provider/path_provider.dart';

import '../diagnostics.dart';

/// The call's diagnostics recording (#498): switched on in the call
/// settings, off by default, it keeps every turn's audio as a WAV file on
/// this Mac beside one JSON line per turn — the durations, why the turn was
/// cut, what the voice detector saw, the thresholds in effect, and the text
/// as recognised and as cleaned up. It is there to tune the pause and
/// barge-in thresholds from real calls and to see what the recogniser made
/// of what was said. Nothing of it is uploaded; it lies in the app's
/// support directory under `logs/call-audio/` and is deleted after
/// [keepFor], or at once from the settings.
class CallRecording {
  CallRecording._(this.dir);

  final Directory dir;

  static const keepFor = Duration(days: 7);

  /// The directory, created; old recordings are removed on the way.
  static Future<CallRecording> open({Directory? root}) async {
    final base = root ?? Directory('${(await getApplicationSupportDirectory()).path}/logs/call-audio');
    await base.create(recursive: true);
    final r = CallRecording._(base);
    await r.prune();
    return r;
  }

  static Future<Directory> directory() async =>
      Directory('${(await getApplicationSupportDirectory()).path}/logs/call-audio');

  File get _log => File('${dir.path}/turns.jsonl');

  /// Keeps one turn: [pcm16] as `<name>.wav` and [facts] as a line of
  /// `turns.jsonl`, with the file's name.
  Future<void> keep(String name, Uint8List pcm16, Map<String, Object?> facts) async {
    try {
      final wav = File('${dir.path}/$name.wav');
      await wav.writeAsBytes(wavBytes(pcm16), flush: true);
      await _log.writeAsString(
        '${jsonEncode({'at': DateTime.now().toIso8601String(), 'file': '$name.wav', ...facts})}\n',
        mode: FileMode.append,
        flush: true,
      );
    } on FileSystemException catch (e) {
      diag('call', 'recording a turn failed: ${e.message}');
    }
  }

  /// Removes recordings older than [keepFor], and the lines that name them.
  Future<void> prune({DateTime? now}) async {
    final cut = (now ?? DateTime.now()).subtract(keepFor);
    final gone = <String>{};
    await for (final e in dir.list()) {
      if (e is! File || !e.path.endsWith('.wav')) continue;
      if ((await e.lastModified()).isBefore(cut)) {
        gone.add(e.uri.pathSegments.last);
        await e.delete();
      }
    }
    if (!await _log.exists()) return;
    final lines = await _log.readAsLines();
    final keep = lines.where((l) {
      try {
        final j = jsonDecode(l) as Map<String, dynamic>;
        final at = DateTime.tryParse(j['at'] as String? ?? '');
        return !gone.contains(j['file']) && (at == null || !at.isBefore(cut));
      } catch (_) {
        return false;
      }
    }).toList();
    if (keep.length == lines.length) return;
    if (keep.isEmpty) {
      await _log.delete();
    } else {
      await _log.writeAsString('${keep.join('\n')}\n', flush: true);
    }
  }

  /// Everything recorded, gone.
  static Future<void> deleteAll({Directory? root}) async {
    final d = root ?? await directory();
    if (await d.exists()) await d.delete(recursive: true);
  }

  /// How much is recorded: bytes and turns.
  static Future<(int, int)> size({Directory? root}) async {
    final d = root ?? await directory();
    if (!await d.exists()) return (0, 0);
    var bytes = 0, turns = 0;
    await for (final e in d.list()) {
      if (e is! File) continue;
      bytes += await e.length();
      if (e.path.endsWith('.wav')) turns++;
    }
    return (bytes, turns);
  }
}

/// A WAV file of 16 kHz mono PCM16.
Uint8List wavBytes(Uint8List pcm16, {int sampleRate = 16000}) {
  final h = ByteData(44);
  void ascii(int at, String s) {
    for (var i = 0; i < s.length; i++) {
      h.setUint8(at + i, s.codeUnitAt(i));
    }
  }

  ascii(0, 'RIFF');
  h.setUint32(4, 36 + pcm16.length, Endian.little);
  ascii(8, 'WAVE');
  ascii(12, 'fmt ');
  h.setUint32(16, 16, Endian.little);
  h.setUint16(20, 1, Endian.little); // PCM
  h.setUint16(22, 1, Endian.little); // mono
  h.setUint32(24, sampleRate, Endian.little);
  h.setUint32(28, sampleRate * 2, Endian.little);
  h.setUint16(32, 2, Endian.little);
  h.setUint16(34, 16, Endian.little);
  ascii(36, 'data');
  h.setUint32(40, pcm16.length, Endian.little);
  return (BytesBuilder(copy: false)
        ..add(h.buffer.asUint8List())
        ..add(pcm16))
      .takeBytes();
}
