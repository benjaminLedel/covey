import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/call/call_settings.dart';
import 'package:covey_mobile/call/voice_choice.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

// The voice per agent on this Mac (#497): automatic, the Mac's voice, the
// organisation's speech server, or one of the instance's voices — each to
// be heard before it is picked.

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

late Strings _strings;
final _shot = GlobalKey();

class _Preview extends VoicePreview {
  _Preview() : super(null);
  final heard = <SpokenVoice?>[];

  @override
  Future<void> play(
    Agent agent,
    SpokenVoice? voice,
    SpeechModelInfo? model,
    String language, {
    bool server = false,
  }) async => heard.add(voice);

  @override
  Future<void> stop() async {}
}

final _agent = Agent(
  id: 'agent-1',
  slug: 'ada',
  displayName: 'Ada Lovelace',
  jobTitle: 'Support',
  status: 'idle',
  departmentId: null,
  killed: false,
);

const _norman = SpeechModelInfo(
  enabled: true,
  name: 'piper-en-norman',
  engine: 'tts',
  size: 20987233,
  voice: TtsVoiceInfo(family: 'vits', language: 'en-US', label: 'Norman', licence: 'public domain', placeholder: true),
);

void main() {
  setUpAll(() async {
    await _loadFonts();
    _strings = await Strings.load(const Locale('de'));
  });

  testWidgets('the picker lists the voices, plays each, and hands back the one picked', (tester) async {
    tester.view.physicalSize = const Size(900, 1300);
    tester.view.devicePixelRatio = 2;
    addTearDown(tester.view.reset);
    final preview = _Preview();
    SpokenVoice? picked;
    var returned = false;
    final strings = _strings;
    await tester.pumpWidget(
      MaterialApp(
        theme: coveyTheme(Brightness.light),
        builder: (context, child) => RepaintBoundary(
          key: _shot,
          child: StringsScope(strings: strings, child: child!),
        ),
        home: Builder(
          builder: (context) => Scaffold(
            body: Center(
              child: TextButton(
                onPressed: () async {
                  final r = await Navigator.of(context).push<VoicePick>(
                    MaterialPageRoute(
                      builder: (_) => VoicePickerScreen(
                        agent: _agent,
                        offer: const VoiceOffer(voices: [_norman], server: true),
                        current: const SpokenVoice.system(),
                        preview: preview,
                        language: 'de',
                      ),
                    ),
                  );
                  returned = r != null;
                  picked = r?.voice;
                },
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.text('Automatisch'), findsOneWidget);
    expect(find.text('Stimme des Macs'), findsOneWidget);
    expect(find.text('Sprachserver der Organisation'), findsOneWidget);
    expect(find.text('Norman · en-US'), findsOneWidget);
    expect(find.textContaining('Platzhalter · 21 MB · public domain'), findsOneWidget);

    final out = Platform.environment['CALL_PNG'];
    if (out != null) {
      await tester.runAsync(() async {
        final b = _shot.currentContext!.findRenderObject()! as RenderRepaintBoundary;
        final img = await b.toImage(pixelRatio: 2);
        final png = await img.toByteData(format: ui.ImageByteFormat.png);
        File('$out/call-voice-picker.png').writeAsBytesSync(png!.buffer.asUint8List());
      });
    }

    await tester.tap(find.byKey(const ValueKey('preview-piper-en-norman#0')));
    await tester.pump();
    expect(preview.heard, [const SpokenVoice.device('piper-en-norman')]);

    await tester.tap(find.text('Norman · en-US'));
    await tester.pumpAndSettle();
    expect(returned, isTrue);
    expect(picked, const SpokenVoice.device('piper-en-norman'));
  });
}
