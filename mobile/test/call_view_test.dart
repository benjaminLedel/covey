import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/call_view.dart';
import 'package:covey_mobile/call/ears.dart';
import 'package:covey_mobile/call/greeting.dart';
import 'package:covey_mobile/call/recording.dart';
import 'package:covey_mobile/face.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'call_fakes.dart';

// The call view (#494): the face large in the middle, what it is doing in
// words under it, the last lines, mute and hang up.

Future<void> _loadFonts() async {
  final manifest = jsonDecode(await rootBundle.loadString('FontManifest.json')) as List;
  for (final f in manifest.cast<Map<String, dynamic>>()) {
    final loader = FontLoader(f['family'] as String);
    for (final a in (f['fonts'] as List).cast<Map<String, dynamic>>()) {
      loader.addFont(rootBundle.load(a['asset'] as String));
    }
    await loader.load();
  }
}

final _shot = GlobalKey();

/// Loaded once: the asset bundle caches what it read in the zone of the
/// test that read it first, and a later test's fake clock never runs it.
late Strings _strings;

Future<void> _pump(WidgetTester tester, CallController call, {Brightness brightness = Brightness.light}) async {
  final strings = _strings;
  await tester.pumpWidget(
    MaterialApp(
      theme: coveyTheme(brightness),
      builder: (context, child) => StringsScope(strings: strings, child: child!),
      home: RepaintBoundary(
        key: _shot,
        child: CallScreen(api: null, agentId: 'agent-1', agentName: 'Ada Lovelace', agentSlug: 'ada', controller: call),
      ),
    ),
  );
  await tester.pump(const Duration(milliseconds: 50));
}

/// `CALL_PNG=<dir>` keeps a picture of each state, to look at.
Future<void> _png(WidgetTester tester, String name) async {
  final out = Platform.environment['CALL_PNG'];
  if (out == null) return;
  await tester.runAsync(() async {
    final b = _shot.currentContext!.findRenderObject()! as RenderRepaintBoundary;
    final img = await b.toImage(pixelRatio: 2);
    final png = await img.toByteData(format: ui.ImageByteFormat.png);
    File('$out/call-$name.png').writeAsBytesSync(png!.buffer.asUint8List());
  });
}

String _state(WidgetTester tester) => tester.widget<Text>(find.byKey(const ValueKey('call-state'))).data!;

