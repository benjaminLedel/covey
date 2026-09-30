import 'dart:typed_data';

import 'package:covey_mobile/call/turns.dart';
import 'package:flutter_test/flutter_test.dart';

// A call's turns (#494): windows of 32 ms with the voice detector's verdict,
// cut where the person pauses for 0.7 s.

/// 32 ms of 16 kHz PCM16, every sample [value] — so a turn's bytes say
/// which windows it holds.
Uint8List _window(int value) => Uint8List(1024)..fillRange(0, 1024, value);

class _Recorder {
  int speech = 0;
  int discarded = 0;
  final turns = <Uint8List>[];
  late final seg = TurnSegmenter(onSpeech: () => speech++, onTurn: turns.add, onDiscard: () => discarded++);

  /// [ms] of voice (or of silence), in whole windows.
  void feed(int ms, {required bool voiced, int value = 1}) {
    for (var i = 0; i < ms ~/ 32; i++) {
      seg.add(_window(value), voiced);
    }
  }
}

void main() {
  test('a turn ends at a pause of 0.7 s, not at a shorter one', () {
    final r = _Recorder();
    r.feed(640, voiced: false);
    r.feed(960, voiced: true);
    r.feed(480, voiced: false); // a breath between two phrases
    r.feed(640, voiced: true);
    expect(r.turns, isEmpty);
    expect(r.speech, 1, reason: 'one person speaking, heard once');
    r.feed(640, voiced: false);
    expect(r.turns, isEmpty, reason: '0.64 s is not yet the pause');
    r.feed(64, voiced: false);
    expect(r.turns, hasLength(1));
    expect(r.seg.inTurn, isFalse);
  });

  test('the turn keeps a little before the first voice and cuts the long tail', () {
    final r = _Recorder();
    r.feed(640, voiced: false, value: 7); // room tone before
    r.feed(320, voiced: true, value: 9);
    r.feed(704, voiced: false, value: 7);
    final pcm = r.turns.single;
    // Pre-roll: at least 300 ms of the room before the voice.
    final firstVoice = pcm.indexOf(9);
    expect(firstVoice, greaterThanOrEqualTo(300 * 32));
    expect(firstVoice, lessThan(400 * 32));
    // After the voice, about 200 ms of the pause, not all 700.
    final tail = pcm.length - (pcm.lastIndexOf(9) + 1);
    expect(tail, inInclusiveRange(190 * 32, 240 * 32));
  });

  test('a knock is not a turn', () {
    final r = _Recorder();
    r.feed(96, voiced: true);
    r.feed(800, voiced: false);
    expect(r.turns, isEmpty);
    expect(r.discarded, 1);
  });

  test('while the agent speaks, only a sustained voice interrupts it', () {
    final r = _Recorder();
    r.seg.confirm = const Duration(milliseconds: 400);
    // Its own voice from the loudspeaker, in short bursts.
    for (var i = 0; i < 4; i++) {
      r.feed(192, voiced: true);
      r.feed(96, voiced: false);
    }
    expect(r.speech, 0);
    r.feed(800, voiced: false);
    expect(r.turns, isEmpty, reason: 'bursts that never held for 0.4 s are dropped');
    // The person, speaking on.
    r.feed(448, voiced: true);
    expect(r.speech, 1);
    r.feed(704, voiced: false);
    expect(r.turns, hasLength(1));
  });

  test('a turn without a pause is handed over at 30 s', () {
    final r = _Recorder();
    r.feed(31000, voiced: true);
    expect(r.turns, hasLength(1));
    expect(r.turns.single.length, lessThanOrEqualTo(30 * 32000 + 1024));
  });

  test('reset forgets the open turn', () {
    final r = _Recorder();
    r.feed(640, voiced: true);
    r.seg.reset();
    r.feed(800, voiced: false);
    expect(r.turns, isEmpty);
    expect(r.seg.inTurn, isFalse);
  });

  test('the agent heard back from the loudspeaker is told from the person', () {
    const said = 'Ich habe die Rechnung für Initech gefunden und schicke sie dir gleich.';
    expect(looksLikeEcho('die Rechnung für Initech gefunden', said), isTrue);
    expect(looksLikeEcho('Warte, nicht die für Initech, die für Globex!', said), isFalse);
    expect(looksLikeEcho('Stopp', said), isFalse, reason: 'one word is no evidence');
    expect(looksLikeEcho('die Rechnung', ''), isFalse);
  });
}
