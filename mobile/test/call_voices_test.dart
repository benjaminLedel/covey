import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/call/fillers.dart';
import 'package:covey_mobile/call/provider.dart';
import 'package:covey_mobile/call/speech_text.dart';
import 'package:covey_mobile/call/spoken_voice.dart';
import 'package:covey_mobile/call/voice.dart';
import 'package:flutter_test/flutter_test.dart';

// A call's voice (#497): the organisation's voice provider, streamed and
// decoded as it comes, the face following its level, and the Mac's voice
// only when the provider is not set or fails — said in a line.

const _system = [
  SystemVoice(id: 'de.anna', name: 'Anna', language: 'de-DE'),
  SystemVoice(id: 'en.samantha', name: 'Samantha', language: 'en-US'),
];

void main() {
  group('the spoken voice', () {
    test('is read from the conversation: the voice\'s own style, else the derived one', () {
      final own = SpokenVoice.fromConversation({
        'speech': {'voice': 'af_bella', 'instructions': 'calm and warm', 'speed': 1.2},
        'instructions': 'calm and warm',
      });
      expect(own, const SpokenVoice(voice: 'af_bella', instructions: 'calm and warm', speed: 1.2));
      final derived = SpokenVoice.fromConversation({'speech': null, 'instructions': 'casual and friendly, concise'});
      expect(derived, const SpokenVoice(instructions: 'casual and friendly, concise'));
      expect(SpokenVoice.fromConversation({}), const SpokenVoice());
    });

    test('the instance says whether there is a voice provider', () {
      expect(SpeechModelInfo.fromJson({'enabled': true, 'name': 'parakeet', 'synthesize': true}).synthesize, isTrue);
      expect(SpeechModelInfo.fromJson({'enabled': false}).synthesize, isFalse, reason: 'an older instance');
    });
  });

  group('audio', () {
    test('the mouth follows the loudness frame by frame', () {
      final samples = Float32List(22050);
      for (var i = 11025; i < 22050; i++) {
        samples[i] = i.isEven ? 0.5 : -0.5;
      }
      final l = levels(Pcm(samples, 22050), fps: 10);
      expect(l.length, 10);
      expect(l.first, 0);
      expect(l.last, greaterThan(0.8));
    });

    test('the loudness in dB, frame by frame, for the double-talk check (#511)', () {
      final samples = Float32List(22050);
      for (var i = 11025; i < 22050; i++) {
        samples[i] = i.isEven ? 0.5 : -0.5;
      }
      final db = levelsDb(Pcm(samples, 22050), fps: 10);
      expect(db.first, -100);
      expect(db.last, closeTo(-6.02, 0.01));
    });

    test('the provider\'s MP3 is decoded as it streams in, with the voice it is told', () async {
      final out = _Output();
      final v = ProviderVoice(
        (text, {voice = '', speed = 0, language = '', instructions = ''}) async {
          expect((voice, speed, language, instructions), ('af_bella', 1.2, 'de', 'calm, concise'));
          return ('audio/mpeg', Stream<List<int>>.fromIterable([List.filled(100, 1), List.filled(50, 2)]));
        },
        out,
        voice: const SpokenVoice(voice: 'af_bella', instructions: 'calm, concise', speed: 1.2),
      );
      final parts = await v.stream('Hallo.', language: 'de').toList();
      expect(parts.map((p) => p.samples.length), [100, 50]);
      expect(parts.first.sampleRate, 48000);
      expect(out.decoders, ['open', 'close']);

      final other = ProviderVoice(
        (text, {voice = '', speed = 0, language = '', instructions = ''}) async =>
            ('application/json', const Stream<List<int>>.empty()),
        out,
      );
      expect(other.stream('x').toList(), throwsFormatException);
    });
  });

  group('the speaker', () {
    test('the provider speaks as it streams in and drives the mouth', () async {
      final out = _Output();
      final said = <String>[];
      final s = _speaker(out, provider: _provider(out, said));
      expect(await s.prepare(language: 'de'), contains('provider voice af_bella'));
      expect(s.fallback.value, isFalse);
      final done = s.speak('Der Export läuft. Er ist gleich fertig.', language: 'de');
      await _settle();
      expect(said, ['Der Export läuft. Er ist gleich fertig.'], reason: 'the whole reply in one stream');
      expect(out.played.map((p) => p.$2), [false, false, true], reason: 'a mark of silence ends it');
      expect(out.said, isEmpty);
      out.emit(SpeakingEvent.started);
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(s.level.value, isNotNull);
      out.emit(SpeakingEvent.finished);
      await done;
      expect(s.level.value, isNull);
      s.dispose();
    });

    test('no provider set: the Mac speaks, in a voice of the language, and says so', () async {
      final out = _Output();
      final s = _speaker(out, available: false, provider: _provider(out, []));
      expect(await s.prepare(language: 'de'), contains('no voice provider'));
      expect(s.fallback.value, isTrue);
      unawaited(s.speak('Hallo.', language: 'de'));
      await _settle();
      expect(out.said.single, ('Hallo.', 'de.anna', 0.0));
      expect(out.played, isEmpty);
      s.dispose();
    });

    test('a provider that fails: the Mac speaks instead, and from then on', () async {
      final out = _Output();
      final said = <String>[];
      final s = _speaker(out, provider: _provider(out, said, fail: true));
      await s.prepare(language: 'de');
      unawaited(s.speak('Eins.', language: 'de'));
      await _settle();
      expect(said, ['Eins.']);
      expect(out.said.single, ('Eins.', 'de.anna', 1.2), reason: 'the agent\'s speed stays');
      expect(s.fallback.value, isTrue);
      out.emit(SpeakingEvent.finished);
      await _settle();
      unawaited(s.speak('Zwei.', language: 'de'));
      await _settle();
      expect(said, ['Eins.'], reason: 'not asked again in this call');
      expect(out.said.last.$1, 'Zwei.');
      s.dispose();
    });

    test('what plays is known in dB while it plays, the reply\'s and a filler\'s (#511)', () async {
      final out = _Output();
      final s = _speaker(out, provider: _provider(out, []));
      await s.prepare(language: 'de');
      expect(s.playbackDb, isNull);
      final done = s.speak('Der Export läuft.', language: 'de');
      await _settle();
      expect(s.playbackDb, isNull, reason: 'queued, not heard yet');
      out.emit(SpeakingEvent.started);
      await _settle();
      expect(s.playbackDb, closeTo(-10.46, 0.01));
      out.emit(SpeakingEvent.finished);
      await done;
      expect(s.playbackDb, isNull);

      // The typing (#526): its level at the volume it plays at, −6 dB of a
      // constant 0.5 and −6 dB more of half the volume.
      await s.thinking(Pcm(Float32List(4800)..fillRange(0, 4800, 0.5), 48000), volume: 0.5);
      expect(s.playbackDb, closeTo(-12.04, 0.01));
      await s.fadeFiller(const Duration(milliseconds: 250));
      expect(s.playbackDb, isNull);
      s.dispose();
    });

    test('the typing loops on the filler player at its volume, with or without a provider (#526)', () async {
      for (final available in [true, false]) {
        final out = _Output();
        final s = _speaker(out, available: available, provider: _provider(out, []));
        await s.prepare(language: 'de');
        expect(await s.thinking(Pcm(Float32List(4410), 44100), volume: 0.35), isTrue);
        expect(out.fillersPlayed.single, (4410, 0.35, true), reason: 'available: $available');
        expect(await s.thinking(Pcm(Float32List(4410), 44100), volume: 0), isFalse, reason: 'silent: not played');
        await s.fadeFiller(const Duration(milliseconds: 120));
        expect(out.fillerFades, 1);
        s.dispose();
      }
    });

    test('the greeting is synthesised ahead and played from it, not asked again (#506)', () async {
      final dir = await Directory.systemTemp.createTemp('greeting');
      addTearDown(() => dir.delete(recursive: true));
      final out = _Output();
      final said = <String>[];
      final s = _speaker(out, provider: _provider(out, said), cache: FillerCache(() async => dir));
      // Asked before the call prepares the voice: it prepares it once.
      expect(await s.prepareUtterance('Hallo Ada, hier ist Mira.', language: 'de'), isTrue);
      expect(said, ['Hallo Ada, hier ist Mira.']);
      expect(out.decoderIds.every((id) => id < 0), isTrue, reason: 'decoded aside from the replies');
      final done = s.speakPrepared('Hallo Ada, hier ist Mira.', language: 'de', ready: true);
      await _settle();
      expect(said, hasLength(1), reason: 'played from what was made');
      expect(out.played, [(4410, false), (1, true)]);
      out.emit(SpeakingEvent.started);
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(s.level.value, isNotNull, reason: 'the mouth follows it');
      out.emit(SpeakingEvent.finished);
      await done;
      s.dispose();
    });

    test('a greeting not ready in time: the Mac says it; without a provider nothing is made', () async {
      final out = _Output();
      final said = <String>[];
      final s = _speaker(out, provider: _provider(out, said));
      await s.prepare(language: 'de');
      unawaited(s.speakPrepared('Hallo Ada.', language: 'de', ready: false));
      await _settle();
      expect(said, isEmpty, reason: 'asking now would be too late');
      expect(out.said.single.$1, 'Hallo Ada.');
      expect(s.fallback.value, isFalse, reason: 'the provider speaks the rest of the call');
      s.dispose();

      final none = _Output();
      final t = _speaker(none, available: false, provider: _provider(none, said));
      expect(await t.prepareUtterance('Hallo Ada.', language: 'de'), isFalse);
      expect(none.decoders, isEmpty);
      t.dispose();
    });

    test('stopping drops what the provider still sends', () async {
      final out = _Output();
      final gate = Completer<void>();
      final s = _speaker(out, provider: _provider(out, [], gate: gate));
      await s.prepare(language: 'de');
      final done = s.speak('Eins. Zwei.', language: 'de');
      await _settle();
      await s.stop();
      await done;
      gate.complete();
      await _settle();
      expect(out.played, isEmpty);
      expect(out.stops, 1);
      s.dispose();
    });
  });
}

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

