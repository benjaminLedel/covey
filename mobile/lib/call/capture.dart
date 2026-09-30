import 'dart:async';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:flutter/services.dart';
import 'package:record/record.dart';

import '../diagnostics.dart';

/// What a call's microphone turned out to be (#507): whether Apple's voice
/// processing — echo cancellation against the agent's own voice — cleans
/// it, which devices it runs on, and at which rates.
class CaptureInfo {
  const CaptureInfo({
    required this.source,
    this.echoCancelled = false,
    this.input = '',
    this.output = '',
    this.inputRate = 0,
    this.outputRate = 0,
    this.note,
  });

  /// `voice processing` for the native stream, `record` for the fallback.
  final String source;

  /// True when voice processing is on: the agent's voice from the
  /// loudspeaker is taken out of the microphone before the call hears it.
  final bool echoCancelled;
  final String input;
  final String output;
  final double inputRate;
  final double outputRate;

  /// Why voice processing is not on, or why the fallback runs.
  final String? note;

  factory CaptureInfo.fromNative(Map<Object?, Object?> m) => CaptureInfo(
    source: 'voice processing',
    echoCancelled: m['vp'] == true,
    input: m['input'] as String? ?? '',
    output: m['output'] as String? ?? '',
    inputRate: (m['inputRate'] as num?)?.toDouble() ?? 0,
    outputRate: (m['outputRate'] as num?)?.toDouble() ?? 0,
    note: m['vpError'] as String?,
  );

  CaptureInfo withNote(String n) => CaptureInfo(
    source: source,
    echoCancelled: echoCancelled,
    input: input,
    output: output,
    inputRate: inputRate,
    outputRate: outputRate,
    note: n,
  );

  /// The diagnostics line of a call's microphone.
  @override
  String toString() {
    String hz(double r) => r > 0 ? '${r.round()} Hz' : '?';
    return 'microphone: $source, echo cancellation ${echoCancelled ? 'yes' : 'no'}'
        '${input.isEmpty ? '' : ', input "$input" ${hz(inputRate)} → 16000 Hz'}'
        '${output.isEmpty ? '' : ', output "$output" ${hz(outputRate)}'}'
        '${note == null ? '' : ' ($note)'}';
  }
}

/// A microphone that has started: its 16 kHz mono samples in −1…1, in
/// chunks of any size, and what it is.
class OpenCapture {
  OpenCapture(this.frames, this.info);
  final Stream<Float32List> frames;
  final CaptureInfo info;
}

/// Where a call's microphone audio comes from (#507), so the call can take
/// the Mac's voice-processed stream and fall back to the `record` package,
/// and a test stands in.
abstract class CaptureSource {
  /// Opens the microphone; throws when it cannot. A
  /// [CaptureDeniedException] means the person has not allowed it, which
  /// no other source changes.
  Future<OpenCapture> start();

  /// Closes the microphone for now (mute); [start] opens it again.
  Future<void> stop();

  /// Frees it after the call.
  Future<void> dispose();
}

class CaptureDeniedException implements Exception {
  const CaptureDeniedException();
  @override
  String toString() => 'the microphone is not allowed';
}

/// Tries [sources] in order, from [from] on, and returns the first that
/// starts with its index; a failure is logged and the next is tried, and
/// the one that runs says why it is not the first. Throws the last failure
/// when none starts, and a denial at once.
Future<(int, OpenCapture)> openCapture(List<CaptureSource> sources, {int from = 0}) async {
  Object? last;
  String? why;
  for (var i = from; i < sources.length; i++) {
    try {
      final open = await sources[i].start();
      return (i, why == null ? open : OpenCapture(open.frames, open.info.withNote('fallback: $why')));
    } on CaptureDeniedException {
      rethrow;
    } catch (e) {
      diag('call', 'microphone source ${i + 1} of ${sources.length} did not start: $e');
      last = e;
      why = '$e';
    }
  }
  throw last ?? StateError('no microphone source');
}

/// Cuts a stream of chunks of any size into windows of exactly [size]
/// samples, keeping the rest for the next chunk: Silero takes 512 at
/// 16 kHz.
class FrameChunker {
  FrameChunker(this.size);
  final int size;
  final _rest = <double>[];

  List<Float32List> add(Float32List chunk) {
    final out = <Float32List>[];
    var at = 0;
    if (_rest.isNotEmpty) {
      final need = size - _rest.length;
      if (chunk.length < need) {
        _rest.addAll(chunk);
        return out;
      }
      out.add(Float32List.fromList([..._rest, ...Float32List.sublistView(chunk, 0, need)]));
      _rest.clear();
      at = need;
    }
    for (; at + size <= chunk.length; at += size) {
      out.add(Float32List.fromList(Float32List.sublistView(chunk, at, at + size)));
    }
    if (at < chunk.length) _rest.addAll(Float32List.sublistView(chunk, at));
    return out;
  }

  void reset() => _rest.clear();
}

/// How long the person has to speak while the agent speaks before it stops
/// (#507). With echo cancellation the agent's own voice no longer reaches
/// the voice detector, so a much shorter confirmation stands — about four
/// windows, enough against a click or a cough — or the tuned one, when that
/// is shorter still. Without it the tuned value holds, as before.
const echoCancelledBargeIn = Duration(milliseconds: 128);

