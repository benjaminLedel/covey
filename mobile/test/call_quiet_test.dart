import 'dart:async';

import 'package:covey_mobile/call/call.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// No notifications while the person is in a call (#525): the call marks
// what it fetched read, so the instance pushes none of it, and it tells the
// app's notifications when it starts and ends.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

CallController _call(FakeEars ears, FakeBackend backend, FakeSpeaker speaker, List<bool> hushed) => CallController(
  backend: backend,
  ears: ears,
  speaker: speaker,
  agentId: 'agent-1',
  appLanguage: 'de',
  words: (k) => words[k] ?? k,
  tuning: const CallTuning(window: Duration.zero),
  hush: hushed.add,
);

void main() {
  test('the call hushes the notifications from its start to its end', () async {
    final hushed = <bool>[];
    final call = _call(FakeEars(), FakeBackend(), FakeSpeaker(), hushed);
    await call.start();
    expect(hushed, [true]);
    await call.hangUp();
    expect(hushed, [true, false]);
    // Hanging up twice tells nobody twice.
    await call.hangUp();
    expect(hushed, [true, false]);
  });

  test('a call that fails lifts the hush at once, and only once', () async {
    final hushed = <bool>[];
    final ears = FakeEars()..gate = Completer<void>();
    final call = _call(ears, FakeBackend(), FakeSpeaker(), hushed);
    final started = call.start();
    ears.gate!.completeError(Exception('no microphone'));
    await started;
    expect(call.mode, CallMode.failed);
    expect(hushed, [true, false]);
    await call.hangUp();
    expect(hushed, [true, false]);
  });

  test('a reply the call fetched is marked read up to its time', () async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = _call(ears, backend, speaker, []);
    backend.agentSays('Von gestern.');
    await call.start();
    expect(backend.readUpTo, isEmpty, reason: 'opening the call reads nothing');
    ears.say('Wie weit bist du?');
    await _settle();
    backend.agentSays('Fast fertig.');
    await _settle();
    expect(backend.readUpTo, isNotEmpty);
    expect(backend.readUpTo.last, backend.messages.last.createdAt);
    await call.hangUp();
  });
}
