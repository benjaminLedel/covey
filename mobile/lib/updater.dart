import 'dart:io';

import 'package:auto_updater/auto_updater.dart';
import 'package:flutter/foundation.dart';

import 'diagnostics.dart';

/// Updates for the Mac app (#421), through Sparkle.
///
/// The release job builds the app with the address of its appcast —
/// `<repository>/releases/latest/download/appcast.xml`, so a fork's app
/// follows the fork's releases — and attaches the appcast to every release,
/// signed with the key whose public half is in Info.plist (SUPublicEDKey).
/// Sparkle installs nothing that signature does not match.
///
/// A development build has no feed and never asks.
class Updater {
  Updater._();

  static const _feed = String.fromEnvironment('COVEY_UPDATE_FEED');

  static bool get supported => Platform.isMacOS && kReleaseMode && _feed != '';

  /// Checks now in the background, and then once a day.
  static Future<void> start() async {
    if (!supported) return;
    try {
      await autoUpdater.setFeedURL(_feed);
      await autoUpdater.setScheduledCheckInterval(86400);
      await autoUpdater.checkForUpdates(inBackground: true);
    } catch (e) {
      diag('update', 'not started: $e');
    }
  }

  /// "Check for updates": Sparkle's own window says what it found.
  static Future<void> check() async {
    if (!supported) return;
    await autoUpdater.checkForUpdates();
  }
}
