import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';

/// The diagnostic log (#352): switched on in settings, it writes what the
/// app does — requests with their status and time, dictation's segments and
/// levels, the speech model's download, errors — to a file on the device.
/// It never holds content: no note text, no transcript, no message, no key;
/// lengths and states only. Off, nothing is written; every line still goes
/// to stderr, which a device console shows during development.
///
/// The file lives in the app's support directory as `logs/covey.log`, with
/// the previous one beside it as `covey.1.log` once it passes 1 MB. On a
/// development build it can be copied off the phone with `xcrun devicectl
/// device copy from --domain-type appDataContainer`, the app's bundle id as
/// `--domain-identifier` and `--source "Library/Application Support/logs"`.
class Diagnostics extends ChangeNotifier {
  Diagnostics._();

  static final Diagnostics instance = Diagnostics._();

  static const _key = 'diagnostics.enabled';
  static const _maxBytes = 1 << 20;
  final _prefs = const FlutterSecureStorage();

  bool enabled = false;
  File? _file;
  IOSink? _sink;
  int _written = 0;

  Future<Directory> _dir() async => Directory('${(await getApplicationSupportDirectory()).path}/logs');

  Future<void> init() async {
    try {
      enabled = await _prefs.read(key: _key) == 'on';
    } catch (_) {
      enabled = false;
    }
    if (enabled) await _open();
    notifyListeners();
  }

  Future<void> setEnabled(bool on) async {
    enabled = on;
    if (on) {
      await _open();
      log('app', 'diagnostic log on · ${Platform.operatingSystem} ${Platform.operatingSystemVersion}');
    } else {
      log('app', 'diagnostic log off');
      await _close();
    }
    notifyListeners();
    try {
      await _prefs.write(key: _key, value: on ? 'on' : 'off');
    } catch (_) {
      // Kept for this run.
    }
  }

  Future<void> _open() async {
    if (_sink != null) return;
    final dir = await _dir();
    await dir.create(recursive: true);
    final f = File('${dir.path}/covey.log');
    _written = await f.exists() ? await f.length() : 0;
    _file = f;
    _sink = f.openWrite(mode: FileMode.append);
  }

  Future<void> _close() async {
    final s = _sink;
    _sink = null;
    await s?.flush();
    await s?.close();
  }

  /// One line: time, area, what happened.
  void log(String area, String line) {
    final text = '${DateTime.now().toIso8601String()} [$area] $line';
    stderr.writeln('covey $text');
    final s = _sink;
    if (!enabled || s == null) return;
    s.writeln(text);
    _written += text.length + 1;
    if (_written > _maxBytes) unawaited(_rotate());
  }

  Future<void> _rotate() async {
    final f = _file;
    if (f == null || _sink == null) return;
    await _close();
    final old = File('${f.parent.path}/covey.1.log');
    if (await old.exists()) await old.delete();
    await f.rename(old.path);
    await _open();
  }

  /// Both files, oldest first, as one text.
  Future<String> read() async {
    await _sink?.flush();
    final dir = await _dir();
    final out = StringBuffer();
    for (final name in ['covey.1.log', 'covey.log']) {
      final f = File('${dir.path}/$name');
      if (await f.exists()) out.write(await f.readAsString());
    }
    return out.toString();
  }

  /// How large the log is, in bytes.
  Future<int> size() async {
    await _sink?.flush();
    final dir = await _dir();
    var n = 0;
    for (final name in ['covey.1.log', 'covey.log']) {
      final f = File('${dir.path}/$name');
      if (await f.exists()) n += await f.length();
    }
    return n;
  }

  Future<void> clear() async {
    final was = enabled;
    await _close();
    final dir = await _dir();
    if (await dir.exists()) await dir.delete(recursive: true);
    if (was) await _open();
    notifyListeners();
  }
}

/// Shorthand: `diag('api', 'GET /me 200 84 ms')`.
void diag(String area, String line) => Diagnostics.instance.log(area, line);
