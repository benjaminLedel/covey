import 'dart:async';

import 'package:covey_mobile/prefs.dart';

/// Runs before every test file. The settings live in memory, and the tour
/// (#402) counts as seen: it comes up over the home screen the first time
/// the app sees the team — in a test that is every time, and it would stand
/// over what the test taps. test/tour_test.dart clears it for its own tests.
Future<void> testExecutable(FutureOr<void> Function() testMain) async {
  Prefs.instance.inMemory({'tour.team': '1'});
  await testMain();
}
