import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/sounds.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// The agent's goodbye ends the call (#517): a message marked end_call is
// spoken — its spoken form — then the hang-up sound plays and the call ends.
// The person speaking into the goodbye keeps the call open, and the setting
// switches it off.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

const _goodbye = {'spoken': 'Gern, bis später!', 'end_call': 'true'};

void main() {
  test('the goodbye is spoken, then the hang-up sound, then the call ends', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
    final call = fakeCall(ears, backend, speaker, sounds: out.sounds());
    await call.start();
    ears.say('Danke dir, das war\'s. Tschüss!');
    await _settle();
    backend.agentSays('Gern, bis später! 👋', meta: _goodbye);
    await _settle();
    expect(speaker.spoken.single.$1, 'Gern, bis später!', reason: 'its spoken form');
    expect(call.ended, isFalse, reason: 'not before the goodbye is said');
    expect(call.endingAfterGoodbye, isTrue);
    expect(out.names, isNot(contains(Earcon.hangUp)));
    speaker.finish();
    await _settle();
    expect(call.ended, isTrue);
    expect(out.names.last, Earcon.hangUp);
    expect(ears.closed, isTrue);
    // Both lines stay in the conversation.
    expect(backend.messages.map((m) => m.authorKind), ['human', 'agent']);
  });

  test('a goodbye with the details in the chat says both, then hangs up', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    ears.say('Schick Bernd noch die Zusammenfassung, danke, tschüss!');
    await _settle();
    backend.agentSays(
      'Mach ich, schick ich Bernd per Mail. Tschüss!',
      taskId: 't1',
      replyTo: 'm0',
      meta: {'spoken': 'Mach ich, ich meld mich im Chat. Tschüss!', 'details_in_chat': 'true', 'end_call': 'true'},
    );
    await _settle();
    speaker.finish();
    await _settle();
    expect(call.ended, isFalse, reason: 'the word about the chat is still to come');
    speaker.finish();
    await _settle();
    expect(speaker.spoken.map((s) => s.$1), [
      'Mach ich, ich meld mich im Chat. Tschüss!',
      'Die Details stehen im Chat.',
    ]);
    expect(call.ended, isTrue);
  });

  test('speaking into the goodbye keeps the call open', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
    final call = fakeCall(ears, backend, speaker, sounds: out.sounds());
    await call.start();
    ears.say('Danke, das war\'s.');
    await _settle();
    backend.agentSays('Gern, bis später!', meta: _goodbye);
    await _settle();
    expect(speaker.speaking, isTrue);
    // "Ach, warte noch …": the person speaks on over the goodbye.
    ears.feed(640, voiced: true);
    await _settle();
    expect(speaker.stops, 1);
    expect(call.endingAfterGoodbye, isFalse);
    ears.heard.add('Ach warte, noch eine Frage zur Rechnung.');
    ears.feed(1056, voiced: false);
    await _settle();
    expect(call.ended, isFalse);
    expect(out.names, isNot(contains(Earcon.hangUp)));
    expect(backend.posted.last, 'Ach warte, noch eine Frage zur Rechnung.');
    await call.hangUp();
  });

  test('switched off, the goodbye is spoken and the line stays open', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = CallController(
      backend: backend,
      ears: ears,
      speaker: speaker,
      agentId: 'agent-1',
      appLanguage: 'de',
      words: (k) => words[k] ?? k,
      tuning: const CallTuning(window: Duration.zero),
      mayHangUp: () => false,
    );
    await call.start();
    ears.say('Danke, das war\'s.');
    await _settle();
    backend.agentSays('Gern, bis später!', meta: _goodbye);
    await _settle();
    speaker.finish();
    await _settle();
    expect(speaker.spoken.single.$1, 'Gern, bis später!');
    expect(call.ended, isFalse);
    expect(call.mode, CallMode.listening);
    await call.hangUp();
  });

  test('a reply without the mark never hangs up', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await call.start();
    ears.say('Danke, und kannst du noch den Bericht schicken?');
    await _settle();
    backend.agentSays('Klar, mach ich.', meta: const {'spoken': 'Klar, mach ich.'});
    await _settle();
    speaker.finish();
    await _settle();
    expect(call.ended, isFalse);
    await call.hangUp();
  });

  test('the setting is on by default', () async {
    expect(CallSettings.hangUp.value, isTrue);
  });
}
