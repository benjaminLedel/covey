import 'dart:async';
import 'dart:typed_data';

import 'package:covey_mobile/api.dart';
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

AgentSpeaker _speaker(_Output out, {bool available = true, ProviderVoice Function(SpokenVoice)? provider}) =>
    AgentSpeaker(
      output: out,
      agentId: 'agent-1',
      available: () async => available,
      spoken: () async => _voice,
      provider: provider,
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

  @override
  Future<bool> openDecoder(int id) async {
    decoders.add('open');
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
