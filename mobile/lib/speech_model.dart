import 'dart:async';
import 'dart:io';
import 'dart:isolate';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';

import 'api.dart';
import 'diagnostics.dart';
import 'prefs.dart';

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

/// The speech model on the device (#348, #351, #366). It comes from the covey
/// instance, once, and is kept under the app's support directory by its
/// digest. What was downloaded is verified against the digest the instance
/// names before the recogniser ever opens it.
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

  /// The app's language (#366): Parakeet covers the European ones; with the
  /// app in Chinese, Japanese or Korean and no model chosen, SenseVoice.
  String appLanguage = 'en';
  static const _asian = {'zh', 'ja', 'ko'};

  static const _modelKey = 'speech.model';
  final _prefs = Prefs.instance;

  Future<void> loadPrefs() async {
    try {
      chosen = await _prefs.read(_modelKey);
    } catch (_) {
      // Unreadable: the defaults.
    }
    notifyListeners();
  }

  Future<void> _save(String key, String? value) async {
    try {
      if (value == null) {
        await _prefs.write(key, null);
      } else {
        await _prefs.write(key, value);
      }
    } catch (_) {
      // Kept for this run.
    }
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
        final d = Directory('${(await _dir()).path}/${i.sha256}');
        _path = await _complete(d, i) ? d.path : null;
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
    final name = chosen ?? (_asian.contains(appLanguage) ? 'sensevoice' : null);
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
    // A model without files is nothing to verify — an instance from before
    // #353 lists none; never take that for a complete download.
    if (info.files.isEmpty) return _fail(SpeechModelProblem.failed, 'the instance lists no files for ${info.name}');

    final root = await _dir();
    final dir = Directory('${root.path}/${info.sha256}');
    await dir.create(recursive: true);
    if (await _complete(dir, info)) {
      _path = dir.path;
      notifyListeners();
      return _path;
    }

    // A model somebody just picked may still be on its way to the
    // instance: wait for it there, with its progress, rather than failing.
    if (!info.ready && info.fetching) {
      diag('speech', 'waiting for the instance to fetch ${info.name}');
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

    diag('speech', 'downloading ${info.name}, ${info.size} bytes in ${info.files.length} files');
    downloading = true;
    total = info.size;
    received = 0;
    notifyListeners();
    try {
      for (final f in info.files) {
        final done = File('${dir.path}/${f.name}');
        if (await done.exists() && await done.length() == f.size) {
          received += f.size;
          continue;
        }
        final part = File('${done.path}.part');
        final had = await part.exists() ? await part.length() : 0;
        received += had;
        await _download(api, info.name, f, part, had);
        final sum = await Isolate.run(() => _sha256(part.path));
        if (sum != f.sha256) {
          await part.delete();
          return _fail(SpeechModelProblem.failed, '${f.name}: sha256 $sum');
        }
        await part.rename(done.path);
      }
      diag('speech', '${info.name} verified and kept');
      // Whatever model came before is not needed any more.
      await for (final e in root.list()) {
        if (e.path != dir.path) await e.delete(recursive: true);
      }
      _path = dir.path;
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

  /// Whether every file of the model lies in [dir] at its full size. The
  /// digests were checked when the files arrived.
  Future<bool> _complete(Directory dir, SpeechModelInfo info) async {
    if (info.files.isEmpty) return false;
    for (final f in info.files) {
      final file = File('${dir.path}/${f.name}');
      if (!await file.exists() || await file.length() != f.size) return false;
    }
    return true;
  }

  Future<void> _download(CoveyApi api, String name, SpeechModelFile f, File part, int had) async {
    if (had >= f.size) {
      // A complete leftover: nothing to fetch, the digest decides.
      return;
    }
    final res = await api.speechModelFile(name: name, file: f.name, from: had);
    if (res.statusCode == 200 && had > 0) {
      // The instance ignored the range: start over.
      received -= had;
      had = 0;
    }
    final sink = part.openWrite(mode: had > 0 ? FileMode.append : FileMode.write);
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

  /// Which recogniser the model in use is for.
  String get engine => info?.engine ?? 'parakeet';

  String? _fail(SpeechModelProblem p, String? d) {
    diag('speech', 'model not available: ${p.name}${d == null ? '' : ' · $d'}');
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
