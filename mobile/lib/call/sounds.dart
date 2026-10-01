import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import '../diagnostics.dart';
import 'provider.dart';
import 'wav.dart';

/// The short sounds of a call (#500), bundled with the app and made by
/// tool/call_sounds.dart.
enum Earcon {
  /// While the call connects; loops, at most twice.
  ringing('call_ringing'),

  /// The call is open.
  connected('call_connected'),

  /// The end of a turn was heard.
  heard('call_heard'),

  /// The agent took the turn on as a task.
  task('call_task'),
  mute('call_mute'),
  unmute('call_unmute'),
  hangUp('call_hangup'),

  /// Somebody typing, while the agent thinks (#526): loops, and plays on
  /// the filler player rather than this one ([CallSounds.take]).
  typing('call_typing');

  const Earcon(this.file);
  final String file;

  String get asset => 'assets/sounds/$file.wav';
}

/// Where a call's sounds play: a player of their own beside the voice's,
/// mixed under it, so a sound never interrupts the agent.
abstract class EarconOutput {
  /// Plays [pcm] at [volume] (0–1), [loops] times in a row; replaces a
  /// sound still playing.
  Future<void> playEarcon(Pcm pcm, {required double volume, int loops = 1});

  /// Stops the sound playing, if one does.
  Future<void> stopEarcon();
}

/// The Mac's side of [EarconOutput]: the `covey/voice` channel. It needs no
/// handler of its own, so the settings can preview a sound outside a call.
class ChannelEarconOutput implements EarconOutput {
  const ChannelEarconOutput();

  static const _channel = MethodChannel('covey/voice');

  @override
  Future<void> playEarcon(Pcm pcm, {required double volume, int loops = 1}) => _channel.invokeMethod<void>('earcon', {
    'samples': pcm.samples,
    'sampleRate': pcm.sampleRate,
    'volume': volume,
    'loops': loops,
  });

  @override
  Future<void> stopEarcon() => _channel.invokeMethod<void>('earconStop');
}

/// What a call plays, and whether: the setting is read at each sound, so
/// switching it off in the middle of a call takes effect at once.
class CallSounds {
  CallSounds({required this.output, required this.enabled, required this.volume, Future<Pcm> Function(Earcon e)? load})
    : _load = load ?? _fromBundle;

  final EarconOutput output;
  final bool Function() enabled;
  final double Function() volume;
  final Future<Pcm> Function(Earcon e) _load;
  final _cache = <Earcon, Pcm>{};

  static Future<Pcm> _fromBundle(Earcon e) async {
    final data = await rootBundle.load(e.asset);
    return decodeWav(data.buffer.asUint8List(data.offsetInBytes, data.lengthInBytes));
  }

  /// Loads every sound, so the first of each plays without a pause.
  Future<void> preload() async {
    for (final e in Earcon.values) {
      try {
        _cache[e] ??= await _load(e);
      } catch (err) {
        diag('call', 'sound ${e.file} not loaded: $err');
      }
    }
  }

  /// Plays [e], unless sounds are off, and answers how long it plays —
  /// zero when it does not. Never throws: a sound that cannot play is not
  /// worth a failed call.
  Future<Duration> play(Earcon e, {int loops = 1}) async {
    if (!enabled()) return Duration.zero;
    final v = volume();
    if (v <= 0) return Duration.zero;
    try {
      final pcm = _cache[e] ??= await _load(e);
      await output.playEarcon(pcm, volume: v, loops: loops);
      return pcm.duration * loops;
    } catch (err) {
      debugPrint('call sound ${e.file}: $err');
      return Duration.zero;
    }
  }

  /// [e]'s audio and the volume it plays at, for a player of its own — the
  /// typing on the filler player (#526). Null when sounds are off, or it
  /// cannot be loaded.
  Future<(Pcm, double)?> take(Earcon e) async {
    if (!enabled()) return null;
    final v = volume();
    if (v <= 0) return null;
    try {
      return (_cache[e] ??= await _load(e), v);
    } catch (err) {
      debugPrint('call sound ${e.file}: $err');
      return null;
    }
  }

  Future<void> stop() async {
    try {
      await output.stopEarcon();
    } catch (_) {}
  }
}
