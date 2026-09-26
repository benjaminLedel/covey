import 'package:covey_mobile/main.dart';
import 'package:covey_mobile/prefs.dart';
import 'package:covey_mobile/profile.dart';
import 'package:covey_mobile/screens/connect.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/splash.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

// Disconnecting (#405): the screen changes whatever the keychain says, and
// the next start honours it — a key the keychain would not let go of is no
// way back in.

/// A keychain that keeps what it was given and refuses to delete it, as
/// macOS does for an item an earlier, differently signed build wrote.
class _StubbornProfiles extends ProfileStore {
  ({String instance, String key})? saved = (instance: 'https://c.example', key: 'covey_alt');
  var refusals = 0;

  @override
  Future<({String instance, String key})?> read() async => saved;

  @override
  Future<void> write(String instance, String key) async => saved = (instance: instance, key: key);

  @override
  Future<void> clear() async {
    refusals++;
    throw Exception('errSecInvalidOwnerEdit');
  }
}

Future<void> _start(WidgetTester tester, ProfileStore profiles) async {
  await tester.runAsync(() async {
    await tester.pumpWidget(CoveyApp(key: UniqueKey(), profiles: profiles));
    await Future<void>.delayed(const Duration(milliseconds: 200));
  });
  // Through the splash: the loading needs real time, the animation fake.
  for (var i = 0; i < 30 && find.byType(Splash).evaluate().isNotEmpty; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await tester.pump(const Duration(milliseconds: 100));
  }
  await tester.pump(const Duration(milliseconds: 600));
}

void main() {
  setUp(() => Prefs.instance.inMemory({'tour.team': '1'}));

  testWidgets('a refused delete does not keep anybody signed in', (tester) async {
    final profiles = _StubbornProfiles();
    await _start(tester, profiles);
    expect(find.byType(HomeScreen), findsOneWidget);

    await tester.runAsync(() async => tester.widget<HomeScreen>(find.byType(HomeScreen)).onDisconnect());
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.byType(ConnectScreen), findsOneWidget, reason: 'the screen changes although the keychain refused');
    expect(profiles.refusals, 1);

    // The next start does not use the key that could not be deleted — and
    // tries to delete it again.
    await _start(tester, profiles);
    expect(find.byType(ConnectScreen), findsOneWidget);
    expect(find.byType(HomeScreen), findsNothing);
    expect(profiles.refusals, 2);

    // Connecting again ends it: the start after that is connected.
    await tester.runAsync(
      () async => tester
          .widget<ConnectScreen>(find.byType(ConnectScreen))
          .onConnected(Uri.parse('https://c.example'), 'covey_neu'),
    );
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.byType(HomeScreen), findsOneWidget);
    await _start(tester, profiles);
    expect(find.byType(HomeScreen), findsOneWidget);
  });
}
