import 'dart:convert';
import 'dart:io';

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

  Future<File> _file() async => File('${(await getApplicationSupportDirectory()).path}/prefs.json');

  Future<void> _load() => _loading ??= () async {
    try {
      final f = await _file();
      if (await f.exists()) {
        _values = (jsonDecode(await f.readAsString()) as Map<String, dynamic>).map((k, v) => MapEntry(k, v as String));
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
    if (value == null) {
      _values.remove(key);
    } else {
      _values[key] = value;
    }
    try {
      final f = await _file();
      await f.parent.create(recursive: true);
      final tmp = File('${f.path}.tmp');
      await tmp.writeAsString(jsonEncode(_values));
      await tmp.rename(f.path);
    } catch (e) {
      diag('prefs', 'not saved: $key · $e');
    }
  }
}
