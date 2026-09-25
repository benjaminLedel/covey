import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:speech_to_text/speech_recognition_result.dart';
import 'package:speech_to_text/speech_to_text.dart';

/// Why dictation could not start.
enum DictationFailure {
  /// No recogniser on this device, or none that works on the device.
  unavailable,

  /// The person has not allowed the microphone or speech recognition.
  denied,

  /// The device could recognise speech, but not on the device for this
  /// language: iOS loads that model only with Dictation switched on in the
  /// keyboard settings. Recognition off the device is not an option — the
  /// notetaker's promise is that no audio leaves the phone.
  noOnDeviceModel,
}

/// Speech to text on the device (#336, spec/14): the phone's own recogniser
/// turns speech into text, and only text leaves it. No audio is kept or sent.
///
/// A recogniser stops after a pause or a time limit — fine for a sentence,
/// wrong for a meeting. In [continuous] mode the session is restarted every
/// time it stops, until [stop], and the finished parts are joined: the
/// meeting runs as long as the person lets it.
class Dictation extends ChangeNotifier {
  Dictation({SpeechToText? engine}) : _stt = engine ?? SpeechToText();

  final SpeechToText _stt;
  final List<String> _parts = [];
  String _partial = '';
  bool _running = false;
  bool _continuous = false;
  bool _ready = false;
  DictationFailure? failure;

  /// What the platform said, for the message: an error code is what makes a
  /// "not available" answerable.
  String? detail;

  bool get running => _running;

  /// Everything recognised so far, the part still being spoken included.
  String get text => [..._parts, if (_partial.isNotEmpty) _partial].join(' ').trim();

  Future<bool> _init() async {
    if (_ready) return true;
    try {
      _ready = await _stt.initialize(
        onStatus: _onStatus,
        onError: (e) {
          detail = e.errorMsg;
          _onStatus('error');
        },
      );
    } catch (e) {
      detail = e is PlatformException ? (e.message ?? e.code) : '$e';
      _ready = false;
    }
    if (!_ready) {
      failure = await _stt.hasPermission ? DictationFailure.unavailable : DictationFailure.denied;
    }
    return _ready;
  }

  /// Starts listening. Returns false and sets [failure] when it cannot.
  Future<bool> start({bool continuous = false}) async {
    failure = null;
    detail = null;
    if (!await _init()) {
      notifyListeners();
      return false;
    }
    _parts.clear();
    _partial = '';
    _continuous = continuous;
    _running = true;
    notifyListeners();
    await _listen();
    return _running;
  }

  Future<void> _listen() async {
    try {
      await _stt.listen(
        onResult: _onResult,
        listenOptions: SpeechListenOptions(
          // Long enough that a meeting is a handful of sessions, not
          // hundreds; the pause is how long silence may last before a
          // session ends.
          listenFor: _continuous ? const Duration(minutes: 30) : const Duration(minutes: 5),
          pauseFor: _continuous ? const Duration(seconds: 30) : const Duration(seconds: 8),
          onDevice: true,
          partialResults: true,
          autoPunctuation: true,
          listenMode: ListenMode.dictation,
          cancelOnError: false,
        ),
      );
    } on PlatformException catch (e) {
      detail = e.message ?? e.code;
      failure = e.code == 'onDeviceError' ? DictationFailure.noOnDeviceModel : DictationFailure.unavailable;
      _running = false;
      notifyListeners();
    } catch (e) {
      detail = '$e';
      failure = DictationFailure.unavailable;
      _running = false;
      notifyListeners();
    }
  }

  void _onResult(SpeechRecognitionResult r) {
    if (r.finalResult) {
      if (r.recognizedWords.trim().isNotEmpty) _parts.add(r.recognizedWords.trim());
      _partial = '';
    } else {
      _partial = r.recognizedWords;
    }
    notifyListeners();
  }

  void _onStatus(String status) {
    if (!_running) return;
    if (status == 'done' || status == 'notListening' || status == 'error') {
      // A finished session in continuous mode is not the end of the meeting.
      if (_continuous) {
        if (_partial.trim().isNotEmpty) _parts.add(_partial.trim());
        _partial = '';
        Future<void>.delayed(const Duration(milliseconds: 250), () {
          if (_running && !_stt.isListening) _listen();
        });
      } else if (status != 'notListening' || !_stt.isListening) {
        _running = false;
        notifyListeners();
      }
    }
  }

  /// Stops and returns the whole text.
  Future<String> stop() async {
    _running = false;
    await _stt.stop();
    if (_partial.trim().isNotEmpty) _parts.add(_partial.trim());
    _partial = '';
    notifyListeners();
    return text;
  }

  @override
  void dispose() {
    _running = false;
    _stt.cancel();
    super.dispose();
  }
}
