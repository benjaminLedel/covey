import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/call_view.dart';
import 'package:covey_mobile/call/ears.dart';
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
    ears.feed(736, voiced: false);
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
}

class _Denied extends FakeEars {
  @override
  Future<void> prepare() async => throw CallException(CallProblem.denied);
}
