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

// A call's sounds and fillers (#500): short sounds for what happens, and
// one short word in the agent's voice while it thinks — never over its
// reply, never into a muted call.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

const _ms = Duration(milliseconds: 1);

void main() {
  group('choosing a filler', () {
    test('never the same twice in a row, always of the language', () {
      final p = FillerPicker(random: math.Random(7));
      String? last;
      for (var i = 0; i < 200; i++) {
        final t = p.pick('de-DE')!;
        expect(fillerPools['de'], contains(t));
        expect(t, isNot(last));
        last = t;
      }
      expect(p.pick('en'), isIn(fillerPools['en']!));
      expect(p.longWait('de'), 'Einen Augenblick noch.');
      expect(p.longWait('en_GB'), 'Just a moment.');
      expect(p.pick('sv'), isNull, reason: 'no fillers in a language without a pool');
    });

    test('every language of the app has a pool and a long wait', () {
      for (final l in ['en', 'de', 'es', 'fr', 'it', 'nl', 'pl', 'pt', 'ja', 'zh']) {
        expect(fillerPools[l], hasLength(greaterThanOrEqualTo(2)), reason: l);
        expect(longWaits[l], isNotNull, reason: l);
        expect(fillerTexts(l), hasLength(fillerPools[l]!.length + 1));
      }
    });
  });

  group('when a filler plays', () {
    late FakeClock clock;
    late FakeSpeaker voice;
    late bool quiet;
    late CallFillers f;

    setUp(() {
      clock = FakeClock();
      voice = FakeSpeaker();
      quiet = false;
      f = CallFillers(voice: voice, language: () => 'de', quiet: () => quiet, timer: clock.start);
    });

    test('after 0.8 s without the reply\'s audio, one per turn, faded when it comes', () async {
      f.waiting();
      await clock.advance(_ms * 790);
      expect(voice.fillers, isEmpty);
      await clock.advance(_ms * 20);
      expect(voice.fillers, hasLength(1));
      expect(f.playing, isTrue);
      await clock.advance(_ms * 3000);
      expect(voice.fillers, hasLength(1), reason: 'at most one short filler a turn');
      f.replyAudio();
      expect(voice.fades, 0, reason: 'the filler was over already');

      f.waiting();
      await clock.advance(_ms * 900);
      expect(voice.fillers, hasLength(2));
      expect(voice.fillers[1], isNot(voice.fillers[0]));
      f.replyAudio();
      expect(voice.fades, 1);
      expect(f.playing, isFalse);
      await clock.advance(const Duration(seconds: 20));
      expect(voice.fillers, hasLength(2), reason: 'nothing after the reply\'s audio');
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

    test('after 8 s once the long wait; not once the reply is there', () async {
      f.waiting();
      await clock.advance(const Duration(seconds: 8));
      expect(voice.fillers.last, 'Einen Augenblick noch.');
      expect(voice.fillers, hasLength(2));
      await clock.advance(const Duration(seconds: 30));
      expect(voice.fillers, hasLength(2), reason: 'once a turn');

      f.waiting();
      await clock.advance(_ms * 900);
      f.replyArrived(); // the message is there, its audio still synthesised
      await clock.advance(const Duration(seconds: 10));
      expect(voice.fillers, hasLength(3), reason: 'no long wait once the reply came');
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
      expect(voice.fillers, hasLength(1), reason: 'a stop ends the turn\'s fillers, the long wait too');
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

    test('a filler playing fades over 250 ms, and the reply waits for it', () async {
      f.waiting();
      await clock.advance(_ms * 850);
      expect(f.playing, isTrue);
      f.replyArrived();
      expect(f.playing, isTrue, reason: 'the text alone does not cut it off');
      var done = false;
      unawaited(f.makeWay().then((_) => done = true));
      await clock.advance(_ms * 249);
      expect(voice.fadedOver.single, const Duration(milliseconds: 250));
      expect(done, isFalse);
      await clock.advance(_ms * 1);
      expect(done, isTrue);
      expect(f.playing, isFalse);
      await clock.advance(const Duration(seconds: 20));
      expect(voice.fillers, hasLength(1), reason: 'nothing more for this turn');
    });

    test('making way without a filler playing is at once', () async {
      f.waiting();
      await clock.advance(_ms * 300);
      var done = false;
      unawaited(f.makeWay().then((_) => done = true));
      await clock.advance(Duration.zero);
      expect(done, isTrue);
      expect(voice.fades, 0);
    });

    test('nothing to say it with: nothing plays, nothing to fade', () async {
      voice.fillerLength = null;
      f.waiting();
      await clock.advance(_ms * 900);
      expect(f.playing, isFalse);
      f.replyAudio();
      expect(voice.fades, 0);
    });
  });

  group('the call', () {
    test('rings, connects, blips at a turn, and says a filler while it waits', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker(), out = FakeEarcons();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: out.sounds(), startTimer: clock.start);
      await call.start();
      await _settle();
      expect(out.names, [Earcon.ringing, Earcon.connected]);
      expect(out.played.first.$3, 2, reason: 'rings at most twice');
      expect(speaker.prefetched.single.$1, fillerTexts('de'), reason: 'the fillers are prepared at the start');
      expect(speaker.prefetched.single.$2, 'de');

      ears.say('Wie weit ist der Export?');
      await _settle();
      expect(out.names.last, Earcon.heard);
      expect(backend.posted, hasLength(1));
      await clock.advance(_ms * 700);
      expect(speaker.fillers, isEmpty);
      await clock.advance(_ms * 150);
      expect(speaker.fillers, hasLength(1));
      expect(fillerPools['de'], contains(speaker.fillers.single));

      backend.agentSays('Der Export läuft noch.');
      await _settle();
      expect(speaker.fades, 1, reason: 'the reply fades the filler');
      expect(speaker.fadedOver.single, const Duration(milliseconds: 250));
      expect(speaker.spoken, isEmpty, reason: 'the reply waits for the filler\'s fade (#511)');
      await clock.advance(_ms * 250);
      expect(speaker.spoken.single.$1, 'Der Export läuft noch.');
      expect(out.names.where((e) => e == Earcon.task), isEmpty, reason: 'an answer, not a task');
      await call.hangUp();
      await _settle();
      expect(out.names.last, Earcon.hangUp);
    });

    test('a quick reply has no filler; a muted call none either', () async {
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

    test('the reply\'s text before 0.8 s: no filler, whenever its audio comes (#511)', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, startTimer: clock.start);
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

    test('the person speaking stops the filler', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      final clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, startTimer: clock.start);
      await call.start();
      ears.say('Was steht an?');
      await _settle();
      await clock.advance(_ms * 850);
      expect(speaker.fillers, hasLength(1));
      // Its own filler from the loudspeaker, briefly, does not stop it.
      ears.feed(160, voiced: true);
      ears.feed(64, voiced: false);
      expect(speaker.fillerStops, 0);
      ears.feed(480, voiced: true);
      await _settle();
      expect(speaker.fillerStops, 1);
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
      for (final e in Earcon.values) {
        final data = await rootBundle.load(e.asset);
        final pcm = decodeWav(data.buffer.asUint8List(data.offsetInBytes, data.lengthInBytes));
        expect(pcm.sampleRate, 44100, reason: e.file);
        expect(pcm.duration, lessThan(const Duration(milliseconds: 1400)), reason: e.file);
        expect(pcm.samples.any((v) => v.abs() > 0.1), isTrue, reason: '${e.file} is heard');
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
