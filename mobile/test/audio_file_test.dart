import 'dart:convert';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/audio_file.dart';
import 'package:covey_mobile/dictation.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/screens/notes.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

/// A WAV file of 16 kHz mono PCM16: [parts] are (seconds, tone?) — a 440 Hz
/// tone where a voice would be, silence between.
Uint8List _wav(List<(double, bool)> parts) {
  final pcm = BytesBuilder();
  var n = 0;
  for (final (seconds, tone) in parts) {
    final samples = (seconds * 16000).round();
    final b = ByteData(samples * 2);
    for (var i = 0; i < samples; i++, n++) {
      final v = tone ? (0.3 * math.sin(2 * math.pi * 440 * n / 16000) * 32767).round() : 0;
      b.setInt16(i * 2, v, Endian.little);
    }
    pcm.add(b.buffer.asUint8List());
  }
  final data = pcm.takeBytes();
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
  h.setUint16(20, 1, Endian.little);
  h.setUint16(22, 1, Endian.little);
  h.setUint32(24, 16000, Endian.little);
  h.setUint32(28, 32000, Endian.little);
  h.setUint16(32, 2, Endian.little);
  h.setUint16(34, 16, Endian.little);
  tag(36, 'data');
  h.setUint32(40, data.length, Endian.little);
  return Uint8List.fromList([...h.buffer.asUint8List(), ...data]);
}

/// The platform decoder's stand-in: it reads a 16 kHz mono WAV, as the
/// native side hands out any file after conversion, in blocks of two
/// seconds.
class _WavDecoder implements AudioFileDecoder {
  _WavDecoder(this.wav);

  final Uint8List wav;
  _WavReader? reader;

  @override
  Future<AudioFileReader> open(String path) async {
    final h = ByteData.sublistView(wav);
    if (wav.length < 44 || String.fromCharCodes(wav.sublist(0, 4)) != 'RIFF') {
      throw AudioFileUnsupported('not a WAV file');
    }
    if (h.getUint32(24, Endian.little) != 16000 || h.getUint16(22, Endian.little) != 1) {
      throw AudioFileUnsupported('this stand-in reads 16 kHz mono only');
    }
    return reader = _WavReader(Uint8List.sublistView(wav, 44));
  }
}

class _WavReader implements AudioFileReader {
  _WavReader(this.pcm);

  final Uint8List pcm;
  int served = 0;
  bool closed = false;

  @override
  Duration get duration => Duration(milliseconds: pcm.length * 1000 ~/ 32000);

  @override
  Future<Uint8List?> read() async {
    if (closed || served >= pcm.length) return null;
    final end = math.min(served + 64000, pcm.length);
    final block = Uint8List.sublistView(pcm, served, end);
    served = end;
    return block;
  }

  @override
  Future<void> close() async => closed = true;
}

class _BrokenDecoder implements AudioFileDecoder {
  @override
  Future<AudioFileReader> open(String path) async => throw AudioFileUnsupported('Format not supported');
}

/// A recogniser without a model: each segment "says" its number, and every
/// segment has a voice of its own, so two segments are two speakers.
class _NumberedEngine extends SegmentedEngine {
  _NumberedEngine({this.decodeTime = Duration.zero});

  final Duration decodeTime;
  int _n = 0;
  int segmentBytes = 0;
  int fed = 0;
  final segmentSizes = <int>[];
  void Function()? onClose;

  @override
  String get kind => 'test';

  @override
  Float32List? get lastEmbedding => Float32List.fromList([for (var i = 0; i < 4; i++) i == _n % 4 ? 1 : 0]);

  @override
  Future<void> openSegment() async => segmentBytes = 0;

  @override
  void feedSegment(Uint8List chunk) {
    segmentBytes += chunk.length;
    fed += chunk.length;
  }

  @override
  Future<String> closeSegment() async {
    onClose?.call();
    segmentSizes.add(segmentBytes);
    if (decodeTime > Duration.zero) await Future<void>.delayed(decodeTime);
    _n++;
    return 'words $_n';
  }
}

/// [Dictation] around [_NumberedEngine]: no model to fetch, the file as
/// its source, paced as the real one is.
class _EngineDictation extends Dictation {
  _EngineDictation(this.eng, {required super.source, required super.onSegment}) : super(paced: true);

  final _NumberedEngine eng;
  bool _on = false;

  @override
  bool get running => _on;

