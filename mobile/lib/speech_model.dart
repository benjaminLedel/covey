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

  /// The instance is still fetching it (the first minutes after an install).
  notReady,

  /// Fetching from the instance failed, or the file was not what the digest
  /// says. [SpeechModel.detail] has the words.
  failed,
}

/// The Whisper model on the phone (#348). It comes from the covey instance,
/// once, and is kept under the app's support directory by its digest: a new
/// model on the instance is a new digest and a new download, the old file
/// goes. What was downloaded is verified against the digest the instance
/// names before whisper.cpp ever opens it.
///
/// One per app: the file is shared by dictation in notes and meetings, and
/// two downloads of 150 MB at once would be one too many.
class SpeechModel extends ChangeNotifier {
  SpeechModel._();

  static final SpeechModel instance = SpeechModel._();

  String? _path;
  Future<String?>? _running;

  /// Bytes fetched so far and expected, while downloading.
  int received = 0;
  int total = 0;
  bool downloading = false;
  SpeechModelProblem? problem;
  String? detail;

  /// What the instance said last: which model, how large.
  SpeechModelInfo? info;

  /// Whether the model is on this device.
  bool get onDevice => _path != null;

  /// The language whisper listens for; null follows the app's language.
  String? language;
  static const _languageKey = 'speech.language';
  final _prefs = const FlutterSecureStorage();

  Future<void> loadLanguage() async {
    try {
      language = await _prefs.read(key: _languageKey);
    } catch (_) {
      language = null;
    }
    notifyListeners();
  }

  Future<void> setLanguage(String? lang) async {
    language = lang;
    notifyListeners();
    try {
      if (lang == null) {
        await _prefs.delete(key: _languageKey);
      } else {
        await _prefs.write(key: _languageKey, value: lang);
      }
    } catch (_) {
      // Kept for this run; the next start follows the app again.
    }
  }

  /// Asks the instance what it offers and looks whether that is already on
  /// the device — without downloading anything.
  Future<void> refresh(CoveyApi api) async {
    try {
      final i = await api.speechModel();
      info = i;
      if (i.enabled) {
        final f = File(await _file(i));
        if (await f.exists() && await f.length() == i.size) _path = f.path;
      }
      problem = i.enabled ? null : SpeechModelProblem.off;
    } on ApiException catch (e) {
      problem = SpeechModelProblem.failed;
      detail = e.message;
    }
    notifyListeners();
  }

  /// Removes the model from the device; the next dictation fetches it again.
  Future<void> remove() async {
    _path = null;
    final dir = Directory('${(await getApplicationSupportDirectory()).path}/speech');
    if (await dir.exists()) await dir.delete(recursive: true);
    notifyListeners();
  }

  Future<String> _file(SpeechModelInfo i) async =>
      '${(await getApplicationSupportDirectory()).path}/speech/${i.sha256}.bin';

  /// The fraction downloaded, 0–1, or null when nothing is being fetched.
  double? get progress => downloading && total > 0 ? received / total : null;

  /// Where the verified model lies, fetching it first when it is not there.
  /// Null with [problem] set when it cannot be had now.
  Future<String?> ensure(CoveyApi api) {
    if (_path != null) return Future.value(_path);
    return _running ??= _ensure(api).whenComplete(() => _running = null);
  }

  Future<String?> _ensure(CoveyApi api) async {
    problem = null;
    detail = null;
    final SpeechModelInfo info;
    try {
      info = await api.speechModel();
    } on ApiException catch (e) {
      return _fail(SpeechModelProblem.failed, e.message);
    }
    this.info = info;
    if (!info.enabled) return _fail(SpeechModelProblem.off, null);

    final dir = Directory('${(await getApplicationSupportDirectory()).path}/speech');
    await dir.create(recursive: true);
    final file = File('${dir.path}/${info.sha256}.bin');
    if (await file.exists() && await file.length() == info.size) {
      _path = file.path;
      notifyListeners();
      return _path;
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
      await _download(api, part);
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

  Future<void> _download(CoveyApi api, File part) async {
    if (received >= total) {
      // A complete leftover: nothing to fetch, the digest decides.
      return;
    }
    final res = await api.speechModelFile(from: received);
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
