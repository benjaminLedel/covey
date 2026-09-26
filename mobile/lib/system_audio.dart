import 'dart:io';

import 'package:flutter/services.dart';

/// The Mac's own audio (#364): what the Mac plays — the other side of a
/// call — as 16 kHz mono PCM16, from a Core Audio process tap
/// (MainFlutterWindow.swift). Listening starts the tap; cancelling stops it.
abstract final class SystemAudio {
  /// macOS only; the native side refuses before macOS 14.4.
  static bool get supported => Platform.isMacOS;

  static const _channel = EventChannel('covey/system-audio');

  static Stream<Uint8List> stream() => _channel.receiveBroadcastStream().map((e) => e as Uint8List);
}
