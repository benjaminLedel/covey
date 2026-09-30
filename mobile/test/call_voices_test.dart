import 'dart:async';
import 'dart:typed_data';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/call/speech_text.dart';
import 'package:covey_mobile/call/synth.dart';
import 'package:covey_mobile/call/voice.dart';
import 'package:covey_mobile/call/voice_choice.dart';
import 'package:flutter_test/flutter_test.dart';

// covey's own voices in a call (#497): which voice speaks, the catalogue as
// the instance describes it, the audio of a speech server, and the speaker
// that plays sentence by sentence and falls back when a voice cannot speak.

SpeechModelInfo _voice(String name, String language, {int speakers = 1}) => SpeechModelInfo(
  enabled: true,
  name: name,
  engine: 'tts',
  voice: TtsVoiceInfo(family: 'vits', language: language, speakers: speakers, label: name),
);

const _system = [
  SystemVoice(id: 'de.anna', name: 'Anna', language: 'de-DE'),
  SystemVoice(id: 'en.samantha', name: 'Samantha', language: 'en-US'),
];

void main() {
  group('the catalogue', () {
    test('voices are read apart from the models, with what the app needs', () {
      final i = SpeechModelInfo.fromJson({
        'enabled': true,
        'name': 'parakeet',
        'models': [
          {'name': 'parakeet', 'engine': 'parakeet'},
        ],
        'synthesize': true,
        'voices': [
          {
            'name': 'piper-en-norman',
            'engine': 'tts',
            'sha256': 'ab',
            'size': 20987233,
            'unpack': 'tar.bz2',
            'files': [
              {'name': 'vits-piper-en_US-norman-medium-int8.tar.bz2', 'sha256': 'ab', 'size': 20987233},
            ],
            'voice': {
              'family': 'vits',
              'language': 'en-US',
              'speakers': 1,
              'label': 'Norman',
              'licence': 'public domain',
              'source': 'https://example.org',
              'placeholder': true,
            },
          },
        ],
      });
      expect(i.models.single.name, 'parakeet', reason: 'a voice is not a recogniser');
      expect(i.synthesize, isTrue);
      final v = i.voices.single;
      expect(v.unpack, 'tar.bz2');
      expect(v.files.single.size, 20987233);
      expect(v.voice!.family, 'vits');
      expect(v.voice!.language, 'en-US');
      expect(v.voice!.placeholder, isTrue);
      expect(v.voice!.licence, 'public domain');
    });

    test('an instance from before voices offers none', () {
      final i = SpeechModelInfo.fromJson({'enabled': true, 'name': 'parakeet'});
      expect(i.voices, isEmpty);
      expect(i.synthesize, isFalse);
      expect(VoiceOffer.of(i).voices, isEmpty);
    });
  });

  group('the spoken voice', () {
    test('reads and writes the shape of a covey voice’s speech field', () {
      for (final v in const [
        SpokenVoice.device('piper-en-norman', speaker: 2, rate: 1.2),
        SpokenVoice.server(model: 'kokoro', voice: 'af_heart'),
        SpokenVoice.system(),
      ]) {
        expect(SpokenVoice.fromJson(v.toJson()), v);
      }
      expect(SpokenVoice.fromJson({'source': 'device'}), isNull, reason: 'a device voice names its model');
      expect(SpokenVoice.fromJson({'source': 'cloud', 'model': 'x'}), isNull);
      expect(SpokenVoice.fromJson(null), isNull);
    });

    test('an agent gets the same own voice every time, among those of the language', () {
      final offered = [_voice('b-en', 'en-US', speakers: 3), _voice('a-de', 'de-DE'), _voice('c-de', 'de-DE')];
      final first = chooseOwnVoice(offered, 'de', 'agent-1');
      expect(first!.$1.voice!.language, 'de-DE');
      expect(chooseOwnVoice(offered.reversed.toList(), 'de-AT', 'agent-1'), first, reason: 'order does not matter');
      expect(chooseOwnVoice(offered, 'fr', 'agent-1'), isNull);
      // Every speaker of a model is a voice of its own.
      final picked = {for (var i = 0; i < 40; i++) chooseOwnVoice(offered, 'en', 'agent-$i')};
      expect(picked.map((p) => p!.$2).toSet(), {0, 1, 2});
    });

    test('nothing chosen speaks with the Mac’s voice', () {
      final plan = planVoice(
        chosen: null,
        offer: VoiceOffer(voices: [_voice('a-de', 'de-DE')], server: true),
        system: _system,
        language: 'de',
        agentId: 'agent-1',
      );
      expect(plan.onDevice || plan.onServer, isFalse);
      expect(plan.systemVoice?.id, 'de.anna');
    });

    test('a chosen device voice speaks its language; another language gets the agent’s own voice for it', () {
      final offer = VoiceOffer(voices: [_voice('a-de', 'de-DE', speakers: 2), _voice('b-en', 'en-US')]);
      const chosen = SpokenVoice.device('a-de', speaker: 5, rate: 1.1);
      final de = planVoice(chosen: chosen, offer: offer, system: _system, language: 'de-DE', agentId: 'x');
      expect(de.model!.name, 'a-de');
      expect(de.speaker, 1, reason: 'a speaker beyond the model is its last');
      expect(de.rate, 1.1);
      final en = planVoice(chosen: chosen, offer: offer, system: _system, language: 'en', agentId: 'x');
      expect(en.model!.name, 'b-en');
      final fr = planVoice(chosen: chosen, offer: offer, system: _system, language: 'fr', agentId: 'x');
      expect(fr.onDevice, isFalse, reason: 'no own voice for French: the Mac speaks');
    });

    test('the speech server speaks while the organisation has one, else the device does', () {
      const chosen = SpokenVoice.server(model: 'kokoro', voice: 'af_heart');
      final voices = [_voice('b-en', 'en-US')];
      final on = planVoice(
        chosen: chosen,
        offer: VoiceOffer(voices: voices, server: true),
        system: _system,
        language: 'en',
        agentId: 'x',
      );
      expect(on.onServer, isTrue);
      expect(on.server!.voice, 'af_heart');
      final off = planVoice(
        chosen: chosen,
        offer: VoiceOffer(voices: voices),
        system: _system,
        language: 'en',
        agentId: 'x',
      );
      expect(off.model?.name, 'b-en');
    });
  });

  group('audio', () {
    test('a WAV of 16-bit PCM is read as samples', () {
      final pcm = Uint8List(8);
      ByteData.sublistView(pcm)
        ..setInt16(0, 16384, Endian.little)
        ..setInt16(2, -32768, Endian.little);
      final wav = _wav(pcm, format: 1, bits: 16, rate: 24000);
      final out = decodeWav(wav)!;
      expect(out.sampleRate, 24000);
      expect(out.samples.length, 4);
      expect(out.samples[0], closeTo(0.5, 1e-6));
      expect(out.samples[1], -1);
      expect(decodeWav(Uint8List.fromList('not a wav file'.codeUnits)), isNull);
    });

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
  });

  group('the speech server', () {
    test('its MP3 is decoded as it streams in, and a WAV is read whole', () async {
      final out = _Output();
      final mp3 = ServerSynthesiser(
        (text, {model = '', voice = '', rate = 0, language = '', instructions = ''}) async {
          expect((voice, language, instructions), ('DEFAULT_VOICE', 'de', 'calm, concise'));
          return ('audio/mpeg', Stream<List<int>>.fromIterable([List.filled(100, 1), List.filled(50, 2)]));
        },
        out,
        voice: 'DEFAULT_VOICE',
        instructions: 'calm, concise',
      );
      final parts = await mp3.stream('Hallo.', language: 'de').toList();
      expect(parts.map((p) => p.samples.length), [100, 50]);
      expect(parts.first.sampleRate, 48000);

      final pcm = Uint8List(4);
      final wav = ServerSynthesiser(
        (text, {model = '', voice = '', rate = 0, language = '', instructions = ''}) async =>
            ('audio/wav', Stream<List<int>>.value(_wav(pcm, format: 1, bits: 16, rate: 24000))),
        out,
      );
      expect((await wav.synthesise('Hallo.')).sampleRate, 24000);

      final other = ServerSynthesiser(
        (text, {model = '', voice = '', rate = 0, language = '', instructions = ''}) async =>
            ('text/plain', const Stream<List<int>>.empty()),
        out,
      );
      expect(other.stream('x').toList(), throwsFormatException);
    });
  });

  group('the speaker', () {
    test('an own voice plays sentence by sentence and drives the mouth', () async {
      final out = _Output();
      final synth = _Synth();
      final s = _speaker(out, synth, chosen: const SpokenVoice.device('a-de'));
      await s.prepare(language: 'de');
      final done = s.speak('Der Export läuft. Er ist gleich fertig.', language: 'de');
      await _settle();
      expect(synth.said, ['Der Export läuft.', 'Er ist gleich fertig.']);
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

    test('a voice not on the device yet: the Mac speaks, and it is fetched meanwhile', () async {
      final out = _Output();
      final fetched = <bool>[];
      final s = _speaker(
        out,
        _Synth(),
        chosen: const SpokenVoice.device('a-de'),
        fetch: (m, {required wait}) async {
          fetched.add(wait);
          return null;
        },
      );
      await s.prepare(language: 'de');
      unawaited(s.speak('Hallo.', language: 'de'));
      await _settle();
      expect(out.said.single, ('Hallo.', 'de.anna'));
      expect(fetched, contains(false));
      s.dispose();
    });

    test('a speech server that fails: the device voice speaks instead, and from then on', () async {
      final out = _Output();
      final server = _Synth(fail: true);
      final device = _Synth();
      final s = _speaker(
        out,
        device,
        chosen: const SpokenVoice.server(model: 'kokoro'),
        server: server,
        offer: VoiceOffer(voices: [_voice('a-de', 'de-DE')], server: true),
      );
      await s.prepare(language: 'de');
      unawaited(s.speak('Eins. Zwei.', language: 'de'));
      await _settle();
      expect(server.said, ['Eins.'], reason: 'the server fails with the first piece');
      expect(device.said, ['Eins.', 'Zwei.']);
      out.emit(SpeakingEvent.finished);
      await _settle();
      unawaited(s.speak('Drei.', language: 'de'));
      await _settle();
      expect(server.said, ['Eins.'], reason: 'not asked again in this call');
      expect(device.said.last, 'Drei.');
      s.dispose();
    });

    test('stopping drops what is still being synthesised', () async {
      final out = _Output();
      final synth = _Synth(gate: Completer<void>());
      final s = _speaker(out, synth, chosen: const SpokenVoice.device('a-de'));
      await s.prepare(language: 'de');
      final done = s.speak('Eins. Zwei.', language: 'de');
      await _settle();
      await s.stop();
      await done;
      synth.gate!.complete();
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

AgentSpeaker _speaker(
  _Output out,
  _Synth device, {
  SpokenVoice? chosen,
  _Synth? server,
  VoiceOffer? offer,
  VoiceModelFetch? fetch,
}) => AgentSpeaker(
  output: out,
  agentId: 'agent-1',
  chosen: () async => chosen,
  offered: () async => offer ?? VoiceOffer(voices: [_voice('a-de', 'de-DE')]),
  fetch: fetch ?? (m, {required wait}) async => '/voices/${m.name}',
  server: server == null ? null : (voice) => server,
  loadSynth: (dir, family) async => device,
);

class _Synth extends Synthesiser {
  _Synth({this.fail = false, this.gate});
  final bool fail;
  final Completer<void>? gate;
  final said = <String>[];

  @override
  Future<Pcm> synthesise(String text, {int speaker = 0, double rate = 1}) async {
    said.add(text);
    await gate?.future;
    if (fail) throw Exception('unreachable');
    final s = Float32List(2205);
    for (var i = 0; i < s.length; i++) {
      s[i] = i.isEven ? 0.3 : -0.3;
    }
    return Pcm(s, 22050);
  }

  @override
  void close() {}
}

class _Output implements VoiceOutput {
  final _events = StreamController<(SpeakingEvent, int)>.broadcast();
  final said = <(String, String?)>[];
  final played = <(int, bool)>[];
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
    said.add((text, voiceId));
  }

  @override
  Future<void> stop() async => stops++;

  @override
  Future<bool> openDecoder(int id) async => true;

  @override
  Future<Pcm> decode(int id, Uint8List bytes) async => Pcm(Float32List(bytes.length), 48000);

  @override
  Future<void> closeDecoder(int id) async {}

  @override
  Future<List<SystemVoice>> voices() async => _system;

  @override
  void dispose() {}
}

Uint8List _wav(Uint8List data, {required int format, required int bits, required int rate}) {
  final h = ByteData(44);
  void tag(int at, String s) {
    for (var i = 0; i < 4; i++) {
      h.setUint8(at + i, s.codeUnitAt(i));
    }
  }

  tag(0, 'RIFF');
  h.setUint32(4, 36 + data.length, Endian.little);
  tag(8, 'WAVE');
  tag(12, 'fmt ');
  h.setUint32(16, 16, Endian.little);
  h.setUint16(20, format, Endian.little);
  h.setUint16(22, 1, Endian.little);
  h.setUint32(24, rate, Endian.little);
  h.setUint32(28, rate * bits ~/ 8, Endian.little);
  h.setUint16(32, bits ~/ 8, Endian.little);
  h.setUint16(34, bits, Endian.little);
  tag(36, 'data');
  h.setUint32(40, data.length, Endian.little);
  return Uint8List.fromList([...h.buffer.asUint8List(), ...data]);
}
