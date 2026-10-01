import 'dart:async';
import 'dart:typed_data';

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/capture.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// The call's microphone (#507): the Mac's echo-cancelled stream first, the
// `record` package when it does not start, windows of exactly what Silero
// takes, and a shorter barge-in once the agent's own voice is cancelled.

class _Source implements CaptureSource {
  _Source(this.info, {this.fails});
  final CaptureInfo info;
  final Object? fails;
  int starts = 0, stops = 0;

  @override
  Future<OpenCapture> start() async {
    starts++;
    if (fails != null) throw fails!;
    return OpenCapture(const Stream.empty(), info);
  }

  @override
  Future<void> stop() async => stops++;

  @override
  Future<void> dispose() async {}
}

Future<void> _settle() => Future<void>.delayed(Duration.zero).then((_) => Future<void>.delayed(Duration.zero));

void main() {
  group('choosing the source', () {
    test('the first that starts is taken', () async {
      final native = _Source(const CaptureInfo(source: 'voice processing', echoCancelled: true));
      final record = _Source(const CaptureInfo(source: 'record'));
      final (i, open) = await openCapture([native, record]);
      expect(i, 0);
      expect(open.info.echoCancelled, isTrue);
      expect(open.info.note, isNull);
      expect(record.starts, 0);
    });

    test('a native start that fails falls back to record, and says why', () async {
      final native = _Source(const CaptureInfo(source: 'voice processing'), fails: StateError('no engine'));
      final record = _Source(const CaptureInfo(source: 'record'));
      final (i, open) = await openCapture([native, record]);
      expect(i, 1);
      expect(open.info.source, 'record');
      expect(open.info.echoCancelled, isFalse);
      expect(open.info.note, contains('no engine'));
      expect('${open.info}', contains('echo cancellation no'));
    });

    test('a later start begins at the source that ran', () async {
      final native = _Source(const CaptureInfo(source: 'voice processing'));
      final record = _Source(const CaptureInfo(source: 'record'));
      final (i, _) = await openCapture([native, record], from: 1);
      expect(i, 1);
      expect(native.starts, 0);
    });

    test('a denied microphone is not worked around', () async {
      final native = _Source(const CaptureInfo(source: 'voice processing'), fails: const CaptureDeniedException());
      final record = _Source(const CaptureInfo(source: 'record'));
      await expectLater(openCapture([native, record]), throwsA(isA<CaptureDeniedException>()));
      expect(record.starts, 0);
    });

    test('none starting throws the last failure', () async {
      final a = _Source(const CaptureInfo(source: 'a'), fails: StateError('a'));
      final b = _Source(const CaptureInfo(source: 'b'), fails: StateError('b'));
      await expectLater(openCapture([a, b]), throwsA(isA<StateError>().having((e) => e.message, 'message', 'b')));
    });

    test('the native info reads vp, devices and rates', () {
      final info = CaptureInfo.fromNative({
        'vp': true,
        'input': 'MacBook Pro Microphone',
        'output': 'MacBook Pro Speakers',
        'inputRate': 48000.0,
        'outputRate': 48000.0,
      });
      expect(info.echoCancelled, isTrue);
      expect(
        '$info',
        'microphone: voice processing, echo cancellation yes, input "MacBook Pro Microphone" 48000 Hz → 16000 Hz, '
            'output "MacBook Pro Speakers" 48000 Hz',
      );
      final failed = CaptureInfo.fromNative({'vp': false, 'vpError': 'refused'});
      expect(failed.echoCancelled, isFalse);
      expect('$failed', endsWith('(refused)'));
    });
  });

  group('windows of 512 samples', () {
    Float32List ramp(int from, int n) => Float32List.fromList([for (var i = 0; i < n; i++) (from + i).toDouble()]);

    test('chunks of any size come out as whole windows, in order, nothing lost', () {
      final c = FrameChunker(512);
      final out = <Float32List>[];
      var at = 0;
      for (final n in [341, 341, 342, 100, 1500, 7, 417]) {
        out.addAll(c.add(ramp(at, n)));
        at += n;
      }
      expect(out.every((w) => w.length == 512), isTrue);
      expect(out.length, at ~/ 512);
      final joined = [for (final w in out) ...w];
      expect(joined, [for (var i = 0; i < out.length * 512; i++) i.toDouble()]);
    });

    test('a chunk of exactly one window passes as it is; reset drops the rest', () {
      final c = FrameChunker(512);
      expect(c.add(ramp(0, 512)).single.length, 512);
      expect(c.add(ramp(0, 300)), isEmpty);
      c.reset();
      expect(c.add(ramp(0, 300)), isEmpty);
      expect(c.add(ramp(300, 212)).single.first, 0);
    });

    test('PCM16 from record reads as samples, an odd byte waiting for the next chunk', () async {
      final bytes = ByteData(6)
        ..setInt16(0, 16384, Endian.little)
        ..setInt16(2, -32768, Endian.little)
        ..setInt16(4, 0, Endian.little);
      final all = bytes.buffer.asUint8List();
      final chunks = [Uint8List.sublistView(all, 0, 3), Uint8List.sublistView(all, 3)];
      final samples = [for (final f in await pcm16Floats(Stream.fromIterable(chunks)).toList()) ...f];
      expect(samples, [0.5, -1.0, 0.0]);
      expect(floatsPcm16(Float32List.fromList(samples)), all);
    });
  });

  group('barge-in by echo cancellation', () {
    test('echo-cancelled, the confirmation is short; otherwise the tuned one holds', () {
      const tuned = Duration(milliseconds: 400);
      expect(bargeInConfirm(tuned, echoCancelled: false), tuned);
      expect(bargeInConfirm(tuned, echoCancelled: true), echoCancelledBargeIn);
      expect(echoCancelledBargeIn, lessThan(CallSettings.defaultBargeIn));
      // Tuned shorter still: that stays.
      const short = Duration(milliseconds: 64);
      expect(bargeInConfirm(short, echoCancelled: true), short);
    });

    Future<(FakeEars, FakeSpeaker, CallController)> speaking({required bool echoCancelled}) async {
      final ears = FakeEars()..echoCancelled = echoCancelled;
      final backend = FakeBackend(), speaker = FakeSpeaker();
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      ears.say('Erzähl mir vom Projekt.');
      await _settle();
      backend.agentSays('Das Projekt hat drei Teile.');
      await _settle();
      expect(speaker.speaking, isTrue);
      return (ears, speaker, call);
    }

    test('with echo cancellation a short phrase stops the agent', () async {
      final (ears, speaker, call) = await speaking(echoCancelled: true);
      ears.feed(160, voiced: true);
      expect(speaker.stops, 1);
      await call.hangUp();
    });

    test('without it the same phrase does not', () async {
      final (ears, speaker, call) = await speaking(echoCancelled: false);
      ears.feed(160, voiced: true);
      ears.feed(64, voiced: false);
      expect(speaker.stops, 0);
      await call.hangUp();
    });

    test('while the canceller settles, the tuned one holds (#511)', () async {
      const tuned = Duration(milliseconds: 400);
      expect(bargeInConfirm(tuned, echoCancelled: true, warmingUp: true), tuned);
      final (ears, speaker, call) = await speaking(echoCancelled: true);
      ears.echoCancelledSince = DateTime.now();
      ears.feed(160, voiced: true);
      ears.feed(64, voiced: false);
      expect(speaker.stops, 0, reason: 'its first 3 s it does not hold the agent back yet');
      ears.echoCancelledSince = DateTime.now().subtract(echoWarmUp + const Duration(milliseconds: 1));
      ears.feed(160, voiced: true);
      expect(speaker.stops, 1);
      await call.hangUp();
    });

    test('voice under what plays plus the margin is the agent, not the person (#511)', () async {
      expect(overPlayback(-20, -20), isFalse);
      expect(overPlayback(-14, -20), isTrue);
      expect(overPlayback(-50, null), isTrue, reason: 'nothing known to play: every window counts');
      expect(loudnessDb(0.6), closeTo(-24, 0.001));

      final (ears, speaker, call) = await speaking(echoCancelled: true);
      speaker.playbackDb = -20;
      ears.feed(640, voiced: true, level: 0.6); // −24 dB: its own voice
      expect(speaker.stops, 0);
      ears.feed(64, voiced: false);
      ears.feed(160, voiced: true, level: 0.8); // −12 dB: the person
      expect(speaker.stops, 1);
      await call.hangUp();
    });

    test('the typing playing is held to the same check', () async {
      final ears = FakeEars()..echoCancelled = true;
      final backend = FakeBackend(), speaker = FakeSpeaker(), clock = FakeClock();
      final call = fakeCall(ears, backend, speaker, sounds: FakeEarcons().sounds(), startTimer: clock.start);
      await call.start();
      ears.say('Was steht an?');
      await _settle();
      await clock.advance(const Duration(milliseconds: 850));
      expect(speaker.fillers, hasLength(1));
      speaker.playbackDb = -18;
      ears.feed(640, voiced: true, level: 0.6);
      expect(speaker.fillerStops, 0);
      ears.feed(64, voiced: false);
      ears.feed(160, voiced: true, level: 0.9);
      await _settle();
      expect(speaker.fillerStops, 1);
      await call.hangUp();
    });

    test('while the Mac\'s own voice speaks, which is not cancelled, the tuned one holds', () async {
      final (ears, speaker, call) = await speaking(echoCancelled: true);
      speaker.fallbackNotifier.value = true;
      ears.feed(160, voiced: true);
      ears.feed(64, voiced: false);
      expect(speaker.stops, 0);
      await call.hangUp();
    });
  });
}
