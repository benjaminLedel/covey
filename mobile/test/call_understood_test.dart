import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/recording.dart';
import 'package:covey_mobile/call/understood.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// Recognition in a call (#498): a turn stands as understood for a moment,
// cleaned up with the conversation as context, before it is sent — and the
// person can send it at once, correct it or discard it. Each turn can be
// recorded for diagnostics.

Future<void> _settle() async {
  for (var i = 0; i < 10; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  group('the understood window', () {
    test('it starts once the clean-up is back, and sends the cleaned text', () async {
      final u = UnderstoodTurn('wie weit ist der export', window: const Duration(milliseconds: 40), cleaning: true);
      await Future<void>.delayed(const Duration(milliseconds: 80));
      expect(u.done, isFalse, reason: 'the clock runs only after the clean-up');
      u.cleanedUp('Wie weit ist der Export?');
      expect(u.text, 'Wie weit ist der Export?');
      expect(u.sendsAt, isNotNull);
      expect(await u.result, ('Wie weit ist der Export?', UnderstoodOutcome.sent));
    });

    test('a failed clean-up shows and sends what was recognised', () async {
      final u = UnderstoodTurn('hallo ada', window: Duration.zero, cleaning: true);
      u.cleanedUp(null);
      expect(await u.result, ('hallo ada', UnderstoodOutcome.sent));
      expect(u.cleaned, isNull);
    });

    test('Enter sends what is shown now; Esc discards it', () async {
      final a = UnderstoodTurn('eins', window: const Duration(seconds: 10), cleaning: true);
      a.send();
      expect(await a.result, ('eins', UnderstoodOutcome.sentNow));
      a.cleanedUp('Eins.');
      expect(a.text, 'eins', reason: 'what was sent is what was shown');

      final b = UnderstoodTurn('zwei', window: const Duration(seconds: 10));
      b.discard();
      expect(await b.result, (null, UnderstoodOutcome.discarded));
    });

    test('typing stops the clock; the correction is sent, an emptied one discarded', () async {
      final u = UnderstoodTurn('der export von inetech', window: const Duration(milliseconds: 30));
      u.edit();
      await Future<void>.delayed(const Duration(milliseconds: 60));
      expect(u.done, isFalse);
      u.cleanedUp('Der Export von Initech');
      expect(u.text, 'der export von inetech', reason: 'a clean-up does not overwrite a correction');
      u.send('Der Export von Initech?');
      expect(await u.result, ('Der Export von Initech?', UnderstoodOutcome.edited));

      final e = UnderstoodTurn('x', window: const Duration(seconds: 5))..edit();
      e.send('  ');
      expect(await e.result, (null, UnderstoodOutcome.discarded));
    });
  });

  group('how much a clean-up changed', () {
    test('a correction is little, a rewrite much', () {
      expect(
        turnEdit('hat gertrut den merge request schon angeschaut', 'Hat Gertrud den Merge Request schon angeschaut?'),
        lessThan(0.1),
      );
      expect(turnEdit('äh kannst du mal schauen', 'Kannst du mal schauen?'), lessThanOrEqualTo(0.2));
      expect(turnEdit('Hallo', 'Ja.'), greaterThan(maxTurnEdit));
      expect(
        turnEdit('kannst du mal nach den pipelines schauen', 'Ja, die Pipelines laufen alle.'),
        greaterThan(maxTurnEdit),
      );
      expect(turnWords('Hallo, Ada!  Wie geht’s?'), ['hallo', 'ada', 'wie', 'geht', 's']);
    });
  });

  group('a call', () {
    test('a turn is cleaned up with the conversation as context, then posted', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      backend.agentSays('Der Bericht für Initech ist fertig.');
      backend.cleanAs = (t) => 'Schick ihn an Grace.';
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      await _settle();
      ears.say('schick ihn an greis');
      await _settle();
      expect(backend.posted, ['Schick ihn an Grace.']);
      final (raw, ctx) = backend.cleaned.single;
      expect(raw, 'schick ihn an greis');
      final fields = ctx.toFields();
      expect(fields['window'], contains('Ada Lovelace'));
      expect(fields['field'], contains('Grace'));
      expect(fields['before'], contains('Initech'));
      await call.hangUp();
    });

    test('without a clean-up the recognised text goes as it is', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      backend.cleanAs = (t) => ApiException(404, 'not found');
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      ears.say('wie weit ist der export');
      await _settle();
      expect(backend.posted, ['wie weit ist der export']);
      await call.hangUp();
    });

    test('a turn under four words is sent as recognised, not cleaned (#511)', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      backend.cleanAs = (t) => 'Ja.';
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      ears.say('Hallo');
      await _settle();
      expect(backend.cleaned, isEmpty, reason: 'the clean-up is not asked');
      expect(backend.posted, ['Hallo']);
      await call.hangUp();
    });

    test('a clean-up that rewrote the turn is dropped for what was recognised (#511)', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      backend.cleanAs = (t) => 'Ja, die Pipelines laufen alle.';
      final call = fakeCall(ears, backend, speaker);
      await call.start();
      ears.say('kannst du mal nach den pipelines schauen');
      await _settle();
      expect(backend.cleaned, hasLength(1));
      expect(backend.posted, ['kannst du mal nach den pipelines schauen']);
      await call.hangUp();
    });

    test('while it stands as understood nothing is posted; discarded, nothing is', () async {
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      final call = fakeCall(ears, backend, speaker, tuning: const CallTuning(window: Duration(seconds: 30)));
      await call.start();
      ears.say('lösch alles');
      await _settle();
      expect(call.mode, CallMode.understood);
      expect(call.understood!.text, 'lösch alles');
      expect(backend.posted, isEmpty);
      call.understood!.discard();
      await _settle();
      expect(backend.posted, isEmpty);
      expect(call.understood, isNull);
      expect(call.mode, CallMode.listening);

      ears.say('mach weiter');
      await _settle();
      call.understood!.send();
      await _settle();
      expect(backend.posted, ['mach weiter']);
      await call.hangUp();
    });

    test('a recorded turn keeps its audio and a line of what was measured and understood', () async {
      final root = await Directory.systemTemp.createTemp('call-audio');
      addTearDown(() => root.delete(recursive: true));
      final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
      backend.cleanAs = (t) => 'Hallo Ada, wie geht es?';
      final call = CallController(
        backend: backend,
        ears: ears,
        speaker: speaker,
        agentId: 'agent-1',
        agentName: 'Ada Lovelace',
        appLanguage: 'de',
        words: (k) => k,
        tuning: const CallTuning(window: Duration.zero, record: true),
        recording: () => CallRecording.open(root: root),
      );
      await call.start();
      expect(call.recordingTurns, isTrue);
      ears.say('hallo ada wie geht es');
      await _settle();
      await Future<void>.delayed(const Duration(milliseconds: 50));
      final wavs = root.listSync().whereType<File>().where((f) => f.path.endsWith('.wav')).toList();
      expect(wavs, hasLength(1));
      expect(String.fromCharCodes(wavs.single.readAsBytesSync().sublist(0, 4)), 'RIFF');
      final line = jsonDecode(File('${root.path}/turns.jsonl').readAsLinesSync().single) as Map<String, dynamic>;
      expect(line['raw'], 'hallo ada wie geht es');
      expect(line['cleaned'], 'Hallo Ada, wie geht es?');
      expect(line['clean_edit'], 0);
      expect(line['cut'], 'pause');
      expect(line['outcome'], 'sent');
      expect(line['thresholds'], containsPair('pause_ms', 700));
      expect((line['vad'] as Map)['voiced'], greaterThan(0));
      await call.hangUp();
    });
  });

  group('the recording', () {
    test('turns older than a week go, with their lines', () async {
      final root = await Directory.systemTemp.createTemp('call-audio');
      addTearDown(() => root.delete(recursive: true));
      final r = await CallRecording.open(root: root);
      await r.keep('old', Uint8List(64), {'turn': 1});
      await r.keep('new', Uint8List(64), {'turn': 2});
      final old = File('${root.path}/old.wav');
      old.setLastModifiedSync(DateTime.now().subtract(const Duration(days: 8)));
      await r.prune();
      expect(old.existsSync(), isFalse);
      expect(File('${root.path}/new.wav').existsSync(), isTrue);
      final lines = File('${root.path}/turns.jsonl').readAsLinesSync();
      expect(lines.map((l) => (jsonDecode(l) as Map)['file']), ['new.wav']);
      expect((await CallRecording.size(root: root)).$2, 1);
      await CallRecording.deleteAll(root: root);
      expect(root.existsSync(), isFalse);
      await root.create();
    });

    test('a WAV header says 16 kHz mono, 16 bits', () {
      final w = wavBytes(Uint8List(320));
      final d = ByteData.sublistView(w);
      expect(w.length, 44 + 320);
      expect(d.getUint32(24, Endian.little), 16000);
      expect(d.getUint16(22, Endian.little), 1);
      expect(d.getUint16(34, Endian.little), 16);
      expect(d.getUint32(40, Endian.little), 320);
    });
  });
}
