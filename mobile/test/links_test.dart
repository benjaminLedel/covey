import 'dart:async';

import 'package:covey_mobile/main.dart';
import 'package:covey_mobile/profile.dart';
import 'package:covey_mobile/splash.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The Keychain without a platform.
class _MemoryProfiles extends ProfileStore {
  ({String instance, String key})? saved;

  @override
  Future<({String instance, String key})?> read() async => saved;

  @override
  Future<void> write(String instance, String key) async => saved = (instance: instance, key: key);

  @override
  Future<void> clear() async => saved = null;
}

void main() {
  testWidgets('a pairing link from outside is confirmed first, naming the host (#333)', (tester) async {
    final links = StreamController<Uri>();
    final profiles = _MemoryProfiles();
    await tester.runAsync(() async {
      await tester.pumpWidget(CoveyApp(profiles: profiles, links: links.stream));
      await Future<void>.delayed(const Duration(milliseconds: 200));
    });
    // Through the splash: the loading needs real time, the animation fake.
    for (var i = 0; i < 30 && find.byType(Splash).evaluate().isNotEmpty; i++) {
      await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
      await tester.pump(const Duration(milliseconds: 100));
    }
    await tester.pump(const Duration(milliseconds: 500));

    expect(find.byType(Splash), findsNothing);
    links.add(Uri.parse('https://evil.example/pair?code=coveypair_x'));
    for (var i = 0; i < 5; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }
    expect(find.textContaining('evil.example'), findsWidgets, reason: 'the dialog names where the app would connect');

    await tester.tap(find.byType(TextButton).last);
    await tester.pump(const Duration(milliseconds: 300));
    expect(profiles.saved, isNull, reason: 'cancelled — nothing is connected');
    await links.close();
  });
}
