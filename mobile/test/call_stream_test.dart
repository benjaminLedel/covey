import 'dart:async';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/sounds.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// The reply streamed while the instance writes it (#529): its spoken form
// is said as one utterance once it is complete, or once the first sentence
// has waited long enough (#533); the message after it adds only what was
// not heard, the next utterance is synthesised while one is spoken, and
// the person speaking into it drops the rest.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

const _end = SpokenPiece.end();
SpokenPiece _s(String text) => SpokenPiece(text);

void main() {
  late FakeEars ears;
  late FakeBackend backend;
  late FakeSpeaker speaker;
  late FakeClock clock;
  late CallController call;

  setUp(() async {
    ears = FakeEars();
    backend = FakeBackend();
    speaker = FakeSpeaker();
    clock = FakeClock();
    call = fakeCall(ears, backend, speaker, startTimer: clock.start);
    await call.start();
  });
  tearDown(() => call.hangUp());

  Iterable<String> said() => speaker.spoken.map((s) => s.$1);

  test('the spoken form is said as one utterance when it is complete (#533)', () async {
    final stream = backend.streamNext();
    ears.say('Ist der Merge Request drin?');
    await _settle();
    final asked = backend.spokenAsked.single;
    expect(asked, backend.messages.last.id, reason: 'asked for the reply to the turn just posted');

    stream.add(_s('Ja, ist drin.'));
    await _settle();
    expect(said(), isEmpty, reason: 'held for the rest of the spoken form');
    stream.add(_s('Die Tests sind grün.'));
    stream.add(_end);
    await _settle();
    expect(said(), ['Ja, ist drin. Die Tests sind grün.'], reason: 'one utterance, one intonation');
    await stream.close();

    backend.agentReplies(
      asked,
      'Ist drin — MR !88, Pipeline grün.',
      meta: {'spoken': 'Ja, ist drin. Die Tests sind grün.'},
    );
    await _settle();
    speaker.finish();
    await _settle();
    expect(said(), hasLength(1), reason: 'nothing said twice');
    expect(call.lines.last.text, contains('Pipeline grün'), reason: 'the written reply in the transcript');
  });

  test('the first sentence waits at most 0.3 s; what comes after is said after it', () async {
    final stream = backend.streamNext();
    ears.say('Wie weit ist der Export?');
    await _settle();
    stream.add(_s('Läuft noch.'));
    await _settle();
    await clock.advance(const Duration(milliseconds: 290));
    expect(said(), isEmpty);
    await clock.advance(const Duration(milliseconds: 10));
    expect(said(), ['Läuft noch.']);
    stream.add(_s('Gut die Hälfte ist durch.'));
    await _settle();
    speaker.finish();
    await _settle();
    expect(said(), ['Läuft noch.', 'Gut die Hälfte ist durch.']);
  });

  test('what the stream did not carry is said when the message comes', () async {
    final stream = backend.streamNext();
    ears.say('Wie weit ist der Export?');
    await _settle();
    final asked = backend.spokenAsked.single;
    stream.add(_s('Läuft noch.'));
    await _settle();
    backend.agentReplies(
      asked,
      'Läuft noch — 3 von 7 Dateien, ETA 10:40.',
      meta: {'spoken': 'Läuft noch. Gut die Hälfte ist durch.', 'details_in_chat': 'true'},
    );
    await _settle();
    stream.add(_s('Gut die Hälfte ist durch.')); // late: the message is there
    await _settle();
    for (var i = 0; i < 3; i++) {
      speaker.finish();
      await _settle();
    }
    expect(said(), ['Läuft noch.', 'Gut die Hälfte ist durch.', 'Die Details stehen im Chat.']);
  });

  test('the next utterance is synthesised while one is spoken, and played from that (#533)', () async {
    final stream = backend.streamNext();
    ears.say('Wie weit ist der Export?');
    await _settle();
    final asked = backend.spokenAsked.single;
    stream.add(_s('Läuft noch.'));
    stream.add(_end);
    await _settle();
    speaker.readyFor['Die Details stehen im Chat.'] = Completer<bool>()..complete(true);
    backend.agentReplies(
      asked,
      'Läuft noch — 3 von 7 Dateien.',
      meta: {'spoken': 'Läuft noch. Gut die Hälfte ist durch.', 'details_in_chat': 'true'},
    );
    await _settle();
    speaker.finish(); // "Läuft noch." ends; the rest starts, the word about the chat is made
    await _settle();
    expect(speaker.prepared, ['Die Details stehen im Chat.']);
    speaker.finish();
    await _settle();
    expect(speaker.spokenPrepared, [('Die Details stehen im Chat.', true)]);
    expect(said(), ['Läuft noch.', 'Gut die Hälfte ist durch.', 'Die Details stehen im Chat.']);
  });

  test('the person speaking into a streamed reply drops the rest of it', () async {
    final stream = backend.streamNext();
    ears.say('Was steht heute an?');
    await _settle();
    final asked = backend.spokenAsked.single;
    stream.add(_s('Drei Sachen.'));
    await _settle();
    await clock.advance(const Duration(milliseconds: 300));
    expect(speaker.speaking, isTrue);
    ears.feed(640, voiced: true); // barge-in
    await _settle();
    expect(speaker.stops, greaterThan(0));
    stream.add(_s('Erstens der Export.'));
    await _settle();
    backend.agentReplies(
      asked,
      'Drei Sachen: Export, Review, Rechnung.',
      meta: {'spoken': 'Drei Sachen. Erstens der Export.'},
    );
    await _settle();
    expect(said(), ['Drei Sachen.'], reason: 'the rest stands in the chat');
  });

  test('a stream with nothing in it: the message is spoken as before', () async {
    ears.say('Hallo?');
    await _settle();
    backend.agentReplies(backend.spokenAsked.single, 'Hallo!');
    await _settle();
    expect(said(), ['Hallo!']);
  });

  test('the reply is read when its stream ends, without an event (#531)', () async {
    final stream = backend.streamNext();
    ears.say('Wie weit ist der Export?');
    await _settle();
    final asked = backend.spokenAsked.single;
    stream.add(_s('Läuft noch.'));
    await _settle();
    backend.agentReplies(
      asked,
      'Läuft noch — 3 von 7.',
      meta: {'spoken': 'Läuft noch. Gut die Hälfte ist durch.'},
      notify: false,
    );
    await stream.close();
    await _settle();
    speaker.finish();
    await _settle();
    expect(said(), ['Läuft noch.', 'Gut die Hälfte ist durch.']);
  });

  test('the previous reply, read late, does not end the next wait (#531)', () async {
    await call.hangUp();
    call = fakeCall(ears, backend, speaker, sounds: FakeEarcons().sounds(), startTimer: clock.start);
    await call.start();
    // The first turn's reply: streamed, its message written without an event,
    // and its stream not closed yet when the next turn is posted.
    final first = backend.streamNext();
    ears.say('Hallo?');
    await _settle();
    final asked = backend.spokenAsked.single;
    first.add(_s('Hallo!'));
    first.add(_end);
    await _settle();
    speaker.finish();
    await _settle();
    backend.agentReplies(asked, 'Hallo!', meta: {'spoken': 'Hallo!'}, notify: false);

    backend.streamNext();
    ears.say('Wie weit ist der Export?');
    await _settle();
    await clock.advance(const Duration(milliseconds: 900));
    expect(speaker.fillers, hasLength(1), reason: 'the typing of the second wait');
    await _settle();
    expect(speaker.fades, 0, reason: 'the first reply, read now, is no answer to the second turn');
    expect(said(), ['Hallo!'], reason: 'and was heard already');
  });
}
