import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:path_provider/path_provider.dart';

import '../diagnostics.dart';
import 'provider.dart';
import 'spoken_voice.dart';
import 'wav.dart';

/// The short words an agent says while it thinks (#500), by language: one
/// of them after a moment without its reply, never the same twice in a row.
const fillerPools = <String, List<String>>{
  'de': ['Hm…', 'Okay, Moment…', 'Mhm, ich schau kurz.', 'Sekunde…'],
  'en': ['Hmm…', 'Okay, one sec…', 'Let me check.', 'One moment…'],
  'es': ['Mmm…', 'Vale, un momento…', 'Déjame ver.', 'Un segundo…'],
  'fr': ['Hmm…', 'D’accord, une seconde…', 'Je regarde.', 'Un instant…'],
  'it': ['Mmm…', 'Okay, un attimo…', 'Fammi controllare.', 'Un secondo…'],
  'nl': ['Hm…', 'Oké, momentje…', 'Even kijken.', 'Eén seconde…'],
  'pl': ['Hmm…', 'Dobrze, chwileczkę…', 'Już sprawdzam.', 'Sekundkę…'],
  'pt': ['Hum…', 'Ok, um segundo…', 'Vou verificar.', 'Só um momento…'],
  'ja': ['うーん…', 'はい、少々お待ちください…', '確認しますね。', '少々…'],
  'zh': ['嗯…', '好的，稍等…', '我看一下。', '稍等一下…'],
};

/// What the agent says once when the reply takes long.
const longWaits = <String, String>{
  'de': 'Einen Augenblick noch.',
  'en': 'Just a moment.',
  'es': 'Solo un momento más.',
  'fr': 'Encore un instant.',
  'it': 'Ancora un momento.',
  'nl': 'Nog even geduld.',
  'pl': 'Jeszcze chwila.',
  'pt': 'Só mais um momento.',
  'ja': 'もう少しお待ちください。',
  'zh': '请再稍等一下。',
};

String _base(String language) => language.split(RegExp('[-_]')).first.toLowerCase();

/// Every filler of [language], the long wait included: what is synthesised
/// ahead. Empty for a language without fillers.
List<String> fillerTexts(String language) {
  final b = _base(language);
  return [...?fillerPools[b], ?longWaits[b]];
}

/// Picks the next filler: at random from the language's pool, never the
/// one said last.
class FillerPicker {
  FillerPicker({math.Random? random}) : _random = random ?? math.Random();

  final math.Random _random;
  String? _last;

  /// Null for a language without fillers.
  String? pick(String language) {
    final pool = fillerPools[_base(language)];
    if (pool == null || pool.isEmpty) return null;
    final choices = pool.length > 1 ? pool.where((t) => t != _last).toList() : pool;
    return _last = choices[_random.nextInt(choices.length)];
  }

  String? longWait(String language) => longWaits[_base(language)];
}

/// What plays a filler: the agent's voice ([Speaker]).
abstract class FillerVoice {
  /// Plays [text] as a filler beside whatever else plays, and returns how
  /// long it lasts — null when there is nothing to play it with: the
  /// provider's audio is not cached yet, or neither the provider nor the
  /// Mac has a voice for the language.
  Future<Duration?> filler(String text, {required String language});

  /// Lets the filler playing fade out over [over]; a filler still being
  /// looked up does not start any more.
  Future<void> fadeFiller(Duration over);

  /// Stops the filler at once; one still being looked up does not start.
  Future<void> stopFiller();
}

typedef StartTimer = Timer Function(Duration after, void Function() fire);

