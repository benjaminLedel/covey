import 'dart:async';
import 'dart:io';
import 'dart:isolate';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';

import 'api.dart';

/// Why the speech model is not on the phone.
enum SpeechModelProblem {
  /// The instance offers no model: speech is switched off there.
  off,

  /// The instance could not fetch it (yet).
  notReady,

  /// Fetching from the instance failed, or the file was not what the digest
  /// says. [SpeechModel.detail] has the words.
  failed,
}

/// The Whisper model on the phone (#348, #351). It comes from the covey
/// instance, once, and is kept under the app's support directory by its
/// digest. What was downloaded is verified against the digest the instance
/// names before whisper.cpp ever opens it.
///
/// The instance may offer several models; which one is used is the person's
/// choice ([chosen], null: the instance's default). Only the one in use is
/// kept: switching downloads the new one and removes the old one once the
/// new one is verified.
///
/// One per app: the file is shared by dictation in notes and meetings, and
/// two downloads of hundreds of MB at once would be one too many.
class SpeechModel extends ChangeNotifier {
  SpeechModel._();

  static final SpeechModel instance = SpeechModel._();

  String? _path;
  Future<String?>? _running;

  /// Bytes fetched so far and expected, while the phone downloads.
  int received = 0;
  int total = 0;
  bool downloading = false;

  /// While the instance itself is still fetching the model: its progress.
  bool onInstance = false;
  int instanceReceived = 0;

  SpeechModelProblem? problem;
  String? detail;

  /// What the instance said last: the models it offers and their state.
  SpeechModelInfo? info;

  /// Whether the model in use is on this device.
  bool get onDevice => _path != null;

  /// The model picked in settings; null follows the instance's default.
  String? chosen;

  /// The language whisper listens for; null follows the app's language.
  String? language;

  static const _languageKey = 'speech.language';
  static const _modelKey = 'speech.model';
  final _prefs = const FlutterSecureStorage();

  Future<void> loadPrefs() async {
    try {
      language = await _prefs.read(key: _languageKey);
      chosen = await _prefs.read(key: _modelKey);
    } catch (_) {
      // Unreadable: the defaults.
    }
    notifyListeners();
  }

  Future<void> _save(String key, String? value) async {
    try {
      if (value == null) {
        await _prefs.delete(key: key);
      } else {
        await _prefs.write(key: key, value: value);
      }
    } catch (_) {
      // Kept for this run.
    }
  }

  Future<void> setLanguage(String? lang) async {
    language = lang;
    notifyListeners();
    await _save(_languageKey, lang);
  }

  /// Picks a model; the next [ensure] fetches it.
  Future<void> choose(String? name) async {
    chosen = name;
    _path = null;
    problem = null;
    detail = null;
    notifyListeners();
    await _save(_modelKey, name);
  }

  Future<Directory> _dir() async => Directory('${(await getApplicationSupportDirectory()).path}/speech');

  /// Asks the instance what it offers and looks whether the model in use is
  /// already on the device — without downloading anything.
  Future<void> refresh(CoveyApi api) async {
    try {
      final i = await _ask(api);
      info = i;
      problem = i.enabled ? null : SpeechModelProblem.off;
      if (i.enabled) {
        final f = File('${(await _dir()).path}/${i.sha256}.bin');
        _path = await f.exists() && await f.length() == i.size ? f.path : null;
      }
    } on ApiException catch (e) {
      problem = SpeechModelProblem.failed;
      detail = e.message;
    }
    notifyListeners();
  }

  /// The model in use, as the instance describes it. A chosen model the
  /// instance no longer offers falls back to its default.
  Future<SpeechModelInfo> _ask(CoveyApi api) async {
    final name = chosen;
    if (name == null) return api.speechModel();
    try {
      return await api.speechModel(name: name);
    } on ApiException catch (e) {
      if (e.status != 404) rethrow;
      await choose(null);
      return api.speechModel();
    }
  }