const _voice = SpokenVoice(voice: 'af_bella', instructions: 'calm and warm', speed: 1.2);

AgentSpeaker _speaker(
  _Output out, {
  bool available = true,
  ProviderVoice Function(SpokenVoice)? provider,
  FillerCache? cache,
}) => AgentSpeaker(
  output: out,
  agentId: 'agent-1',
  available: () async => available,
  spoken: () async => _voice,
  provider: provider,
  fillerCache: cache,
);

/// A provider that answers two chunks of MP3, or fails, noting each text.
ProviderVoice Function(SpokenVoice) _provider(
  _Output out,
  List<String> said, {
  bool fail = false,
  Completer<void>? gate,
}) =>
    (v) => ProviderVoice(
      (text, {voice = '', speed = 0, language = '', instructions = ''}) async {
        said.add(text);
        await gate?.future;
        if (fail) throw ApiException(502, 'the voice provider did not answer');
        return ('audio/mpeg', Stream<List<int>>.fromIterable([List.filled(2205, 1), List.filled(2205, 2)]));
      },
      out,
      voice: v,
    );

class _Output implements VoiceOutput {
  final _events = StreamController<(SpeakingEvent, int)>.broadcast();
  final said = <(String, String?, double)>[];
  final played = <(int, bool)>[];
  final decoders = <String>[];
  int stops = 0;
  int _last = 0;

