import 'dart:typed_data';

/// Why a turn ended (#498).
enum TurnCut {
  /// The person paused for [TurnSegmenter.endSilence].
  pause,

  /// The turn reached [TurnSegmenter.maxTurn] without a pause.
  cap,
}

/// What the segmenter measured of one turn, for the diagnostics (#498).
class TurnStats {
  const TurnStats({
    required this.speech,
    required this.silence,
    required this.length,
    required this.cut,
    required this.windows,
    required this.voicedWindows,
    required this.longestRun,
  });

  /// Voice in the turn, all runs together.
  final Duration speech;

  /// The silence at its end, before the tail was trimmed.
  final Duration silence;

  /// The audio handed over.
  final Duration length;
  final TurnCut cut;

  /// The detector's verdicts: windows seen, windows voiced, and the longest
  /// run of voiced windows.
  final int windows;
  final int voicedWindows;
  final Duration longestRun;
}

/// Cuts a call's audio into turns (#494). Fed window by window — 16 kHz
/// mono PCM16 with the voice detector's verdict for each window — it says
/// when the person starts speaking ([onSpeech], which stops the agent
/// mid-sentence) and hands over each finished turn ([onTurn]) once the
/// person has been silent for [endSilence]. Partway into that pause it
/// hands over what the turn will be unless they speak again ([onLull]), so
/// it can be recognised ahead.
///
/// Knows nothing of microphones or models, so a test feeds it synthetic
/// windows.
class TurnSegmenter {
  TurnSegmenter({
    required this.onSpeech,
    required this.onTurn,
    this.onDiscard,
    this.onLull,
    this.lullAfter = const Duration(milliseconds: 500),
    this.endSilence = const Duration(milliseconds: 700),
    this.minSpeech = const Duration(milliseconds: 250),
    this.maxTurn = const Duration(seconds: 30),
    this.preRoll = const Duration(milliseconds: 300),
  });

  /// The person started speaking: [confirm] of voice in a row was heard.
  final void Function() onSpeech;

  /// A finished turn: its audio, from a little before the first voice to a
  /// little after the last, and what was measured of it.
  final void Function(Uint8List pcm, TurnStats stats) onTurn;

  /// A turn that ended with too little voice to be words — a knock, a
  /// cough, a click of the keyboard.
  final void Function()? onDiscard;

  /// The person has paused for [lullAfter] in a turn that would be kept:
  /// its audio so far, trimmed as [onTurn] trims it. The trailing silence
  /// beyond the tail is cut either way, so unless they speak again, the
  /// turn [onTurn] hands over is these very bytes. Once per pause, and only
  /// when [lullAfter] is shorter than [endSilence] and longer than the tail.
  final void Function(Uint8List pcm)? onLull;
  final Duration lullAfter;

  /// The pause that ends a turn.
  final Duration endSilence;

  /// Less voice than this in a turn is not a turn.
  final Duration minSpeech;

  /// A turn is handed over at this length even without a pause.
  final Duration maxTurn;

  /// Audio kept from before the first voiced window, so the first syllable
  /// is not cut: the detector needs a moment to be sure.
  final Duration preRoll;

  /// How much voice in a row counts as the person speaking. Zero while
  /// the agent is silent; longer while it speaks, so its own voice from the
  /// loudspeaker, caught in short bursts, does not interrupt it.
  Duration confirm = Duration.zero;

  static const _bytesPerMs = 32;

  final _preRoll = <Uint8List>[];
  int _preRollBytes = 0;

  final _turn = BytesBuilder(copy: false);
  bool _inTurn = false;
  bool _confirmed = false;
  int _voicedBytes = 0;
  int _runBytes = 0;
  int _silentBytes = 0;
  int _longestRunBytes = 0;
  int _windows = 0;
  int _voicedWindows = 0;
  bool _lulled = false;

  /// The trailing silence of a turn is not handed over beyond this.
  static const _tailBytes = 200 * _bytesPerMs;

  /// Whether a turn is open: the person has said something and not yet
  /// paused long enough.
  bool get inTurn => _inTurn;

  /// Whether the open turn has been confirmed as speech.
  bool get speaking => _inTurn && _confirmed;

