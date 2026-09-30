import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';

import 'diagnostics.dart';

/// The app's plain settings (#357): the speech model and language, the
/// switches of dictate-anywhere and the diagnostic log. None of it is
/// secret, so it lives in a small JSON file in the app's support directory,
/// not in the keychain — which refuses writes to an app outside the sandbox
/// without a keychain entitlement, silently, and the choice was gone at the
/// next start. The connection's key stays in the keychain (profile.dart).
class Prefs {
  Prefs._();

  static final Prefs instance = Prefs._();

  Map<String, String> _values = {};
  Future<void>? _loading;

  /// Set by [inMemory]: no file is read or written.
  bool _memoryOnly = false;

  /// Keeps the settings in memory only, starting from [values]. For tests,
  /// where there is no support directory to read, and asking for one before
  /// the test binding stands never answers.
  @visibleForTesting
  void inMemory([Map<String, String> values = const {}]) {
    _values = Map.of(values);
    _memoryOnly = true;
  }

  /// Set by [atDirectory]: where the file is, instead of the support
  /// directory.
  Directory? _dir;

  /// Keeps the settings in a file under [dir]. For tests of what reaches
  /// the file.
  @visibleForTesting
  void atDirectory(Directory dir) {
    _dir = dir;
    _values = {};
    _loading = null;
    _memoryOnly = false;
  }

  Future<File> _file() async => File('${(_dir ?? await getApplicationSupportDirectory()).path}/prefs.json');

  /// The writes, one after the other (#511): two at once wrote the same
  /// temporary file, and the one renamed first could carry the other's
  /// state — a switch turned on in the call settings was missing from the
  /// file afterwards.
  Future<void> _writing = Future.value();

  // In memory a fresh future each time, made in the caller's zone: one kept
  // from the zone that set up the test would complete in that zone, which a
  // test's fake clock never runs.
  Future<void> _load() => _memoryOnly
      ? Future.value()
      : _loading ??= () async {
          try {
            final f = await _file();
            if (await f.exists()) {
              _values = (jsonDecode(await f.readAsString()) as Map<String, dynamic>).map(
                (k, v) => MapEntry(k, v as String),
              );
            }
          } catch (e) {
            diag('prefs', 'unreadable, starting empty: $e');
          }
        }();

  Future<String?> read(String key) async {
    await _load();
    return _values[key];
  }

  Future<void> write(String key, String? value) async {
    await _load();
    void apply(Map<String, String> m) => value == null ? m.remove(key) : m[key] = value;
    apply(_values);
    if (_memoryOnly) return;
    final done = _writing.then((_) => _save(key, apply));
    _writing = done.catchError((_) {});
    await done;
  }

  /// Writes the file with [apply] made to what it holds now — so a key
  /// another copy of the app wrote meanwhile is not lost — and keeps that
  /// in memory.
  Future<void> _save(String key, void Function(Map<String, String>) apply) async {
    try {
      final f = await _file();
      await f.parent.create(recursive: true);
      if (await f.exists()) {
        try {
          final onDisk = (jsonDecode(await f.readAsString()) as Map<String, dynamic>).map(
            (k, v) => MapEntry(k, v as String),
          );
          // Every write of this copy is in the file already: what the file
          // holds is the newest, but for the one key written now.
          apply(onDisk);
          _values = onDisk;
        } catch (_) {
          // Unreadable: what is in memory replaces it.
        }
      }
      final tmp = File('${f.path}.tmp');
      await tmp.writeAsString(jsonEncode(_values), flush: true);
      await tmp.rename(f.path);
    } catch (e) {
      diag('prefs', 'not saved: $key · $e');
    }
  }
}
