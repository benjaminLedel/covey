import 'dart:async';
import 'dart:typed_data';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/call/recognition.dart';
import 'package:covey_mobile/call/wav.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// Recognising a call's turn at the organisation's voice provider (#516): the
// device's recogniser runs beside it, and its text is taken when the
// provider fails, answers nothing, or takes longer than the bound.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

Future<String> _after(Duration d, String text) => Future.delayed(d, () => text);

void main() {
  group('recogniseTurn', () {
    test('without the server, the device', () async {
      final t = await recogniseTurn(device: () async => ' Frag doch mal bei ihr nach ');
      expect(t.text, 'Frag doch mal bei ihr nach');
      expect(t.by, Recogniser.device);
      expect(t.serverProblem, isNull);
    });

    test('the server\'s text when it arrives in time, whatever the device heard', () async {
      final t = await recogniseTurn(
        device: () async => 'Das wird mal bei ihr nachgefragt',
        server: () => _after(const Duration(milliseconds: 20), 'Frag doch mal bei ihr nach.'),
        bound: const Duration(seconds: 1),
      );
      expect(t.text, 'Frag doch mal bei ihr nach.');
      expect(t.by, Recogniser.server);
      expect(t.elapsed, lessThan(const Duration(seconds: 1)));
    });

    test('too slow: the device\'s text at the bound, not later', () async {
      final watch = Stopwatch()..start();
      final t = await recogniseTurn(
        device: () async => 'Tschüss',
        server: () => _after(const Duration(seconds: 2), 'Tschüss.'),
        bound: const Duration(milliseconds: 60),
      );
      expect(t.text, 'Tschüss');
      expect(t.by, Recogniser.device);
      expect(t.serverProblem, 'timeout');
      expect(watch.elapsed, lessThan(const Duration(seconds: 1)));
    });

    test('a failure or nothing heard: the device\'s text', () async {
      final failed = await recogniseTurn(
        device: () async => 'Just one.',
        server: () async => throw ApiException(502, 'the voice provider did not answer'),
      );
      expect((failed.text, failed.by), ('Just one.', Recogniser.device));
      expect(failed.serverProblem, contains('502'));
      final empty = await recogniseTurn(device: () async => 'Hallo', server: () async => '  ');
      expect((empty.text, empty.by, empty.serverProblem), ('Hallo', Recogniser.device, 'empty'));
    });

    test('both run at once: the fallback does not wait for the server first', () async {
      final device = Completer<String>(), server = Completer<String>();
      var deviceAsked = false;
      final t = recogniseTurn(
        device: () {
          deviceAsked = true;
          return device.future;
        },
        server: () => server.future,
        bound: const Duration(seconds: 5),
      );
      await _settle();
      expect(deviceAsked, isTrue, reason: 'the device decodes while the server is asked');
      server.completeError(ApiException(0, 'offline'));
      device.complete('Bis später');
      expect((await t).text, 'Bis später');
    });

    test('the device failing while the server is taken is no error', () async {
      final t = await recogniseTurn(
        device: () async => throw StateError('decoder closed'),
        server: () async => 'Danke, das war\'s.',
      );
      expect(t.by, Recogniser.server);
      await _settle();
    });
  });

  test('a turn\'s PCM goes as a 16 kHz mono WAV, the samples as they are', () {
    final pcm = Uint8List.fromList(List.generate(640, (i) => i % 256));
    final wav = wavOfPcm16(pcm);
    expect(wav.length, 44 + 640);
    expect(String.fromCharCodes(wav.sublist(0, 4)), 'RIFF');
    expect(String.fromCharCodes(wav.sublist(8, 12)), 'WAVE');
    final b = ByteData.sublistView(wav);
    expect(b.getUint16(22, Endian.little), 1, reason: 'mono');
    expect(b.getUint32(24, Endian.little), 16000);
    expect(b.getUint16(34, Endian.little), 16);
    expect(wav.sublist(44), pcm);
    final back = decodeWav(wav);
    expect((back.sampleRate, back.samples.length), (16000, 320));
  });

  group('in a call', () {
    test('the organisation allows it: the server\'s text is sent, in the call\'s language', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      backend
        ..serverRecognises = true
        ..serverHeard.add('Frag doch mal bei ihr nach.');
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      expect(call.serverRecognition, isTrue);
      ears.say('Das wird mal bei ihr nachgefragt');
      await _settle();
      expect(backend.posted, ['Frag doch mal bei ihr nach.']);
      final (wav, language) = backend.transcribed.single;
      expect(language, 'de');
      expect(String.fromCharCodes(wav.sublist(0, 4)), 'RIFF');
      expect(wav.length - 44, greaterThan(0));
      await call.hangUp();
    });

    test('off: the device alone, nothing leaves it', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      expect(call.serverRecognition, isFalse);
      ears.say('Wie weit ist der Export?');
      await _settle();
      expect(backend.posted, ['Wie weit ist der Export?']);
      expect(backend.transcribed, isEmpty);
      await call.hangUp();
    });

    test('too slow: the device\'s text is sent', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      final slow = Completer<String>();
      backend
        ..serverRecognises = true
        ..serverHeard.add(slow);
      final call = fakeCall(ears, backend, speaker, serverBound: const Duration(milliseconds: 40));
      await call.start();
      ears.say('Wie weit ist der Export?');
      await Future<void>.delayed(const Duration(milliseconds: 120));
      await _settle();
      expect(backend.posted, ['Wie weit ist der Export?']);
      slow.complete('Zu spät.');
      await _settle();
      expect(backend.posted, hasLength(1), reason: 'a late answer is dropped');
      expect(call.serverRecognition, isTrue, reason: 'a slow turn is no reason to stop asking');
      await call.hangUp();
    });

    test('refused (409): the device\'s text, and the server is not asked again this call', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      backend
        ..serverRecognises = true
        ..serverHeard.add(ApiException(409, 'recognition at the voice provider is off for this organisation'));
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      ears.say('Wie weit ist der Export?');
      await _settle();
      expect(backend.posted, ['Wie weit ist der Export?']);
      expect(call.serverRecognition, isFalse);
      speaker.finish();
      ears.say('Und die Rechnung?');
      await _settle();
      expect(backend.transcribed, hasLength(1));
      expect(backend.posted.last, 'Und die Rechnung?');
      await call.hangUp();
    });
  });
}
