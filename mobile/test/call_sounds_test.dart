import 'dart:async';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/fillers.dart';
import 'package:covey_mobile/call/provider.dart';
import 'package:covey_mobile/call/sounds.dart';
import 'package:covey_mobile/call/spoken_voice.dart';
import 'package:covey_mobile/call/wav.dart';
import 'package:covey_mobile/prefs.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// A call's sounds (#500): short sounds for what happens, and the typing
// while the agent thinks (#526) — never over its reply, never into a muted
// call.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

const _ms = Duration(milliseconds: 1);

Future<Pcm> _bundled(Earcon e) async {
  final data = await rootBundle.load(e.asset);
  return decodeWav(data.buffer.asUint8List(data.offsetInBytes, data.lengthInBytes));
}

void main() {
  group('when the typing plays', () {
    late FakeClock clock;
    late FakeSpeaker voice;
    late bool quiet;
    late (Pcm, double)? sound;
    late CallFillers f;

    setUp(() {
      clock = FakeClock();
      voice = FakeSpeaker();
      quiet = false;
      sound = (Pcm(Float32List(4), 44100), 0.35);
      f = CallFillers(
        voice: voice,
        sound: () async => sound,
        quiet: () => quiet,
        after: const Duration(milliseconds: 800),
        timer: clock.start,
      );
    });

    test('after 0.8 s without the reply\'s audio, until it comes, then faded', () async {
      f.waiting();
      await clock.advance(_ms * 790);
      expect(voice.fillers, isEmpty);
      await clock.advance(_ms * 20);
      expect(voice.fillers, [0.35], reason: 'at the sounds\' volume');
      expect(f.playing, isTrue);
      await clock.advance(const Duration(seconds: 30));
      expect(voice.fillers, hasLength(1), reason: 'started once, it goes on');
      expect(f.playing, isTrue, reason: 'it loops until the reply');
      f.replyAudio();
      expect(voice.fades, 1);
      expect(f.playing, isFalse);
      await clock.advance(const Duration(seconds: 20));
      expect(voice.fillers, hasLength(1), reason: 'nothing after the reply\'s audio');
    });

    test('no typing with the call\'s sounds off', () async {
      sound = null;
      f.waiting();
      await clock.advance(const Duration(seconds: 5));
      expect(voice.fillers, isEmpty);
      expect(f.playing, isFalse);
    });

    test('no filler when the reply is quicker than 0.8 s', () async {
      f.waiting();
      await clock.advance(_ms * 500);
      f.replyArrived();
      f.replyAudio();
      await clock.advance(const Duration(seconds: 20));
      expect(voice.fillers, isEmpty);
      expect(voice.fades, 0);
    });

    test('never while quiet, and a stop ends the filler at once', () async {
      quiet = true;
      f.waiting();
      await clock.advance(const Duration(seconds: 10));
      expect(voice.fillers, isEmpty, reason: 'muted: nothing is said');

      quiet = false;
      f.waiting();
      await clock.advance(_ms * 850);
      expect(f.playing, isTrue);
      f.stop();
      expect(voice.fillerStops, 1);
      await clock.advance(const Duration(seconds: 10));
      expect(voice.fillers, hasLength(1), reason: 'a stop ends the turn\'s typing');
    });

    test('none once the reply\'s text is there, though its audio is not yet (#511)', () async {
      f.waiting();
      await clock.advance(_ms * 700);
      f.replyArrived(); // the message, its audio still being made
      await clock.advance(const Duration(seconds: 20));
      expect(voice.fillers, isEmpty);
    });

    test('one still being looked up when the text arrives does not start', () async {
      voice.fillerGate = Completer<void>();
      f.waiting();
      await clock.advance(_ms * 850);
      f.replyArrived();
      voice.fillerGate!.complete();
      await clock.advance(_ms * 10);
      expect(f.playing, isFalse);
      expect(voice.fillerStops, 1, reason: 'what was looked up is stopped');
    });

    test('the reply\'s message fades the typing over 250 ms at once, its audio not awaited (#527)', () async {
      f.waiting();
      await clock.advance(_ms * 850);
      expect(f.playing, isTrue);
      f.replyArrived();
      expect(voice.fadedOver.single, const Duration(milliseconds: 250));
      expect(f.playing, isFalse);
      f.replyAudio();
      expect(voice.fades, 1, reason: 'faded once');
      await clock.advance(const Duration(seconds: 20));
      expect(voice.fillers, hasLength(1), reason: 'nothing more for this turn');
    });

    test('nothing to say it with: nothing plays, nothing to fade', () async {
      voice.canThink = false;
      f.waiting();
      await clock.advance(_ms * 900);
      expect(f.playing, isFalse);
      f.replyAudio();
      expect(voice.fades, 0);
    });
  });

  group('the call', () {
    test('rings, connects, taps at a turn, and types while it waits', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds(), startTimer: clock.start);
      await call.start();
      await _settle();
      expect(out.names, [Earcon.ringing, Earcon.connected]);
      expect(out.played.first.$3, 2, reason: 'rings at most twice');

      ears.say('Wie weit ist der Export?');
      await _settle();
      expect(out.names.last, Earcon.heard);
      expect(backend.posted, hasLength(1));
      await clock.advance(_ms * 700);
      expect(speaker.fillers, isEmpty);
      await clock.advance(_ms * 150);
      expect(speaker.fillers, [0.35], reason: 'the typing, at the sounds\' volume');
      expect(out.names.where((e) => e == Earcon.typing), isEmpty, reason: 'not on the sounds\' own player');

      backend.agentSays('Der Export läuft noch.');
      await _settle();
      expect(speaker.fades, 1, reason: 'the reply fades the typing');
      expect(speaker.fadedOver.single, const Duration(milliseconds: 250));
      expect(speaker.spoken.single.$1, 'Der Export läuft noch.', reason: 'asked for at once, under the fade (#527)');
      expect(out.names.where((e) => e == Earcon.task), isEmpty, reason: 'an answer, not a task');
      await call.hangUp();
      await _settle();
      expect(out.names.last, Earcon.hangUp);
    });

    test('a quick reply has no typing; a muted call none either', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds(), startTimer: clock.start);
      await call.start();
      ears.say('Hallo?');
      await _settle();
      await clock.advance(_ms * 300);
      backend.agentSays('Ja, hallo.');
      await _settle();
      await clock.advance(const Duration(seconds: 10));
      expect(speaker.fillers, isEmpty);
      speaker.finish();
      await _settle();

      ears.say('Und der Bericht?');
      await _settle();
      await call.setMuted(true);
      await clock.advance(const Duration(seconds: 10));
      expect(speaker.fillers, isEmpty, reason: 'nothing is said into a muted call');
      await call.setMuted(false);
      await _settle();
      expect(out.names.skip(2).where((e) => e != Earcon.heard), [Earcon.mute, Earcon.unmute]);
      await call.hangUp();
    });

    test('the reply\'s text before 0.8 s: no typing, whenever its audio comes (#511)', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds(), startTimer: clock.start);
      await call.start();
      ears.say('Wie weit ist der Export?');
      await _settle();
      await clock.advance(_ms * 700);
      backend.agentSays('Der Export läuft noch.');
      await _settle();
      await clock.advance(const Duration(seconds: 10));
      expect(speaker.fillers, isEmpty);
      expect(speaker.spoken.single.$1, 'Der Export läuft noch.');
      await call.hangUp();
    });

    test('the person speaking stops the typing', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds(), startTimer: clock.start);
      await call.start();
      ears.say('Was steht an?');
      await _settle();
      await clock.advance(_ms * 850);
      expect(speaker.fillers, hasLength(1));
      // Its own typing from the loudspeaker, briefly, does not stop it.
      ears.feed(160, voiced: true);
      ears.feed(64, voiced: false);
      expect(speaker.fillerStops, 0);
      ears.feed(480, voiced: true);
      await _settle();
      expect(speaker.fillerStops, 1);
      await call.hangUp();
    });

    test('the typing starts while the turn is still recognised and shown, before it is sent', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      final clock = FakeClock();
      final call = CallController(
        backend: backend,
        ears: ears,
        speaker: speaker,
        agentId: 'agent-1',
        appLanguage: 'de',
        words: (k) => words[k] ?? k,
        // The understood window, as set by default: the turn waits in it.
        tuning: const CallTuning(window: Duration(milliseconds: 1500)),
        sounds: out.sounds(),
        startTimer: clock.start,
      );
      await call.start();
      ears.say('Wie weit ist der Export?');
      await _settle();
      await clock.advance(_ms * 250);
      expect(speaker.fillers, isEmpty);
      await clock.advance(_ms * 60);
      expect(speaker.fillers, hasLength(1), reason: '0.3 s after the end of the turn was heard');
      expect(backend.posted, isEmpty, reason: 'the turn is still in its window');
      await call.hangUp();
      expect(speaker.fillerStops, 1);
    });

    test('a turn that goes nowhere stops the typing', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds(), startTimer: clock.start);
      await call.start();
      ears.say(''); // nothing recognised
      await _settle();
      await clock.advance(const Duration(seconds: 2));
      expect(speaker.fillers, isEmpty);
      expect(backend.posted, isEmpty);
      await call.hangUp();
    });

    test('a task created sounds; its result later does not', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      backend.agentSays('Ich habe den alten Bericht fertig.', taskId: 't0', replyTo: 'x');
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds());
      await call.start();
      ears.say('Mach bitte den Monatsbericht.');
      await _settle();
      backend.agentSays('Mach ich.', taskId: 't1', replyTo: 'm1');
      await _settle();
      expect(out.names.where((e) => e == Earcon.task), hasLength(1));
      speaker.finish();
      backend.agentSays('Der Monatsbericht ist fertig.', taskId: 't1', kind: 'result');
      await _settle();
      speaker.finish();
      // A note to a task the conversation knew already is no new task.
      backend.agentSays('Notiert.', taskId: 't0', replyTo: 'm3');
      await _settle();
      expect(out.names.where((e) => e == Earcon.task), hasLength(1));
      await call.hangUp();
    });
  });

  group('the sounds', () {
    test('play at the set volume, not at all when off or silent', () async {
      final out = FakeEarcons();
      var on = true;
      var volume = 0.35;
      final s = out.sounds(enabled: () => on, volume: () => volume);
      await s.play(Earcon.heard);
      expect(out.played.single, (Earcon.heard, 0.35, 1));
      volume = 0.8;
      await s.play(Earcon.ringing, loops: 2);
      expect(out.played.last, (Earcon.ringing, 0.8, 2));
      on = false;
      await s.play(Earcon.mute);
      on = true;
      volume = 0;
      await s.play(Earcon.mute);
      expect(out.played, hasLength(2));
    });

    test('the setting: on and quiet unless changed, and kept', () async {
      Prefs.instance.inMemory();
      await CallSettings.load();
      expect(CallSettings.sounds.value, isTrue);
      expect(CallSettings.volume.value, CallSettings.defaultVolume);
      await CallSettings.setSounds(false);
      await CallSettings.setVolume(0.6);
      CallSettings.sounds.value = true;
      CallSettings.volume.value = 0;
      await CallSettings.load();
      expect(CallSettings.sounds.value, isFalse);
      expect(CallSettings.volume.value, 0.6);
      await CallSettings.setSounds(true);
      await CallSettings.setVolume(CallSettings.defaultVolume);
    });

    test('every sound is bundled as short 16-bit mono WAV at 44.1 kHz', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      for (final e in Earcon.values.where((e) => e != Earcon.typing)) {
        final pcm = await _bundled(e);
        expect(pcm.sampleRate, 44100, reason: e.file);
        expect(pcm.duration, lessThan(const Duration(milliseconds: 1400)), reason: e.file);
        expect(pcm.samples.any((v) => v.abs() > 0.05), isTrue, reason: '${e.file} is heard');
      }
    });

    test('the typing is long enough not to be heard repeating, quiet, and loops without a seam', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      final pcm = await _bundled(Earcon.typing);
      expect(pcm.duration, greaterThanOrEqualTo(const Duration(seconds: 6)));
      final peak = pcm.samples.fold<double>(0, (m, v) => math.max(m, v.abs()));
      expect(peak, inInclusiveRange(0.1, 0.35));
      // Silent at both ends: where it starts over, no keystroke is cut.
      final edge = pcm.sampleRate ~/ 10;
      expect(pcm.samples.take(edge).every((v) => v.abs() < 0.001), isTrue);
      expect(pcm.samples.skip(pcm.samples.length - edge).every((v) => v.abs() < 0.001), isTrue);
    });

    test('the tap at a turn is quieter than the other sounds', () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      double peak(Pcm p) => p.samples.fold<double>(0, (m, v) => math.max(m, v.abs()));
      final heard = peak(await _bundled(Earcon.heard));
      for (final e in [Earcon.connected, Earcon.task, Earcon.mute, Earcon.hangUp]) {
        expect(heard, lessThan(peak(await _bundled(e))), reason: e.file);
      }
    });

    test('WAV round trip', () {
      final s = Float32List.fromList([0, 0.5, -0.5, 0.25]);
      final back = decodeWav(encodeWav(Pcm(s, 24000)));
      expect(back.sampleRate, 24000);
      for (var i = 0; i < s.length; i++) {
        expect(back.samples[i], closeTo(s[i], 1 / 16000));
      }
      expect(() => decodeWav(Uint8List.fromList([1, 2, 3])), throwsFormatException);
    });
  });

  group('the filler cache', () {
    test('a key per voice, style, speed and text', () {
      const a = SpokenVoice(voice: 'af_bella', instructions: 'calm', speed: 1.2);
      final k = FillerCache.key(a, 'Hm…');
      expect(k, FillerCache.key(const SpokenVoice(voice: 'af_bella', instructions: 'calm', speed: 1.2), 'Hm…'));
      expect(k, matches(RegExp(r'^[0-9a-f]{64}$')), reason: 'nothing of the voice in the file name');
      for (final other in [
        FillerCache.key(const SpokenVoice(voice: 'am_adam', instructions: 'calm', speed: 1.2), 'Hm…'),
        FillerCache.key(const SpokenVoice(voice: 'af_bella', instructions: 'warm', speed: 1.2), 'Hm…'),
        FillerCache.key(const SpokenVoice(voice: 'af_bella', instructions: 'calm', speed: 1), 'Hm…'),
        FillerCache.key(a, 'Sekunde…'),
      ]) {
        expect(other, isNot(k));
      }
    });

    test('kept on disk and read back', () async {
      final dir = await Directory.systemTemp.createTemp('fillers');
      addTearDown(() => dir.delete(recursive: true));
      final c = FillerCache(() async => dir);
      const v = SpokenVoice(voice: 'af_bella');
      expect(await c.read(v, 'Hm…'), isNull);
      await c.write(v, 'Hm…', Pcm(Float32List.fromList([0.1, 0.2]), 24000));
      expect(await c.has(v, 'Hm…'), isTrue);
      expect((await c.read(v, 'Hm…'))!.samples, hasLength(2));
      expect(dir.listSync().whereType<File>().where((f) => f.path.endsWith('.part')), isEmpty);
    });
  });
}