Duration bargeInConfirm(Duration tuned, {required bool echoCancelled}) =>
    echoCancelled && tuned > echoCancelledBargeIn ? echoCancelledBargeIn : tuned;

/// The Mac's microphone through the call's own audio engine, with voice
/// processing on (MainFlutterWindow.swift, `covey/voice`): the frames come
/// on `covey/voice/mic`, events of the engine — a device change — beside
/// them.
class NativeVoiceCapture implements CaptureSource {
  static const _channel = MethodChannel('covey/voice');
  static const _mic = EventChannel('covey/voice/mic');

  StreamSubscription<Object?>? _sub;
  StreamController<Float32List>? _frames;

  @override
  Future<OpenCapture> start() async {
    await stop();
    final frames = _frames = StreamController<Float32List>();
    // Listening first: the frames that come right after the start are not
    // lost.
    _sub = _mic.receiveBroadcastStream().listen((e) {
      if (e is Float32List) {
        frames.add(e);
      } else if (e is Map) {
        final m = Map<Object?, Object?>.from(e);
        if (m['event'] == 'restarted') {
          diag('call', 'audio device changed, ${CaptureInfo.fromNative(m)}');
        } else {
          diag('call', 'microphone: ${m['event']} ${m['error'] ?? ''}');
        }
      }
    }, onError: (Object e) => diag('call', 'microphone stream: $e'));
    try {
      final r = await _channel.invokeMapMethod<Object?, Object?>('micStart');
      return OpenCapture(frames.stream, CaptureInfo.fromNative(r ?? const {}));
    } on PlatformException catch (e) {
      await _close();
      if (e.code == 'denied') throw const CaptureDeniedException();
      rethrow;
    } catch (_) {
      await _close();
      rethrow;
    }
  }

  Future<void> _close() async {
    final sub = _sub, frames = _frames;
    _sub = null;
    _frames = null;
    await sub?.cancel();
    await frames?.close();
  }

  @override
  Future<void> stop() async {
    if (_sub == null) return;
    await _close();
    try {
      await _channel.invokeMethod<void>('micStop');
    } catch (_) {}
  }

  @override
  Future<void> dispose() async {
    await _close();
    try {
      await _channel.invokeMethod<void>('micRelease');
    } catch (_) {}
  }
}

/// The `record` package's microphone, raw as dictation records (#359):
/// its voice processing left the stream silent on macOS. No echo
/// cancellation.
class RecordCapture implements CaptureSource {
  RecordCapture([AudioRecorder? recorder]) : _recorder = recorder ?? AudioRecorder();
  final AudioRecorder _recorder;
  bool _open = false;

  @override
  Future<OpenCapture> start() async {
    if (!await _recorder.hasPermission()) throw const CaptureDeniedException();
    final raw = await _recorder.startStream(
      const RecordConfig(encoder: AudioEncoder.pcm16bits, sampleRate: 16000, numChannels: 1),
    );
    _open = true;
    return OpenCapture(pcm16Floats(raw), const CaptureInfo(source: 'record', inputRate: 16000));
  }

  @override
  Future<void> stop() async {
    if (!_open) return;
    _open = false;
    await _recorder.stop();
  }

  @override
  Future<void> dispose() async {
    await stop();
    await _recorder.dispose();
  }
}

/// PCM16 little-endian chunks as samples in −1…1; an odd byte waits for
/// the next chunk.
Stream<Float32List> pcm16Floats(Stream<Uint8List> raw) async* {
  int? odd;
  await for (final chunk in raw) {
    var bytes = chunk;
    if (odd != null) {
      bytes = Uint8List(chunk.length + 1)
        ..[0] = odd
        ..setRange(1, chunk.length + 1, chunk);
      odd = null;
    }
    if (bytes.length.isOdd) {
      odd = bytes.last;
      bytes = Uint8List.sublistView(bytes, 0, bytes.length - 1);
    }
    final data = ByteData.sublistView(bytes);
    final out = Float32List(bytes.length ~/ 2);
    for (var i = 0; i < out.length; i++) {
      out[i] = data.getInt16(i * 2, Endian.little) / 32768.0;
    }
    yield out;
  }
}

/// Samples in −1…1 as 16-bit little-endian PCM, what the turn segmenter
/// and the recogniser take.
Uint8List floatsPcm16(Float32List s) {
  final out = ByteData(s.length * 2);
  for (var i = 0; i < s.length; i++) {
    out.setInt16(i * 2, (s[i] * 32768).round().clamp(-32768, 32767), Endian.little);
  }
  return out.buffer.asUint8List();
}

/// Perceived loudness, 0–1: -60 dB → 0, 0 dB → 1.
double loudness(Float32List s) {
  if (s.isEmpty) return 0;
  var sum = 0.0;
  for (final v in s) {
    sum += v * v;
  }
  final rms = math.sqrt(sum / s.length);
  if (rms <= 0) return 0;
  final db = 20 * math.log(rms) / math.ln10;
  return ((db + 60) / 60).clamp(0.0, 1.0);
}
