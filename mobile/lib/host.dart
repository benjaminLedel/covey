import 'dart:io';

import 'package:flutter/foundation.dart';

/// The operating system the app runs on: `dart:io`'s [Platform], with one
/// seam. A test that draws the app as it looks on another system — the App
/// Store screenshots (mobile/store/), drawn on whatever machine runs the
/// tests — sets [debugOverride]; nothing else does.
abstract final class Host {
  @visibleForTesting
  static TargetPlatform? debugOverride;

  static bool get isMacOS => _is(TargetPlatform.macOS, Platform.isMacOS);
  static bool get isIOS => _is(TargetPlatform.iOS, Platform.isIOS);
  static bool get isAndroid => _is(TargetPlatform.android, Platform.isAndroid);
  static bool get isWindows => _is(TargetPlatform.windows, Platform.isWindows);
  static bool get isLinux => _is(TargetPlatform.linux, Platform.isLinux);

  /// The Mac App Store build (#486), chosen at build time with
  /// `--dart-define=COVEY_STORE=true` (mobile/tool/macos_store_release.sh).
  /// It runs in the App Sandbox, so what needs to reach into other apps —
  /// dictate-anywhere's synthesised ⌘V, the activity log's Accessibility
  /// reads — is off, and so is Sparkle: the store updates the app.
  static const bool appStore = bool.fromEnvironment('COVEY_STORE');

  static bool _is(TargetPlatform p, bool actual) => debugOverride == null ? actual : debugOverride == p;
}
