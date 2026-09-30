import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/face.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

// The call button (#494): in the thread with an agent, on the Mac, while
// the trial is switched on — and not for a stopped agent.

late Strings _strings;

Future<void> _pump(WidgetTester tester, {FaceState state = FaceState.working}) async {
  final api = CoveyApi(
    Uri.parse('https://c.example'),
    'k',
    client: MockClient(
      (req) async => http.Response.bytes(
        utf8.encode(jsonEncode({'entries': [], 'pending': false})),
        200,
        headers: {'content-type': 'application/json; charset=utf-8'},
      ),
    ),
  );
  final me = Me(email: 'a@b.c', displayName: 'Ada', role: 'org_admin', teamSurface: true);
  await tester.pumpWidget(
    MaterialApp(
      theme: coveyTheme(Brightness.light),
      builder: (context, child) => StringsScope(strings: _strings, child: child!),
      home: ThreadScreen(api: api, agentId: 'a1', agentName: 'Bea', agentSlug: 'bea', faceState: state, me: me),
    ),
  );
  for (var i = 0; i < 4; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

void main() {
  setUpAll(() async => _strings = await Strings.load(const Locale('de')));
  tearDown(() {
    CallSettings.debugSupported = null;
    CallSettings.enabled.value = true;
  });

  testWidgets('the thread offers a call on the Mac while the trial is on', (tester) async {
    CallSettings.debugSupported = true;
    await _pump(tester);
    expect(find.byTooltip('Bea anrufen (Test) · ⌘⇧C'), findsOneWidget);

    CallSettings.enabled.value = false;
    await tester.pump();
    expect(find.byKey(const ValueKey('call')), findsNothing, reason: 'switched off in the settings');
  });

  testWidgets('no call elsewhere, nor to a stopped agent', (tester) async {
    CallSettings.debugSupported = false;
    await _pump(tester);
    expect(find.byKey(const ValueKey('call')), findsNothing);

    CallSettings.debugSupported = true;
    await _pump(tester, state: FaceState.killed);
    expect(find.byKey(const ValueKey('call')), findsNothing);
  });
}
