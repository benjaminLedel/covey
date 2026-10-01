import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:path_provider/path_provider.dart';

import '../diagnostics.dart';
import 'provider.dart';
import 'spoken_voice.dart';
import 'wav.dart';

/// What plays a filler: beside the agent's voice ([Speaker]).
abstract class FillerVoice {
  /// Plays [pcm] at [volume] (0–1) over and over, beside whatever else
  /// plays, until it is faded or stopped (#526). Answers whether it does.
  Future<bool> thinking(Pcm pcm, {required double volume});

  /// Lets the filler playing fade out over [over].
  Future<void> fadeFiller(Duration over);

  /// Stops the filler at once.
  Future<void> stopFiller();
}

typedef StartTimer = Timer Function(Duration after, void Function() fire);

/// What is heard while the agent thinks (#500, #526): a turn was sent
/// ([waiting]); after [after] without the reply, the typing starts — the
/// sound [sound] answers with its volume, null when the call's sounds are
/// off — and goes on until the reply. Once the reply's message is there
/// ([replyArrived]) it no longer starts; one that is playing fades over
/// [fade] when the reply is about to be spoken, and the reply waits for
/// that ([makeWay]). Barge-in, mute and hanging up stop it ([stop]).
/// Nothing starts while [quiet] — muted, the agent speaking, the person
/// speaking.
///
/// It used to be spoken words — "Hm…", "Sekunde…" — in the agent's voice.
/// Heard many times a call they sounded canned; a sound says the same
/// without words.
class CallFillers {
  CallFillers({
    required this.voice,
    required this.sound,
    required this.quiet,
    this.after = const Duration(milliseconds: 800),
    this.fade = const Duration(milliseconds: 250),
    StartTimer? timer,
  }) : _timer = timer ?? Timer.new;

  final FillerVoice voice;
  final Future<(Pcm, double)?> Function() sound;
  final bool Function() quiet;
  final Duration after;
  final Duration fade;

  final StartTimer _timer;

  Timer? _start;

  /// Counts the waits and the interruptions, so a sound looked up for one
  /// that has ended does not start.
  int _generation = 0;
  bool _playing = false;

  /// Asked for and not yet playing: its sound is being looked up.
  bool _starting = false;

  /// Whether the typing is playing now.
  bool get playing => _playing;

  /// A turn was sent: the waiting for its reply begins.
  void waiting() {
    _cancel();
    final g = ++_generation;
    _start = _timer(after, () => _fire(g));
  }

  /// The reply's message is there: the typing no longer starts. One
  /// already playing goes on until the reply makes way for it ([makeWay]).
  void replyArrived() {
    _cancel();
    if (_starting || !_playing) {
      _generation++;
      _starting = _playing = false;
    }
  }

  /// The reply is about to be spoken: the typing fades out over [fade] —
  /// the returned future completes when it has, so the reply does not
  /// start over its last keystrokes.
  Future<void> makeWay() async {
    _cancel();
    _generation++;
    if (!_playing) return;
    _playing = false;
    diag('call', 'typing fading over ${fade.inMilliseconds} ms before the reply');
    unawaited(voice.fadeFiller(fade).catchError((_) {}));
    final faded = Completer<void>();
    _timer(fade, faded.complete);
    await faded.future;
  }

  /// The reply's first audio: the typing fades out.
  void replyAudio() {
    _cancel();
    _generation++;
    _starting = false;
    if (_playing) {
      _playing = false;
      unawaited(voice.fadeFiller(fade).catchError((_) {}));
    }
  }

  /// Barge-in, mute, hanging up: the typing stops at once, nothing more
  /// for this turn.
  void stop() {
    _cancel();
    _generation++;
    _starting = false;
    if (_playing) {
      _playing = false;
      unawaited(voice.stopFiller().catchError((_) {}));
    }
  }

  void _cancel() {
    _start?.cancel();
    _start = null;
  }

  Future<void> _fire(int g) async {
    if (g != _generation || quiet() || _playing) return;
    // Counted as playing from here, so a barge-in meanwhile stops it.
    _playing = _starting = true;
    var on = false;
    try {
      final s = await sound();
      if (s != null && g == _generation) on = await voice.thinking(s.$1, volume: s.$2);
    } catch (e) {
      diag('call', 'typing failed: $e');
    }
    if (g == _generation) _starting = false;
    if (g != _generation) {
      // The reply came, or the person spoke, while it was looked up.
      if (on) unawaited(voice.stopFiller().catchError((_) {}));
      return;
    }
    if (!on) {
      _playing = false;
      return;
    }
    diag('call', 'typing');
  }

  void dispose() => stop();
}

/// What the provider synthesised ahead, as it is kept on this Mac (#500,
/// #506) — the greeting: one WAV per voice, style hint, speed and text, so
/// it plays without asking the provider again.
class FillerCache {
  FillerCache(this._dir);

  final Future<Directory> Function() _dir;

  /// Under the app's support directory.
  static FillerCache appSupport() =>
      FillerCache(() async => Directory('${(await getApplicationSupportDirectory()).path}/call-fillers'));

  static String key(SpokenVoice v, String text) =>
      sha256.convert(utf8.encode('${v.voice}\n${v.instructions}\n${v.speed}\n$text')).toString();

  Future<File> _file(SpokenVoice v, String text) async => File('${(await _dir()).path}/${key(v, text)}.wav');

  Future<Pcm?> read(SpokenVoice v, String text) async {
    try {
      final f = await _file(v, text);
      if (!await f.exists()) return null;
      return decodeWav(await f.readAsBytes());
    } catch (e) {
      diag('call', 'cached filler unreadable: $e');
      return null;
    }
  }

  Future<bool> has(SpokenVoice v, String text) async {
    try {
      return await (await _file(v, text)).exists();
    } catch (_) {
      return false;
    }
  }

  /// Written whole or not at all: a file half written by a call that ended
  /// is never read as a filler.
  Future<void> write(SpokenVoice v, String text, Pcm pcm) async {
    final f = await _file(v, text);
    await f.parent.create(recursive: true);
    final tmp = File('${f.path}.part');
    await tmp.writeAsBytes(encodeWav(pcm), flush: true);
    await tmp.rename(f.path);
  }
}

/// All of a stream's pieces as one.
Pcm joinPcm(List<Pcm> parts) {
  final total = parts.fold<int>(0, (n, p) => n + p.samples.length);
  final out = Pcm(Float32List(total), parts.isEmpty ? 0 : parts.first.sampleRate);
  var at = 0;
  for (final p in parts) {
    out.samples.setAll(at, p.samples);
    at += p.samples.length;
  }
  return out;
}
