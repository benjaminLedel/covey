// The sounds of a call (#500), synthesised here from sine partials — and
// the typing (#526) from filtered noise: no samples, so nothing to license,
// and anybody can change a sound and build it again.
//
//   cd mobile && dart run tool/call_sounds.dart
//
// Writes assets/sounds/call_<name>.wav: 16-bit PCM, 44.1 kHz, mono. They are
// meant to sit under the agent's voice and to be heard many times a call —
// soft attacks, few overtones, quiet. The app plays them at its own volume
// setting on top of the level written here.
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

const rate = 44100;

/// One note: a few partials (ratio to [hz], relative level), a short
/// attack and an exponential decay; [glideTo] bends the pitch over the note.
class Note {
  const Note(
    this.at,
    this.hz, {
    this.length = 0.4,
    this.attack = 0.008,
    this.decay = 0.18,
    this.level = 1,
    this.partials = const [(1.0, 1.0)],
    this.glideTo,
  });

  final double at;
  final double hz;
  final double length;
  final double attack;

  /// The time over which the note falls by about 27 dB (e^-3).
  final double decay;
  final double level;
  final List<(double, double)> partials;
  final double? glideTo;
}

/// A soft bell: the fundamental, a quiet octave and a quieter inharmonic
/// partial that makes it ring rather than beep.
const bell = [(1.0, 1.0), (2.0, 0.22), (2.76, 0.07)];

/// A rounder sound for blips: the fundamental and a whisper of the octave.
const round = [(1.0, 1.0), (2.0, 0.08)];

Float64List render(List<Note> notes, {required double length, required double peak}) {
  final out = Float64List((length * rate).round());
  for (final n in notes) {
    final start = (n.at * rate).round();
    final count = math.min((n.length * rate).round(), out.length - start);
    for (final (ratio, amp) in n.partials) {
      var phase = 0.0;
      for (var i = 0; i < count; i++) {
        final t = i / rate;
        final f0 = n.glideTo == null ? n.hz : n.hz * math.pow(n.glideTo! / n.hz, math.min(1.0, t / n.length));
        phase += 2 * math.pi * f0 * ratio / rate;
        // Higher partials fade sooner, as they do on a struck bar.
        final env = math.min(1.0, t / n.attack) * math.exp(-3 * t / (n.decay / ratio.clamp(1, 3)));
        out[start + i] += math.sin(phase) * amp * env * n.level;
      }
    }
  }
  // A short fade at the end, so nothing clicks where the file stops.
  final fade = math.min(out.length, (0.02 * rate).round());
  for (var i = 0; i < fade; i++) {
    out[out.length - 1 - i] *= i / fade;
  }
  var max = 0.0;
  for (final v in out) {
    max = math.max(max, v.abs());
  }
  if (max > 0) {
    for (var i = 0; i < out.length; i++) {
      out[i] = out[i] / max * peak;
    }
  }
  return out;
}

Uint8List wav(Float64List samples) {
  final data = samples.length * 2;
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
  b.setUint16(20, 1, Endian.little); // PCM
  b.setUint16(22, 1, Endian.little); // mono
  b.setUint32(24, rate, Endian.little);
  b.setUint32(28, rate * 2, Endian.little);
  b.setUint16(32, 2, Endian.little);
  b.setUint16(34, 16, Endian.little);
  tag(36, 'data');
  b.setUint32(40, data, Endian.little);
  for (var i = 0; i < samples.length; i++) {
    b.setInt16(44 + i * 2, (samples[i].clamp(-1.0, 1.0) * 32767).round(), Endian.little);
  }
  return b.buffer.asUint8List();
}

// Pitches, in Hz.
const g4 = 392.0, b4 = 493.88, e5 = 659.25, g5 = 783.99, a5 = 880.0, c6 = 1046.5, e6 = 1318.5;