void main() {
  setUpAll(() async {
    await _loadFonts();
    _strings = await Strings.load(const Locale('de'));
  });

  testWidgets('each state shows in the face and in words', (tester) async {
    tester.view.physicalSize = const Size(1000, 1400);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    final ears = FakeEars()..gate = Completer<void>();
    final backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    unawaited(call.start());
    await _pump(tester, call);

    expect(_state(tester), 'Anruf wird vorbereitet…');
    expect(find.text('Ada Lovelace'), findsOneWidget);
    expect(find.text('Stumm'), findsOneWidget);
    expect(find.text('Auflegen'), findsOneWidget);
    await _png(tester, 'preparing');

    ears.gate!.complete();
    await tester.pump(const Duration(milliseconds: 50));
    expect(_state(tester), 'Hört zu');
    expect(tester.widget<Face>(find.byType(Face)).talk, FaceTalk.listening);
    await _png(tester, 'listening');

    ears.feed(320, voiced: true, level: 0.9);
    await tester.pump(const Duration(milliseconds: 50));
    expect(_state(tester), 'Hört dich');
    expect(tester.widget<Face>(find.byType(Face)).level, greaterThan(0.5));
    await _png(tester, 'hearing');

    ears.heard.add('Wie weit ist der Export für Initech?');
    ears.feed(1056, voiced: false);
    await tester.pump(const Duration(milliseconds: 50));
    expect(_state(tester), 'Denkt nach…');
    expect(tester.widget<Face>(find.byType(Face)).talk, FaceTalk.thinking);
    expect(find.textContaining('Wie weit ist der Export'), findsOneWidget);
    await _png(tester, 'thinking');

    backend.agentSays('Der Export läuft noch, in zehn Minuten ist er fertig.');
    await tester.pump(const Duration(milliseconds: 50));
    speaker.word();
    await tester.pump(const Duration(milliseconds: 40));
    expect(_state(tester), 'Spricht');
    final face = tester.widget<Face>(find.byType(Face));
    expect(face.talk, FaceTalk.speaking);
    expect(face.mouth, greaterThan(0.3), reason: 'the mouth opens on a word');
    await _png(tester, 'speaking');

    speaker.finish();
    await tester.pump(const Duration(milliseconds: 50));
    await tester.tap(find.byTooltip('Stumm'));
    await tester.pump(const Duration(milliseconds: 50));
    expect(_state(tester), 'Stummgeschaltet');
    expect(find.text('Laut'), findsOneWidget);
    expect(ears.listening, isFalse);
    await _png(tester, 'muted');

    await tester.tap(find.byTooltip('Auflegen'));
    await tester.pump(const Duration(milliseconds: 50));
    expect(call.ended, isTrue);
    expect(ears.closed, isTrue);
  });

  testWidgets('a call that cannot start says why; dark, still under reduced motion', (tester) async {
    tester.view.physicalSize = const Size(1000, 1400);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    final ears = _Denied();
    final call = fakeCall(ears, FakeBackend(), FakeSpeaker());
    final strings = _strings;
    await tester.pumpWidget(
      MaterialApp(
        theme: coveyTheme(Brightness.dark),
        builder: (context, child) => StringsScope(
          strings: strings,
          child: MediaQuery(data: MediaQuery.of(context).copyWith(disableAnimations: true), child: child!),
        ),
        home: RepaintBoundary(
          key: _shot,
          child: CallScreen(
            api: null,
            agentId: 'agent-1',
            agentName: 'Ada Lovelace',
            agentSlug: 'ada',
            controller: call,
          ),
        ),
      ),
    );
    await call.start();
    await tester.pump(const Duration(milliseconds: 50));
    expect(_state(tester), startsWith('Das Mikrofon ist nicht erlaubt.'));
    await _png(tester, 'failed-dark');
    await tester.tap(find.byTooltip('Auflegen'));
    await tester.pump(const Duration(milliseconds: 50));
  });

  testWidgets('Escape hangs up', (tester) async {
    final ears = FakeEars();
    final call = fakeCall(ears, FakeBackend(), FakeSpeaker());
    unawaited(call.start());
    await _pump(tester, call);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pump(const Duration(milliseconds: 50));
    expect(call.ended, isTrue);
  });

  testWidgets('the greeting is the agent\'s line, the face speaking it (#506)', (tester) async {
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker()..ready.complete(true);
    final call = fakeCall(
      ears,
      backend,
      speaker,
      greeter: CallGreeter(enabled: () => true, memory: _NoMemory(), now: () => DateTime(2026, 9, 30, 9)),
    );
    unawaited(call.start());
    await _pump(tester, call);
    await tester.pump(const Duration(milliseconds: 50));
    final said = speaker.spokenPrepared.single.$1;
    expect(said, contains('Grace'));
    expect(_state(tester), 'Spricht');
    expect(tester.widget<Face>(find.byType(Face)).talk, FaceTalk.speaking);
    expect(find.textContaining(said), findsOneWidget);
    await _png(tester, 'greeting');
    speaker.finish();
    await tester.pump(const Duration(milliseconds: 50));
    expect(_state(tester), 'Hört zu');
    expect(backend.posted, isEmpty);
    await tester.runAsync(call.hangUp);
    await tester.pump(const Duration(milliseconds: 50));
  });

  testWidgets('the Mac\'s voice standing in for the voice provider is said in a line (#497)', (tester) async {
    tester.view.physicalSize = const Size(1000, 1400);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await _pump(tester, call);
    await tester.runAsync(call.start);
    await tester.pump(const Duration(milliseconds: 50));
    final line = find.byKey(const ValueKey('call-provider-unavailable'));
    expect(line, findsNothing, reason: 'the provider speaks');
    speaker.fallbackNotifier.value = true;
    await tester.pump(const Duration(milliseconds: 50));
    expect(line, findsOneWidget);
    expect(tester.widget<Text>(line).data, 'Stimmen-Anbieter nicht erreichbar — es spricht die Stimme des Macs');
    await _png(tester, 'provider-unavailable');
    await tester.runAsync(call.hangUp);
    await tester.pump(const Duration(milliseconds: 50));
  });

  testWidgets('recognition at the organisation\'s server is said in the line at the top (#516)', (tester) async {
    tester.view.physicalSize = const Size(1000, 1400);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    final ears = FakeEars(), backend = FakeBackend()..serverRecognises = true, speaker = FakeSpeaker();
    final call = fakeCall(ears, backend, speaker);
    await _pump(tester, call);
    await tester.runAsync(call.start);
    await tester.pump(const Duration(milliseconds: 50));
    final line = find.byKey(const ValueKey('call-on-server'));
    expect(line, findsOneWidget);
    expect(tester.widget<Text>(line).data, 'Anruf · Test · Sprache wird auf dem Server deiner Organisation erkannt');
    expect(find.byKey(const ValueKey('call-on-device')), findsNothing);
    await _png(tester, 'on-server');
    await tester.runAsync(call.hangUp);
    await tester.pump(const Duration(milliseconds: 50));
  });

  testWidgets('what was understood stands before it is sent: Enter sends, Esc discards, typing corrects (#498)', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1000, 1400);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    final root = (await tester.runAsync(() => Directory.systemTemp.createTemp('call-audio')))!;
    addTearDown(() => root.deleteSync(recursive: true));
    final ears = FakeEars(), backend = FakeBackend(), speaker = FakeSpeaker();
    backend.cleanAs = (t) => t == 'wie weit ist der export für initech' ? 'Wie weit ist der Export für Initech?' : null;
    final call = CallController(
      backend: backend,
      ears: ears,
      speaker: speaker,
      agentId: 'agent-1',
      agentName: 'Ada Lovelace',
      appLanguage: 'de',
      words: (k) => k,
      tuning: const CallTuning(window: Duration(seconds: 30), record: true),
      recording: () => CallRecording.open(root: root),
    );
    await _pump(tester, call);
    await tester.runAsync(call.start);
    await tester.pump(const Duration(milliseconds: 50));
    expect(find.byKey(const ValueKey('call-recording')), findsOneWidget, reason: 'recording is said while it is on');

    ears.say('wie weit ist der export für initech');
    await tester.pump(const Duration(milliseconds: 50));
    await tester.pump(const Duration(milliseconds: 50));
    expect(_state(tester), 'Verstanden');
    expect(find.byKey(const ValueKey('call-understood')), findsOneWidget);
    expect(find.text('Wie weit ist der Export für Initech?'), findsOneWidget, reason: 'the cleaned text is shown');
    expect(backend.posted, isEmpty);
    await tester.pump(const Duration(milliseconds: 600));
    await _png(tester, 'understood');
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pump(const Duration(milliseconds: 50));
    expect(backend.posted, ['Wie weit ist der Export für Initech?']);
    expect(find.byKey(const ValueKey('call-understood')), findsNothing);

    ears.say('lösch den bericht');
    await tester.pump(const Duration(milliseconds: 50));
    await tester.pump(const Duration(milliseconds: 50));
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pump(const Duration(milliseconds: 50));
    expect(backend.posted, hasLength(1), reason: 'discarded, not sent');
    expect(call.ended, isFalse, reason: 'Esc discards the turn; it does not hang up');

    ears.say('schick ihn an greis');
    await tester.pump(const Duration(milliseconds: 50));
    await tester.pump(const Duration(milliseconds: 50));
    await tester.sendKeyEvent(LogicalKeyboardKey.period, character: '.');
    await tester.pump(const Duration(milliseconds: 50));
    final field = find.byKey(const ValueKey('call-understood-field'));
    expect(field, findsOneWidget);
    expect(tester.widget<TextField>(field).controller!.text, 'schick ihn an greis.');
    await tester.enterText(field, 'Schick ihn an Grace.');
    await tester.pump(const Duration(milliseconds: 50));
    await _png(tester, 'understood-editing');
    await tester.testTextInput.receiveAction(TextInputAction.send);
    await tester.pump(const Duration(milliseconds: 50));
    expect(backend.posted.last, 'Schick ihn an Grace.');
    await tester.runAsync(call.hangUp);
    await tester.pump(const Duration(milliseconds: 50));
  });
}

class _Denied extends FakeEars {
  @override
  Future<void> prepare() async => throw CallException(CallProblem.denied);
}

class _NoMemory implements GreetingMemory {
  @override
  Future<String?> last(String agentId) async => null;

  @override
  Future<void> remember(String agentId, String key) async {}
}