  /// Removes the model from the device; the next dictation fetches it again.
  Future<void> remove() async {
    _path = null;
    final dir = await _dir();
    if (await dir.exists()) await dir.delete(recursive: true);
    notifyListeners();
  }

  /// The fraction downloaded, 0–1: of the instance's fetch while it runs,
  /// then of the phone's. Null when nothing is being fetched.
  double? get progress {
    final size = info?.size ?? 0;
    if (onInstance && size > 0) return instanceReceived / size;
    return downloading && total > 0 ? received / total : null;
  }

  /// Where the verified model lies, fetching it first when it is not there.
  /// Null with [problem] set when it cannot be had now.
  Future<String?> ensure(CoveyApi api) {
    if (_path != null) return Future.value(_path);
    return _running ??= _ensure(api).whenComplete(() => _running = null);
  }

  Future<String?> _ensure(CoveyApi api) async {
    problem = null;
    detail = null;
    SpeechModelInfo info;
    try {
      info = await _ask(api);
    } on ApiException catch (e) {
      return _fail(SpeechModelProblem.failed, e.message);
    }
    this.info = info;
    if (!info.enabled) return _fail(SpeechModelProblem.off, null);

    final dir = await _dir();
    await dir.create(recursive: true);
    final file = File('${dir.path}/${info.sha256}.bin');
    if (await file.exists() && await file.length() == info.size) {
      _path = file.path;
      notifyListeners();
      return _path;
    }

    // A model somebody just picked may still be on its way to the
    // instance: wait for it there, with its progress, rather than failing.
    if (!info.ready && info.fetching) {
      onInstance = true;
      try {
        while (!info.ready && info.fetching) {
          instanceReceived = info.received;
          notifyListeners();
          await Future<void>.delayed(const Duration(seconds: 2));
          info = await _ask(api);
          this.info = info;
        }
      } on ApiException catch (e) {
        return _fail(SpeechModelProblem.failed, e.message);
      } finally {
        onInstance = false;
      }
    }
    if (!info.ready) {
      return _fail(info.error == null ? SpeechModelProblem.notReady : SpeechModelProblem.failed, info.error);
    }

    final part = File('${file.path}.part');
    downloading = true;
    total = info.size;
    received = await part.exists() ? await part.length() : 0;
    notifyListeners();
    try {
      await _download(api, info.name, part);
      final sum = await Isolate.run(() => _sha256(part.path));
      if (sum != info.sha256) {
        await part.delete();
        return _fail(SpeechModelProblem.failed, 'sha256 $sum');
      }
      await part.rename(file.path);
      // Whatever model came before is not needed any more.
      await for (final f in dir.list()) {
        if (f.path != file.path && f is File && !f.path.endsWith('.part')) await f.delete();
      }
      _path = file.path;
      return _path;
    } on ApiException catch (e) {
      return _fail(e.status == 503 ? SpeechModelProblem.notReady : SpeechModelProblem.failed, e.message);
    } on Exception catch (e) {
      return _fail(SpeechModelProblem.failed, '$e');
    } finally {
      downloading = false;
      notifyListeners();
    }
  }

  Future<void> _download(CoveyApi api, String name, File part) async {
    if (received >= total) {
      // A complete leftover: nothing to fetch, the digest decides.
      return;
    }
    final res = await api.speechModelFile(name: name, from: received);
    if (res.statusCode == 200 && received > 0) {
      // The instance ignored the range: start over.
      received = 0;
    }
    final sink = part.openWrite(mode: received > 0 ? FileMode.append : FileMode.write);
    try {
      await for (final chunk in res.stream) {
        sink.add(chunk);
        received += chunk.length;
        notifyListeners();
      }
    } finally {
      await sink.close();
    }
  }

  String? _fail(SpeechModelProblem p, String? d) {
    problem = p;
    detail = d;
    notifyListeners();
    return null;
  }

  /// For tests: forget the path so the next [ensure] asks again.
  @visibleForTesting
  void reset() {
    _path = null;
    problem = null;
    detail = null;
  }
}

Future<String> _sha256(String path) async => (await sha256.bind(File(path).openRead()).first).toString();