  /// One window of audio and whether it is voice.
  void add(Uint8List window, bool voiced) {
    if (!_inTurn) {
      if (!voiced) {
        _keepPreRoll(window);
        return;
      }
      _open();
    }
    _turn.add(window);
    _windows++;
    if (voiced) {
      _voicedWindows++;
      _voicedBytes += window.length;
      _runBytes += window.length;
      if (_runBytes > _longestRunBytes) _longestRunBytes = _runBytes;
      _silentBytes = 0;
      _lulled = false;
      if (!_confirmed && _runBytes >= confirm.inMilliseconds * _bytesPerMs) {
        _confirmed = true;
        onSpeech();
      }
    } else {
      _runBytes = 0;
      _silentBytes += window.length;
    }
    if (_silentBytes >= endSilence.inMilliseconds * _bytesPerMs) {
      _close(TurnCut.pause);
    } else if (!_lulled && onLull != null && _silentBytes >= lullAfter.inMilliseconds * _bytesPerMs) {
      _lull();
    } else if (_turn.length >= maxTurn.inMilliseconds * _bytesPerMs) {
      _close(TurnCut.cap);
    }
  }

  /// Forgets the open turn and the pre-roll: muted, or the call ended.
  void reset() {
    _preRoll.clear();
    _preRollBytes = 0;
    _turn.clear();
    _inTurn = false;
    _confirmed = false;
    _lulled = false;
    _voicedBytes = _runBytes = _silentBytes = _longestRunBytes = _windows = _voicedWindows = 0;
  }

  void _lull() {
    _lulled = true;
    if (_silentBytes <= _tailBytes) return;
    if (!_confirmed || _voicedBytes < minSpeech.inMilliseconds * _bytesPerMs) return;
    final pcm = _turn.toBytes();
    onLull!(Uint8List.sublistView(pcm, 0, pcm.length - (_silentBytes - _tailBytes)));
  }

  void _keepPreRoll(Uint8List window) {
    _preRoll.add(window);
    _preRollBytes += window.length;
    while (_preRollBytes - _preRoll.first.length >= preRoll.inMilliseconds * _bytesPerMs) {
      _preRollBytes -= _preRoll.removeAt(0).length;
    }
  }

  void _open() {
    _inTurn = true;
    _confirmed = false;
    _lulled = false;
    _voicedBytes = _runBytes = _silentBytes = _longestRunBytes = _windows = _voicedWindows = 0;
    for (final w in _preRoll) {
      _turn.add(w);
    }
    _preRoll.clear();
    _preRollBytes = 0;
  }

  void _close(TurnCut why) {
    final pcm = _turn.takeBytes();
    final keep = _confirmed && _voicedBytes >= minSpeech.inMilliseconds * _bytesPerMs;
    final silent = _silentBytes;
    Duration ms(int bytes) => Duration(milliseconds: bytes ~/ _bytesPerMs);
    final stats = (speech: ms(_voicedBytes), windows: _windows, voiced: _voicedWindows, run: ms(_longestRunBytes));
    reset();
    if (!keep) {
      onDiscard?.call();
      return;
    }
    final cut = silent > _tailBytes ? silent - _tailBytes : 0;
    final turn = Uint8List.sublistView(pcm, 0, pcm.length - cut);
    onTurn(
      turn,
      TurnStats(
        speech: stats.speech,
        silence: ms(silent),
        length: ms(turn.length),
        cut: why,
        windows: stats.windows,
        voicedWindows: stats.voiced,
        longestRun: stats.run,
      ),
    );
  }
}

/// Whether what the microphone heard is the agent's own voice from the
/// loudspeaker rather than the person (#494): most of its words — the ones
/// longer than a "die" or a "the", which any two sentences share — are words
/// the agent just said. Without echo cancellation on the Mac, a call
/// without headphones hears itself.
bool looksLikeEcho(String heard, String spoken) {
  final said = _words(spoken).toSet();
  final words = _words(heard).where((w) => w.length > 3).toList();
  if (said.isEmpty || words.length < 2) return false;
  final echoed = words.where(said.contains).length;
  return echoed / words.length >= 0.7;
}

Iterable<String> _words(String text) =>
    RegExp(r"[\p{L}\p{N}']+", unicode: true).allMatches(text.toLowerCase()).map((m) => m[0]!);