final sounds = <String, Float64List>{
  // Two soft bell notes and room to breathe: the loop's period is the file.
  'ringing': render(
    const [
      Note(0, g5, length: 0.5, decay: 0.3, partials: bell),
      Note(0.16, e5, length: 0.6, decay: 0.34, level: 0.85, partials: bell),
    ],
    length: 1.3,
    peak: 0.4,
  ),
  // A low "dong": the line is open.
  'connected': render(
    const [Note(0, g4, length: 0.75, attack: 0.006, decay: 0.42, partials: bell)],
    length: 0.8,
    peak: 0.5,
  ),
  // Barely there: the end of the turn was heard. Low and with a slow
  // attack (#526), a tap rather than a beep.
  'heard': render(
    const [Note(0, g5, length: 0.12, attack: 0.014, decay: 0.06, partials: round)],
    length: 0.13,
    peak: 0.1,
  ),
  // A rising third that settles: something was taken on.
  'task': render(
    const [
      Note(0, c6, length: 0.22, attack: 0.005, decay: 0.12, partials: round),
      Note(0.1, e6, length: 0.34, attack: 0.005, decay: 0.2, level: 0.9, partials: round),
    ],
    length: 0.46,
    peak: 0.35,
  ),
  // Falling for off, rising for on.
  'mute': render(
    const [Note(0, a5, length: 0.12, attack: 0.004, decay: 0.08, partials: round, glideTo: e5)],
    length: 0.13,
    peak: 0.3,
  ),
  'unmute': render(
    const [Note(0, e5, length: 0.12, attack: 0.004, decay: 0.08, partials: round, glideTo: a5)],
    length: 0.13,
    peak: 0.3,
  ),
  // Two notes down to rest: the call is over.
  'hangup': render(
    const [
      Note(0, e5, length: 0.3, decay: 0.16, partials: bell),
      Note(0.13, b4, length: 0.4, decay: 0.24, level: 0.9, partials: bell),
    ],
    length: 0.55,
    peak: 0.4,
  ),
  // While the agent thinks (#526): looped on the filler player.
  'typing': typing(),
};

/// A biquad band-pass (RBJ), for the keystrokes' body and click.
class BandPass {
  BandPass(double hz, double q) {
    final w = 2 * math.pi * hz / rate, alpha = math.sin(w) / (2 * q), a0 = 1 + alpha;
    b0 = alpha / a0;
    b2 = -alpha / a0;
    a1 = -2 * math.cos(w) / a0;
    a2 = (1 - alpha) / a0;
  }

  late final double b0, b2, a1, a2;
  double _x1 = 0, _x2 = 0, _y1 = 0, _y2 = 0;

  double call(double x) {
    final y = b0 * x + b2 * _x2 - a1 * _y1 - a2 * _y2;
    _x2 = _x1;
    _x1 = x;
    _y2 = _y1;
    _y1 = y;
    return y;
  }
}

/// Somebody typing a few words at a laptop's keyboard, heard from across a
/// desk (#526): muffled keystrokes — a soft knock and a quieter click, each
/// a little different — in words of a few letters, a deeper space bar
/// between them, now and then a pause. The same seed gives the same file.
/// It loops: it starts and ends in a pause, so the seam is not heard.
Float64List typing({double length = 9, double peak = 0.3, int seed = 526}) {
  final r = math.Random(seed);
  final out = Float64List((length * rate).round());
  double between(double lo, double hi) => lo + r.nextDouble() * (hi - lo);

  void key(double at, {required bool space}) {
    final start = (at * rate).round();
    final count = ((space ? 0.05 : 0.035) * rate).round();
    if (start + count >= out.length) return;
    // The body: a hollow knock, lower for the space bar. The click: the key
    // reaching its stop, brighter and shorter.
    final body = BandPass(space ? between(160, 220) : between(240, 420), 3);
    final click = BandPass(between(2200, 3600), 2.5);
    final level = space ? between(0.7, 0.9) : between(0.45, 1);
    final clickLevel = between(0.25, 0.5);
    for (var i = 0; i < count; i++) {
      final t = i / rate;
      final n = r.nextDouble() * 2 - 1;
      final knock = body(n) * math.exp(-t / (space ? 0.012 : 0.008)) * 6;
      final tick = click(n) * math.exp(-t / 0.002) * clickLevel * 3;
      // The key's release, softer, a moment after.
      final release = t > 0.018 ? body(n) * math.exp(-(t - 0.018) / 0.004) * 1.5 : 0.0;
      out[start + i] += (knock + tick + release) * level * math.min(1.0, t / 0.0006);
    }
  }

  // The last keystroke well before the end, so the loop starts over in a
  // pause.
  final last = length - 0.4;
  var at = between(0.25, 0.4);
  while (at < last - 0.5) {
    for (var k = 0, letters = 2 + r.nextInt(7); k < letters && at < last; k++) {
      key(at, space: false);
      at += between(0.075, 0.17);
    }
    if (at < last) key(at, space: true);
    // A short breath between words; now and then a longer think.
    at += r.nextDouble() < 0.18 ? between(0.6, 1.1) : between(0.14, 0.3);
  }
  var max = 0.0;
  for (final v in out) {
    max = math.max(max, v.abs());
  }
  if (max > 0) {
    for (var i = 0; i < out.length; i++) {
      out[i] = out[i] / max * peak;
    }
  }
  return out;
}

void main() {
  final dir = Directory('assets/sounds')..createSync(recursive: true);
  for (final e in sounds.entries) {
    final bytes = wav(e.value);
    File('${dir.path}/call_${e.key}.wav').writeAsBytesSync(bytes);
    final ms = (e.value.length * 1000 / rate).round();
    stdout.writeln('call_${e.key}.wav  $ms ms  ${(bytes.length / 1024).toStringAsFixed(1)} KB');
  }
}