  void emit(SpeakingEvent e) => _events.add((e, _last));

  @override
  Stream<(SpeakingEvent, int)> get events => _events.stream;

  @override
  Future<String?> language(String text) async => null;

  @override
  Future<void> play(int id, Pcm pcm, {required bool last}) async {
    _last = id;
    played.add((pcm.samples.length, last));
  }

  @override
  Future<void> say(int id, String text, {String? voiceId, required String language, double rate = 0}) async {
    _last = id;
    said.add((text, voiceId, rate));
  }

  @override
  Future<void> stop() async => stops++;

  final fillersPlayed = <(int, double, bool)>[];
  int fillerFades = 0;
  final decoderIds = <int>[];

  @override
  Future<void> playFiller(Pcm pcm, {double volume = 1, bool loop = false}) async =>
      fillersPlayed.add((pcm.samples.length, volume, loop));

  @override
  Future<void> fadeFiller(Duration over) async => fillerFades++;

  @override
  Future<void> stopFiller() async {}

  @override
  Future<bool> openDecoder(int id) async {
    decoders.add('open');
    decoderIds.add(id);
    return true;
  }

  @override
  Future<Pcm> decode(int id, Uint8List bytes) async {
    final s = Float32List(bytes.length);
    for (var i = 0; i < s.length; i++) {
      s[i] = i.isEven ? 0.3 : -0.3;
    }
    return Pcm(s, 48000);
  }

  @override
  Future<void> closeDecoder(int id) async => decoders.add('close');

  @override
  Future<List<SystemVoice>> voices() async => _system;

  @override
  void dispose() {}
}