  @override
  Future<bool> start({bool continuous = false}) async {
    eng
      ..source = source
      ..paced = paced
      ..onSegment = onSegment;
    await eng.start(modelPath: '', partials: (_) {});
    _on = true;
    notifyListeners();
    return true;
  }

  @override
  Future<String> stop() async {
    _on = false;
    return eng.stop();
  }
}

AudioFileRecognizer _recognizer(_NumberedEngine eng) =>
    (api, source, onSegment) => _EngineDictation(eng, source: source, onSegment: onSegment);

final _api = CoveyApi(Uri.parse('https://c.example'), 'k', client: MockClient((_) async => http.Response('', 404)));

/// Two voices with pauses: 1 s quiet, 2 s voice, 1.5 s quiet, 3 s voice,
/// 1 s quiet — 8.5 s.
final _twoVoices = _wav([(1, false), (2, true), (1.5, false), (3, true), (1, false)]);

void main() {
  test('a generated WAV runs through the segmenter: two segments, two speakers, all of it fed', () async {
    final decoder = _WavDecoder(_twoVoices);
    final eng = _NumberedEngine();
    final job = AudioFileTranscription(api: _api, path: 'interview.wav', decoder: decoder, recognizer: _recognizer(eng));
    final seen = <double>[];
    job.addListener(() {
      final p = job.progress;
      if (p != null) seen.add(p);
    });

    expect(await job.run(), isTrue);
    expect(job.segments, [(key: 's1', text: 'words 1'), (key: 's2', text: 'words 2')]);
    expect(job.duration, const Duration(milliseconds: 8500));
    expect(job.progress, 1.0);
    expect(seen, isNotEmpty);
    for (var i = 1; i < seen.length; i++) {
      expect(seen[i], greaterThanOrEqualTo(seen[i - 1]), reason: 'progress only grows');
    }
    expect(eng.fed, _twoVoices.length - 44, reason: 'every sample reached the recogniser');
    expect(decoder.reader!.closed, isTrue);
    expect(job.running, isFalse);
  });

  test('the file waits while a segment is recognised: memory stays bounded', () async {
    // 60 s of speech in 5 s turns: decoding is instant, recognition is not.
    final wav = _wav([for (var i = 0; i < 6; i++) ...[(4.0, true), (1.0, false)]]);
    final decoder = _WavDecoder(wav);
    final eng = _NumberedEngine(decodeTime: const Duration(milliseconds: 30));
    var ahead = 0;
    eng.onClose = () => ahead = math.max(ahead, decoder.reader!.served - eng.fed);
    final job = AudioFileTranscription(api: _api, path: 'long.wav', decoder: decoder, recognizer: _recognizer(eng));

    expect(await job.run(), isTrue);
    expect(job.segments, hasLength(6));
    // At most one block (two seconds) decoded beyond what the recogniser
    // took — not the rest of the file.
    expect(ahead, lessThanOrEqualTo(64000));
  });

  test('cancelling stops reading the file and closes it', () async {
    final wav = _wav([for (var i = 0; i < 40; i++) ...[(2.0, true), (1.0, false)]]);
    final decoder = _WavDecoder(wav);
    final eng = _NumberedEngine(decodeTime: const Duration(milliseconds: 5));
    final job = AudioFileTranscription(api: _api, path: 'long.wav', decoder: decoder, recognizer: _recognizer(eng));
    job.addListener(() {
      if ((job.progress ?? 0) > 0.1) job.cancel();
    });

    expect(await job.run(), isFalse);
    expect(job.cancelled, isTrue);
    expect(decoder.reader!.closed, isTrue);
    expect(decoder.reader!.served, lessThan(wav.length - 44), reason: 'the rest of the file is not read');
    expect(job.progress, lessThan(0.5));
  });

  test('a file the platform cannot read is refused before anything is recognised', () async {
    var made = false;
    final job = AudioFileTranscription(
      api: _api,
      path: 'notes.pdf',
      decoder: _BrokenDecoder(),
      recognizer: (api, source, onSegment) {
        made = true;
        return _EngineDictation(_NumberedEngine(), source: source, onSegment: onSegment);
      },
    );
    expect(await job.run(), isFalse);
    expect(job.unsupported, isNotNull);
    expect(job.detail, 'Format not supported');
    expect(made, isFalse);
  });

  group('the platform channel', () {
    TestWidgetsFlutterBinding.ensureInitialized();
    final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    tearDown(() => messenger.setMockMethodCallHandler(PlatformAudioFileDecoder.channel, null));

    test('opens, reads blocks until null, closes', () async {
      final calls = <String>[];
      var reads = 0;
      messenger.setMockMethodCallHandler(PlatformAudioFileDecoder.channel, (call) async {
        calls.add(call.method);
        switch (call.method) {
          case 'open':
            expect((call.arguments as Map)['path'], '/tmp/memo.m4a');
            return {'id': 7, 'durationMs': 4000};
          case 'read':
            expect((call.arguments as Map)['id'], 7);
            return reads++ < 2 ? Uint8List(64000) : null;
          default:
            return null;
        }
      });
      final r = await const PlatformAudioFileDecoder().open('/tmp/memo.m4a');
      expect(r.duration, const Duration(seconds: 4));
      expect((await r.read())!.length, 64000);
      expect((await r.read())!.length, 64000);
      expect(await r.read(), isNull);
      await r.close();
      await r.close();
      expect(calls, ['open', 'read', 'read', 'read', 'close']);
    });

    test('"unsupported" from the platform is a refusal, not a crash', () async {
      messenger.setMockMethodCallHandler(PlatformAudioFileDecoder.channel, (call) async {
        throw PlatformException(code: 'unsupported', message: 'The operation couldn’t be completed.');
      });
      expect(
        () => const PlatformAudioFileDecoder().open('/tmp/x.pdf'),
        throwsA(isA<AudioFileUnsupported>().having((e) => e.detail, 'detail', 'The operation couldn’t be completed.')),
      );
    });
  });

  group('the screen', () {
    Future<Widget> app(Widget home) async {
      await initializeDateFormatting();
      final strings = await Strings.load(const Locale('de'));
      return MaterialApp(
        theme: coveyTheme(Brightness.light),
        builder: (context, child) => StringsScope(strings: strings, child: child!),
        home: home,
      );
    }

    /// The pipeline's futures run on the real clock: a stream's cancel
    /// completes outside the test's fake time.
    Future<void> settle(WidgetTester tester) async {
      for (var i = 0; i < 20; i++) {
        await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 5)));
        await tester.pump(const Duration(milliseconds: 20));
      }
    }

    testWidgets('the transcript is saved as a meeting note, by speaker, titled after the file', (tester) async {
      Map<String, Object?>? posted;
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async {
          posted = jsonDecode(req.body) as Map<String, Object?>;
          return http.Response.bytes(
            utf8.encode(
              jsonEncode({
                'id': 'n9',
                'kind': 'meeting',
                'title': posted!['title'],
                'body': posted!['body'],
                'summary': '',
                'duration_seconds': posted!['duration_seconds'],
                'created_at': '2026-10-21T10:00:00Z',
              }),
            ),
            201,
            headers: {'content-type': 'application/json; charset=utf-8'},
          );
        }),
      );
      final job = AudioFileTranscription(
        api: api,
        path: 'interview.m4a',
        decoder: _WavDecoder(_twoVoices),
        recognizer: _recognizer(_NumberedEngine()),
      );
      await tester.pumpWidget(
        await tester.runAsync(
              () => app(AudioFileScreen(api: api, path: 'interview.m4a', name: 'interview.m4a', transcription: job)),
            )
            as Widget,
      );
      await settle(tester);

      // Two voices: the names are asked for, as after a meeting.
      expect(find.text('Sprecher 1'), findsWidgets);
      await tester.enterText(find.byType(TextField).first, 'Ada');
      await tester.tap(find.text('Speichern'));
      await settle(tester);

      expect(posted?['kind'], 'meeting');
      expect(posted?['title'], 'interview');
      expect(posted?['duration_seconds'], 8);
      expect(posted?['body'], '**Ada:** words 1\n\n**Sprecher 2:** words 2');
    });

    testWidgets('an unreadable file says so and saves nothing', (tester) async {
      var requests = 0;
      final api = CoveyApi(
        Uri.parse('https://c.example'),
        'k',
        client: MockClient((req) async {
          requests++;
          return http.Response('', 500);
        }),
      );
      final job = AudioFileTranscription(api: api, path: 'x.pdf', decoder: _BrokenDecoder());
      await tester.pumpWidget(
        await tester.runAsync(() => app(AudioFileScreen(api: api, path: 'x.pdf', name: 'x.pdf', transcription: job)))
            as Widget,
      );
      await settle(tester);
      expect(find.text('Diese Datei lässt sich auf diesem Gerät nicht als Audio lesen.'), findsOneWidget);
      expect(find.text('Fertig'), findsOneWidget);
      expect(requests, 0);
    });
  });
}