/// When a call says a filler (#500). A turn was sent ([waiting]): after
/// [after] without the reply, one short filler; after [longAfter] without
/// the reply, the long wait, once. Once the reply's message is there
/// ([replyArrived]) no filler starts any more, even while its audio is
/// still being made (#511): a filler that started then was cut off by the
/// reply a few hundred milliseconds later. One that is playing finishes its
/// word — it fades over [fade], and the reply waits for that ([makeWay]).
/// Barge-in, mute and hanging up stop it ([stop]). Nothing is said while
/// [quiet] — muted, the agent speaking, the person speaking.
class CallFillers {
  CallFillers({
    required this.voice,
    required this.language,
    required this.quiet,
    this.after = const Duration(milliseconds: 800),
    this.longAfter = const Duration(seconds: 8),
    this.fade = const Duration(milliseconds: 250),
    this.onPlaying,
    StartTimer? timer,
    math.Random? random,
  }) : _timer = timer ?? Timer.new,
       picker = FillerPicker(random: random);

  final FillerVoice voice;
  final String Function() language;
  final bool Function() quiet;
  final Duration after;
  final Duration longAfter;
  final Duration fade;

  /// A filler started, lasting the given time: the call keeps its words to
  /// tell its echo from the person.
  final void Function(String text, Duration length)? onPlaying;

  final StartTimer _timer;
  final FillerPicker picker;

  Timer? _short;
  Timer? _long;
  Timer? _ends;

  /// Counts the waits and the interruptions, so a filler looked up for one
  /// that has ended does not start.
  int _generation = 0;
  bool _playing = false;

  /// A filler asked for and not yet playing: its audio is being looked up.
  bool _starting = false;

  /// Whether a filler is playing now.
  bool get playing => _playing;

  /// A turn was sent: the waiting for its reply begins.
  void waiting() {
    _cancel();
    final g = ++_generation;
    _short = _timer(after, () => _fire(g, picker.pick(language())));
    _long = _timer(longAfter, () => _fire(g, picker.longWait(language())));
  }

  /// The reply's message is there: no filler starts any more, and one
  /// still being looked up does not start. One already playing goes on
  /// until the reply makes way for it ([makeWay]).
  void replyArrived() {
    _cancel();
    if (_starting) {
      _generation++;
      _starting = false;
      _playing = false;
    }
  }

  /// The reply is about to be spoken: nothing more is said for this turn,
  /// and a filler playing fades out over [fade] — the returned future
  /// completes when it has, so the reply does not talk over its last word.
  Future<void> makeWay() async {
    replyArrived();
    if (!_playing) return;
    _generation++;
    _playing = false;
    _ends?.cancel();
    diag('call', 'filler fading over ${fade.inMilliseconds} ms before the reply');
    unawaited(voice.fadeFiller(fade).catchError((_) {}));
    final faded = Completer<void>();
    _timer(fade, faded.complete);
    await faded.future;
  }

  /// The reply's first audio: nothing more is said for this turn, and a
  /// filler playing fades out.
  void replyAudio() {
    _cancel();
    _generation++;
    _starting = false;
    if (_playing) {
      _playing = false;
      unawaited(voice.fadeFiller(fade).catchError((_) {}));
    }
  }

  /// Barge-in, mute, hanging up: the filler stops at once, nothing more
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
    _short?.cancel();
    _long?.cancel();
    _short = _long = null;
  }

  Future<void> _fire(int g, String? text) async {
    if (g != _generation || text == null || quiet() || _playing) return;
    final lang = language();
    _playing = true;
    _starting = true;
    Duration? length;
    try {
      length = await voice.filler(text, language: lang);
    } catch (e) {
      diag('call', 'filler failed: $e');
    }
    if (g == _generation) _starting = false;
    if (g != _generation) {
      // The reply came, or the person spoke, while it was looked up.
      if (length != null) unawaited(voice.stopFiller().catchError((_) {}));
      return;
    }
    if (length == null) {
      _playing = false;
      return;
    }
    diag('call', 'filler, ${length.inMilliseconds} ms');
    onPlaying?.call(text, length);
    _ends?.cancel();
    _ends = _timer(length, () {
      if (g == _generation) _playing = false;
    });
  }

  void dispose() {
    stop();
    _ends?.cancel();
  }
}

/// The provider's fillers as they are kept on this Mac (#500): one WAV per
/// voice, style hint, speed and text, so a filler plays without asking the
/// provider — and the next call with the same voice need not ask again.
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
