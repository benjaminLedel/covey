import 'dart:async';

import 'package:covey_mobile/splash.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('the splash waits for the animation and for the app, whichever is later', (tester) async {
    final ready = Completer<void>();
    var done = false;
    await tester.pumpWidget(MaterialApp(home: Splash(ready: ready.future, onDone: () => done = true)));

    await tester.pump(Splash.duration + const Duration(milliseconds: 100));
    expect(done, isFalse, reason: 'the app has not loaded yet');

    ready.complete();
    await tester.pump();
    expect(done, isTrue);
  });

  testWidgets('a fast app still sees the animation through', (tester) async {
    var done = false;
    await tester.pumpWidget(MaterialApp(home: Splash(ready: Future.value(), onDone: () => done = true)));
    await tester.pump(const Duration(milliseconds: 500));
    expect(done, isFalse);
    await tester.pump(Splash.duration);
    expect(done, isTrue);
  });

  testWidgets('with reduced motion there is no animation to wait for (#332)', (tester) async {
    var done = false;
    await tester.pumpWidget(MediaQuery(
      data: const MediaQueryData(disableAnimations: true),
      child: MaterialApp(home: Splash(ready: Future.value(), onDone: () => done = true)),
    ));
    await tester.pump();
    expect(done, isTrue);
  });
}
