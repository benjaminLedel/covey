import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

import 'api.dart';
import 'diagnostics.dart';
import 'diarize.dart';
import 'dictation.dart';
import 'host.dart';

/// The file is not audio the platform can read: another format, a video
/// without a sound track, a damaged file.
class AudioFileUnsupported implements Exception {
  AudioFileUnsupported(this.detail);

  /// The platform's words, for the message.
  final String detail;

  @override
  String toString() => 'unsupported audio file: $detail';
}

/// Opens an audio file as 16 kHz mono PCM16, the recogniser's input (#634).
abstract class AudioFileDecoder {
  /// Throws [AudioFileUnsupported] for a file the platform cannot decode.
  Future<AudioFileReader> open(String path);
}

/// One open file, read block by block: the caller asks for the next block
/// when it is ready for it, so a file of hours never sits in memory whole.
abstract class AudioFileReader {
  /// The file's length, as its container states it; zero when unknown.
  Duration get duration;

  /// The next block of 16 kHz mono PCM16, little-endian; null at the end.
  Future<Uint8List?> read();

  /// Frees the platform's decoder. Safe to call twice.
  Future<void> close();
}

/// The platform's own decoder (#634): AVFoundation on iOS and macOS
/// (`mobile/darwin/AudioFileDecoder.swift`), MediaExtractor and MediaCodec
/// on Android (`AudioFileDecoder.kt`). No codec is bundled with the app, so
/// what the system cannot read is refused rather than half-decoded.
class PlatformAudioFileDecoder implements AudioFileDecoder {
  const PlatformAudioFileDecoder();

  static const channel = MethodChannel('covey/audio-file');

  /// Where a decoder exists. Windows and Linux have none yet.
  static bool get supported => Host.isIOS || Host.isMacOS || Host.isAndroid;

  /// Samples per block: two seconds, 64 KB across the channel.
  static const frames = 32000;

  @override
  Future<AudioFileReader> open(String path) async {
    try {
      final m = await channel.invokeMapMethod<String, Object?>('open', {'path': path});
      final ms = (m?['durationMs'] as num?)?.toInt() ?? 0;
      return _ChannelReader(m!['id']! as int, Duration(milliseconds: ms));
    } on PlatformException catch (e) {
      if (e.code == 'unsupported') throw AudioFileUnsupported(e.message ?? '');
      rethrow;
    } on MissingPluginException {
      throw AudioFileUnsupported('no audio decoder on this platform');
    }
  }
}

class _ChannelReader implements AudioFileReader {
  _ChannelReader(this._id, this.duration);

  final int _id;
  bool _closed = false;

  @override
  final Duration duration;

  @override
  Future<Uint8List?> read() async {
    if (_closed) return null;
    return PlatformAudioFileDecoder.channel.invokeMethod<Uint8List>('read', {
      'id': _id,
      'frames': PlatformAudioFileDecoder.frames,
    });
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    try {
      await PlatformAudioFileDecoder.channel.invokeMethod<void>('close', {'id': _id});
    } catch (e) {
      diag('dictation', 'closing the audio file failed: $e');
    }
  }
}

/// The recogniser an audio file is fed to: a [Dictation] whose source is
/// the file. A test hands in one that needs no speech model.
typedef AudioFileRecognizer =
    Dictation Function(
      CoveyApi api,
      Stream<Uint8List> Function() source,
      void Function(String text, DateTime at, Float32List? voice) onSegment,
    );

Dictation _sherpa(
  CoveyApi api,
  Stream<Uint8List> Function() source,
  void Function(String text, DateTime at, Float32List? voice) onSegment,
) => Dictation(api: api, source: source, diarize: true, paced: true, onSegment: onSegment);

/// An audio file turned into a meeting's transcript on the device (#634):
/// decoded by the platform, cut at the pauses, recognised and its voices
/// told apart exactly as a meeting recorded from the microphone — the file
/// is simply another source for the same [Dictation]. The source waits for
/// the recogniser ([Dictation.paced]), so memory stays bounded however long
/// the file is; [progress] is how much of it has been fed.
///
/// Nothing of the audio leaves the device or is kept: the decoder reads the
/// file where it lies, and only the text comes out.
class AudioFileTranscription extends ChangeNotifier {
  AudioFileTranscription({
    required this.api,
    required this.path,
    this.decoder = const PlatformAudioFileDecoder(),
    AudioFileRecognizer? recognizer,
  }) : _recognizer = recognizer ?? _sherpa;

  final CoveyApi api;
  final String path;
  final AudioFileDecoder decoder;
  final AudioFileRecognizer _recognizer;

  /// The finished segments, in the order they were spoken: `s1`, `s2` … is
  /// who spoke.
  final segments = <({String key, String text})>[];
  final _voices = Diarizer();

  /// The recogniser while it runs: its model download, its failure.
  Dictation? dictation;

  /// The file's length, once it is open.
  Duration duration = Duration.zero;
  int _fed = 0;

  bool _running = false;
  bool _cancelled = false;

  /// The file is no audio the platform can read.
  AudioFileUnsupported? unsupported;

  /// Recognition could not start (no model, switched off …).
  DictationFailure? failure;

