import 'dart:async';
import 'dart:math' as math;

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/greeting.dart';
import 'package:covey_mobile/call/sounds.dart';
import 'package:covey_mobile/prefs.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// The agent greets when the call connects (#506): by name, in the chat
// tone's du or Sie, fitting the time of day, never the same greeting twice
// in a row; synthesised while the line rings and said after the connected
// sound, interrupted by the person speaking, and in the Mac's voice when
// the provider's audio is late.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

final _morning = DateTime(2026, 9, 30, 8), _afternoon = DateTime(2026, 9, 30, 15), _evening = DateTime(2026, 9, 30, 20);

const _grace = GreetingFacts(personName: 'Grace Hopper', department: 'Support');

class _Memory implements GreetingMemory {
  final keys = <String, String>{};

  @override
  Future<String?> last(String agentId) async => keys[agentId];

  @override
  Future<void> remember(String agentId, String key) async => keys[agentId] = key;
}

Greeting _compose(
  String language, {
  GreetingFacts facts = _grace,
  DateTime? time,
  String? last,
  int seed = 1,
  String agent = 'Mira',
}) => composeGreeting(
  language: language,
  agentName: agent,
  facts: facts,
  time: time ?? _afternoon,
  last: last,
  random: math.Random(seed),
)!;

void main() {
  group('choosing a greeting', () {
    test('never the one said last; the same kind differs in opening and question', () {
      for (var seed = 0; seed < 60; seed++) {
        final first = _compose('de', seed: seed);
        final next = _compose('de', last: first.key, seed: seed + 1000);
        expect(next.key, isNot(first.key));
        final a = first.key.split('.'), b = next.key.split('.');
        expect(b[3], isNot(a[3]), reason: 'another opening');
        expect(b[5], isNot(a[5]), reason: 'another question');
      }
    });

    test('fits the time of day', () {
      for (var seed = 0; seed < 30; seed++) {
        expect(_compose('de', time: _morning, seed: seed).text, matches(RegExp(r'^(Guten Morgen|Moin|Morgen)')));
        expect(_compose('de', time: _evening, seed: seed).text, matches(RegExp(r'^(Guten Abend|Hallo|Hi)')));
        expect(_compose('en', time: _morning, seed: seed).key, contains('.morning.'));
        expect(_compose('en', time: _afternoon, seed: seed).key, contains('.day.'));
        expect(_compose('en', time: DateTime(2026, 9, 30, 2), seed: seed).key, contains('.evening.'));
      }
      expect(dayTimeOf(DateTime(2026, 1, 1, 11, 59)), DayTime.morning);
      expect(dayTimeOf(DateTime(2026, 1, 1, 12)), DayTime.day);
      expect(dayTimeOf(DateTime(2026, 1, 1, 18)), DayTime.evening);
      expect(dayTimeOf(DateTime(2026, 1, 1, 4)), DayTime.evening);
    });

    test('informal: the first name and du; formal: Sie and the full name', () {
      for (var seed = 0; seed < 30; seed++) {
        final du = _compose('de', seed: seed);
        expect(du.text, contains('Grace'));
        expect(du.text, isNot(contains('Hopper')));
        expect(du.text, isNot(matches(RegExp(r'\b(Sie|Ihnen)\b'))));
        expect(du.text, contains('Mira'));
        expect(du.text, contains('aus dem Team Support'));

        final sie = _compose(
          'de',
          facts: const GreetingFacts(personName: 'Grace Hopper', address: 'sie'),
          seed: seed,
        );
        expect(sie.key, startsWith('de.formal.'));
        expect(sie.text, contains('Grace Hopper'));
        expect(sie.text, matches(RegExp(r'\b(Sie|Ihnen)\b')));
      }
      // Unknown is informal in German, and the plain friendly form in English.
      expect(_compose('de', facts: const GreetingFacts(personName: 'Grace')).key, startsWith('de.informal.'));
      expect(_compose('en', facts: const GreetingFacts(personName: 'Grace')).key, startsWith('en.informal.'));
      expect(_compose('de', facts: const GreetingFacts(address: 'du')).key, startsWith('de.informal.'));
    });

    test('formal without a surname, or without any name: no name at all', () {
      expect(greetingName('Grace', formal: true), '');
      expect(greetingName('Grace Brewster Hopper', formal: true), 'Grace Brewster Hopper');
      expect(greetingName('Grace Hopper', formal: false), 'Grace');
      expect(greetingName('grace@example.org', formal: false), '');
      final formal = _compose(
        'de',
        facts: const GreetingFacts(personName: 'Grace', address: 'sie'),
        time: _afternoon,
      );
      expect(formal.text, isNot(contains('Grace')));
      expect(formal.text, matches(RegExp(r'^(Guten Tag|Schönen guten Tag|Hallo)\. ')));
    });

    test('every language, register and time reads whole with names missing', () {
      expect(greetingLanguages, containsAll(['de', 'en', 'es', 'fr', 'it', 'nl', 'pl', 'pt', 'ja', 'zh']));
      for (final lang in greetingLanguages) {
        for (final facts in const [
          GreetingFacts(),
          GreetingFacts(address: 'sie'),
          GreetingFacts(personName: 'Grace Hopper', department: 'Support'),
          GreetingFacts(personName: 'Grace Hopper', department: 'Support', address: 'sie'),
        ]) {
          for (final time in [_morning, _afternoon, _evening]) {
            for (var seed = 0; seed < 12; seed++) {
              final g = _compose(lang, facts: facts, time: time, seed: seed);
              expect(g.text, isNot(matches(RegExp(r'[\[\]{}]'))), reason: '$lang ${g.key}: ${g.text}');
              expect(g.text, isNot(matches(RegExp(r' [,.]|,[,.!?]|、[。！]|，[。！]'))), reason: g.text);
              expect(g.text, contains('Mira'));
              expect(g.language, lang);
            }
          }
        }
      }
      expect(composeGreeting(language: 'sv', agentName: 'Mira', facts: _grace, time: _morning), isNull);
      expect(_compose('de-AT').language, 'de');
    });

    test('the greeter remembers the last per agent', () async {
      final memory = _Memory();
      final greeter = CallGreeter(enabled: () => true, memory: memory, now: () => _morning, random: math.Random(3));
      String? before;
      for (var i = 0; i < 20; i++) {
        final g = await greeter.choose(agentId: 'a1', agentName: 'Mira', language: 'de', facts: _grace);
        expect(g!.key, isNot(before));
        expect(memory.keys['a1'], g.key);
        before = g.key;
      }
      expect(memory.keys.keys, ['a1']);
    });
  });

  group('the call', () {
    CallGreeter greeter({bool Function()? enabled, Duration wait = const Duration(milliseconds: 1500)}) =>
        CallGreeter(enabled: enabled ?? () => true, memory: _Memory(), now: () => _morning, wait: wait);

    test('rings, is synthesised while ringing, connects, greets, then listens', () async {
      final ears = FakeEars()..gate = Completer<void>();
      final backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons(), clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds(), startTimer: clock.start, greeter: greeter());
      final started = call.start();
      await _settle();
      expect(call.mode, CallMode.preparing);
      expect(speaker.prepared, hasLength(1), reason: 'made while the models load');
      final text = speaker.prepared.single;
      expect(text, contains('Grace'));
      expect(text, contains('Ada Lovelace'));
      speaker.ready.complete(true);

      ears.gate!.complete();
      await started;
      await _settle();
      expect(out.names, [Earcon.ringing, Earcon.connected]);
      expect(speaker.spokenPrepared, isEmpty, reason: 'after the connected sound');
      await clock.advance(const Duration(milliseconds: 1));
      expect(speaker.spokenPrepared, [(text, true)]);
      expect(call.mode, CallMode.speaking, reason: 'the face speaks');
      expect(call.lines.single.text, text);
      expect(call.lines.single.mine, isFalse);
      expect(backend.posted, isEmpty, reason: 'not a message');
      expect(backend.messages, isEmpty);

      speaker.finish();
      await _settle();
      expect(call.mode, CallMode.listening);
      ears.say('Wie weit ist der Export?');
      await _settle();
      expect(backend.posted, ['Wie weit ist der Export?']);
      await call.hangUp();
    });

    test('the person speaking stops the greeting and is heard', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), clock = FakeClock();
      speaker.ready.complete(true);
      final call = fakeCall(ears, backend, speaker, startTimer: clock.start, greeter: greeter());
      await call.start();
      await _settle();
      expect(speaker.spokenPrepared, hasLength(1));
      expect(call.mode, CallMode.speaking);
      ears.feed(480, voiced: true);
      await _settle();
      expect(speaker.stops, 1);
      expect(call.mode, CallMode.hearing);
      ears.heard.add('Ich brauche den Bericht.');
      ears.feed(1056, voiced: false);
      await _settle();
      expect(backend.posted, ['Ich brauche den Bericht.']);
      await call.hangUp();
    });

    test('speaking before the greeting began: it is not said', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, startTimer: clock.start, greeter: greeter());
      await call.start();
      await _settle();
      ears.feed(480, voiced: true);
      speaker.ready.complete(true);
      await clock.advance(const Duration(seconds: 2));
      expect(speaker.spokenPrepared, isEmpty);
      expect(call.lines, isEmpty);
      await call.hangUp();
    });

    test('the provider\'s audio late: after the wait, the Mac\'s voice says it', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, startTimer: clock.start, greeter: greeter());
      await call.start();
      await _settle();
      await clock.advance(const Duration(milliseconds: 1400));
      expect(speaker.spokenPrepared, isEmpty, reason: 'waited for');
      await clock.advance(const Duration(milliseconds: 200));
      expect(speaker.spokenPrepared.single.$2, isFalse, reason: 'the Mac says it');
      expect(call.mode, CallMode.speaking);
      speaker.finish();
      await _settle();
      expect(call.mode, CallMode.listening);
      await call.hangUp();
    });

    test('an answer arriving during the greeting waits for its end', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), clock = FakeClock();
      speaker.ready.complete(true);
      final call = fakeCall(ears, backend, speaker, startTimer: clock.start, greeter: greeter());
      await call.start();
      await _settle();
      backend.agentSays('Der Bericht ist fertig.');
      await _settle();
      expect(speaker.spoken, hasLength(1), reason: 'the greeting only');
      speaker.finish();
      await _settle();
      expect(speaker.spoken.last.$1, 'Der Bericht ist fertig.');
      await call.hangUp();
    });

    test('switched off: nothing is made or said', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), clock = FakeClock();
      final call = fakeCall(
        ears,
        backend,
        speaker,
        startTimer: clock.start,
        greeter: greeter(enabled: () => false),
      );
      await call.start();
      await clock.advance(const Duration(seconds: 3));
      expect(speaker.prepared, isEmpty);
      expect(speaker.spokenPrepared, isEmpty);
      expect(call.mode, CallMode.listening);
      await call.hangUp();
    });

    test('the setting: on unless switched off, and kept', () async {
      Prefs.instance.inMemory();
      await CallSettings.load();
      expect(CallSettings.greeting.value, isTrue);
      await CallSettings.setGreeting(false);
      CallSettings.greeting.value = true;
      await CallSettings.load();
      expect(CallSettings.greeting.value, isFalse);
      await CallSettings.setGreeting(true);
    });
  });
}
