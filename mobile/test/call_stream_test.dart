import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// The reply streamed while the instance writes it (#529): each sentence is
// spoken as it comes, the message after it adds only what was not heard,
// and the person speaking into it drops the rest.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  test('sentences are spoken as they come, the message adds nothing heard', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    final stream = backend.streamNext();
    ears.say('Ist der Merge Request drin?');
    await _settle();
    final asked = backend.spokenAsked.single;
    expect(asked, backend.messages.last.id, reason: 'asked for the reply to the turn just posted');

    stream.add('Ja, ist drin.');
    await _settle();
    expect(speaker.spoken.map((s) => s.$1), ['Ja, ist drin.'], reason: 'before the message is written');
    stream.add('Die Tests sind grün.');
    await _settle();
    speaker.finish();
    await _settle();
    expect(speaker.spoken.map((s) => s.$1), ['Ja, ist drin.', 'Die Tests sind grün.']);
    await stream.close();

    backend.agentReplies(
      asked,
      'Ist drin — MR !88, Pipeline grün.',
      meta: {'spoken': 'Ja, ist drin. Die Tests sind grün.'},
    );
    await _settle();
    speaker.finish();
    await _settle();
    expect(speaker.spoken, hasLength(2), reason: 'nothing said twice');
    expect(call.lines.last.text, contains('Pipeline grün'), reason: 'the written reply in the transcript');
    await call.hangUp();
  });

  test('what the stream did not carry is said when the message comes', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    final stream = backend.streamNext();
    ears.say('Wie weit ist der Export?');
    await _settle();
    final asked = backend.spokenAsked.single;
    stream.add('Läuft noch.');
    await _settle();
    backend.agentReplies(
      asked,
      'Läuft noch — 3 von 7 Dateien, ETA 10:40.',
      meta: {'spoken': 'Läuft noch. Gut die Hälfte ist durch.', 'details_in_chat': 'true'},
    );
    await _settle();
    stream.add('Gut die Hälfte ist durch.'); // late: the message is there
    await _settle();
    for (var i = 0; i < 3; i++) {
      speaker.finish();
      await _settle();
    }
    expect(speaker.spoken.map((s) => s.$1), [
      'Läuft noch.',
      'Gut die Hälfte ist durch.',
      'Die Details stehen im Chat.',
    ]);
    await call.hangUp();
  });

  test('the person speaking into a streamed reply drops the rest of it', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    final stream = backend.streamNext();
    ears.say('Was steht heute an?');
    await _settle();
    final asked = backend.spokenAsked.single;
    stream.add('Drei Sachen.');
    await _settle();
    expect(speaker.speaking, isTrue);
    ears.feed(640, voiced: true); // barge-in
    await _settle();
    expect(speaker.stops, greaterThan(0));
    stream.add('Erstens der Export.');
    await _settle();
    backend.agentReplies(
      asked,
      'Drei Sachen: Export, Review, Rechnung.',
      meta: {'spoken': 'Drei Sachen. Erstens der Export.'},
    );
    await _settle();
    expect(speaker.spoken.map((s) => s.$1), ['Drei Sachen.'], reason: 'the rest stands in the chat');
    await call.hangUp();
  });

  test('a stream with nothing in it: the message is spoken as before', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    ears.say('Hallo?');
    await _settle();
    backend.agentReplies(backend.spokenAsked.single, 'Hallo!');
    await _settle();
    expect(speaker.spoken.map((s) => s.$1), ['Hallo!']);
    await call.hangUp();
  });
}