  /// What went wrong in words: the decoder's or the recogniser's.
  String? detail;

  bool get running => _running;
  bool get cancelled => _cancelled;

  /// How far into the file recognition has come.
  Duration get position => Duration(milliseconds: _fed * 1000 ~/ _bytesPerSecond);

  /// The fraction of the file fed to the recogniser, 0–1; null while the
  /// length is unknown.
  double? get progress {
    final total = duration.inMilliseconds * _bytesPerSecond ~/ 1000;
    if (total <= 0) return null;
    return (_fed / total).clamp(0.0, 1.0);
  }

  static const _bytesPerSecond = 32000;

  /// What the segmenter takes at a time: 100 ms, as the microphone delivers
  /// it, so a pause is found as finely as in a meeting.
  static const _chunkBytes = _bytesPerSecond ~/ 10;

  /// Decodes and recognises the whole file. True when it ran to the end;
  /// false when it was cancelled or failed — [unsupported], [failure] and
  /// [detail] say which.
  Future<bool> run() async {
    _running = true;
    notifyListeners();
    final AudioFileReader reader;
    try {
      reader = await decoder.open(path);
    } on AudioFileUnsupported catch (e) {
      unsupported = e;
      detail = e.detail;
      _finish();
      return false;
    } catch (e) {
      detail = '$e';
      _finish();
      return false;
    }
    duration = reader.duration;
    diag('dictation', 'audio file: ${(duration.inMilliseconds / 1000).toStringAsFixed(1)} s');
    final fed = Completer<void>();
    final d = _recognizer(api, () => _feed(reader, fed), (text, at, voice) {
      segments.add((key: 's${_voices.assign(voice)}', text: text));
      notifyListeners();
    });
    dictation = d;
    d.addListener(notifyListeners);
    try {
      if (!await d.start(continuous: true)) {
        failure = d.failure;
        detail = d.detail;
        return false;
      }
      await fed.future;
      await d.stop();
      return !_cancelled && detail == null;
    } catch (e) {
      detail = '$e';
      return false;
    } finally {
      await reader.close();
      d.removeListener(notifyListeners);
      d.dispose();
      dictation = null;
      _finish();
    }
  }

  void _finish() {
    _running = false;
    notifyListeners();
  }

  /// The file as the recogniser's source: a block is read only when the
  /// last one has been taken, and while the recogniser pauses the source
  /// this waits at its `yield`.
  Stream<Uint8List> _feed(AudioFileReader reader, Completer<void> fed) async* {
    try {
      while (!_cancelled) {
        final block = await reader.read();
        if (block == null) break;
        for (var at = 0; at < block.length && !_cancelled; at += _chunkBytes) {
          final end = at + _chunkBytes < block.length ? at + _chunkBytes : block.length;
          final chunk = Uint8List.sublistView(block, at, end);
          _fed += chunk.length;
          // A second of audio at a time is enough for the progress bar; a
          // repaint per chunk is not.
          if (_fed % _bytesPerSecond < chunk.length) notifyListeners();
          yield chunk;
        }
      }
    } catch (e) {
      // Ends the source: what was recognised so far is not lost, and the
      // message says the file broke off.
      detail = '$e';
      diag('dictation', 'audio file read failed: $e');
    } finally {
      notifyListeners();
      if (!fed.isCompleted) fed.complete();
    }
  }

  /// Stops at the next block; [run] then returns false, and what was
  /// recognised so far is dropped.
  void cancel() {
    if (_cancelled) return;
    _cancelled = true;
    notifyListeners();
  }
}

/// Removes the copy a file picker made of [path] (#634): on iOS and Android
/// the picked file arrives as a copy in the app's temporary or cache
/// directory, and the audio is not to stay on the device after its
/// transcript is written. The original — on the Mac the picked file itself —
/// is never touched: only a path inside those directories is deleted.
Future<void> discardPickedCopy(String path) async {
  if (!(Host.isIOS || Host.isAndroid)) return;
  final roots = <String>{Directory.systemTemp.path};
  try {
    roots.add((await getTemporaryDirectory()).path);
  } catch (_) {
    // Without path_provider the system's temporary directory still counts.
  }
  final file = File(path);
  final canonical = file.absolute.path;
  if (!roots.any((r) => r.isNotEmpty && canonical.startsWith('$r/'))) return;
  try {
    if (await file.exists()) await file.delete();
    // The picker gives each copy a directory of its own; it goes too once
    // it is empty.
    final dir = file.parent;
    if (!roots.contains(dir.path) && await dir.exists() && await dir.list().isEmpty) await dir.delete();
    // iOS's document picker leaves its own copy in "<bundle>-Inbox" before
    // the plugin copies it again.
    if (Host.isIOS) {
      final name = file.uri.pathSegments.last;
      await for (final e in Directory.systemTemp.list()) {
        if (e is Directory && e.path.endsWith('-Inbox')) {
          final twin = File('${e.path}/$name');
          if (await twin.exists()) await twin.delete();
        }
      }
    }
  } catch (e) {
    diag('dictation', 'the picked copy was not removed: $e');
  }
}
