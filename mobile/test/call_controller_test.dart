import 'package:covey_mobile/call/call.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// A call's flow (#494): a turn goes into the conversation as a message said
// in a call, the agent's reply is spoken, the person interrupts it, and
// hanging up leaves nothing running.

Future<void> _settle() => Future<void>.delayed(Duration.zero).then((_) => Future<void>.delayed(Duration.zero));

void main() {
  test('a turn is posted, the reply is spoken in a voice of its language', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    backend.agentSays('Hallo, was gibt es?'); // before the call: not spoken
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    expect(call.mode, CallMode.listening);
    expect(ears.listening, isTrue);

    ears.feed(320, voiced: true);
    expect(call.mode, CallMode.hearing);
    ears.heard.add('Wie weit ist der Export?');
    ears.feed(736, voiced: false);
    await _settle();
    expect(backend.posted, ['Wie weit ist der Export?']);
    expect(call.mode, CallMode.thinking);
    expect(speaker.spoken, isEmpty);

    backend.agentSays('Der Export läuft, **fertig** in zehn Minuten.');
    await _settle();
    expect(call.mode, CallMode.speaking);
    expect(speaker.spoken.single.$1, 'Der Export läuft, fertig in zehn Minuten.');
    expect(speaker.spoken.single.$2, 'de');
    speaker.word();
    await _settle();
    expect(call.wordTicks.value, 1);
    speaker.finish();
    await _settle();
    expect(call.mode, CallMode.listening);
    expect(call.lines.map((l) => l.mine), [true, false]);
    await call.hangUp();
  });

  test('speaking on while the agent speaks stops it', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    ears.say('Erzähl mir vom Projekt.');
    await _settle();
    backend.agentSays('Das Projekt hat drei Teile.');
    backend.agentSays('Der erste ist der Export.');
    await _settle();
    expect(speaker.speaking, isTrue);
    // A short burst — its own voice from the loudspeaker — does not stop it.
    ears.feed(160, voiced: true);
    ears.feed(64, voiced: false);
    expect(speaker.stops, 0);
    // The person, speaking on.
    ears.feed(480, voiced: true);
    await _settle();
    expect(speaker.stops, 1);
    expect(call.mode, CallMode.hearing);
    // What it had still to say is dropped.
    expect(speaker.spoken, hasLength(1));
    await call.hangUp();
  });

  test('a task\'s acknowledgement and its later result are both spoken', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    ears.say('Mach bitte den Monatsbericht.');
    await _settle();
    backend.agentSays('Mach ich, ich melde mich mit dem Bericht.', taskId: 't1', replyTo: 'm0');
    await _settle();
    speaker.finish();
    await _settle();
    backend.agentSays('Der Monatsbericht ist fertig und liegt im Wiki.', taskId: 't1', kind: 'result');
    await _settle();
    expect(speaker.spoken.map((s) => s.$1), [
      'Mach ich, ich melde mich mit dem Bericht.',
      'Der Monatsbericht ist fertig und liegt im Wiki.',
    ]);
    await call.hangUp();
  });

  test('a long reply is cut, and the call says where the rest is', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    backend.agentSays(List.filled(6, 'Das ist ein langer Satz über den Stand der Dinge im Projekt.').join(' '));
    await _settle();
    speaker.finish();
    await _settle();
    expect(speaker.spoken.last.$1, 'Der Rest steht im Chat.');
    await call.hangUp();
  });

  test('the agent heard back from the loudspeaker is not posted', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    backend.agentSays('Ich habe die Rechnung für Initech gefunden.');
    await _settle();
    speaker.finish();
    ears.say('die Rechnung für Initech gefunden');
    await _settle();
    expect(backend.posted, isEmpty);
    await call.hangUp();
  });

  test('muted, nothing is heard; hanging up closes the microphone and stops speaking', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    await call.setMuted(true);
    expect(call.mode, CallMode.muted);
    expect(ears.listening, isFalse);
    ears.say('Hallo?');
    await _settle();
    expect(backend.posted, isEmpty);
    await call.setMuted(false);
    expect(ears.listening, isTrue);

    backend.agentSays('Noch etwas?');
    await _settle();
    expect(speaker.speaking, isTrue);
    await call.hangUp();
    expect(call.mode, CallMode.ended);
    expect(ears.closed, isTrue);
    expect(speaker.speaking, isFalse);
    // Nothing spoken after the call ended.
    backend.agentSays('Hallo?');
    await _settle();
    expect(speaker.spoken, hasLength(1));
  });
}
