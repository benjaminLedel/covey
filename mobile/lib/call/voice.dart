import 'dart:async';

import 'package:flutter/services.dart';

import '../diagnostics.dart';
import 'speech_text.dart';

/// What the synthesiser reports while it speaks (#494): it began, it
/// reached a word (the face's mouth moves on each), it finished or was
/// stopped.
enum SpeakingEvent { started, word, finished, cancelled }

/// The seam to the system's speech synthesis, so a test stands in.
abstract class Speaker {
  Future<List<SystemVoice>> voices();

  /// The language [text] is written in, BCP 47, when it can be told with
  /// some confidence; null otherwise.
  Future<String?> language(String text);

  /// Speaks [text]; the returned future completes when speaking ends,
  /// finished or stopped.
  Future<void> speak(String text, {SystemVoice? voice, required String language});

  /// Stops at once, mid-word.
  Future<void> stop();

  Stream<SpeakingEvent> get events;

  void dispose();
}

/// macOS `AVSpeechSynthesizer` through the `covey/voice` channel
/// (MainFlutterWindow.swift). Speech is synthesised on the Mac; no text
/// goes anywhere for it.
class MacSpeaker implements Speaker {
  MacSpeaker() {
    _channel.setMethodCallHandler(_onCall);
  }

  static const _channel = MethodChannel('covey/voice');
  final _events = StreamController<SpeakingEvent>.broadcast();
  Completer<void>? _done;
  int _id = 0;

  Future<Object?> _onCall(MethodCall call) async {
    final e = switch (call.method) {
      'started' => SpeakingEvent.started,
      'word' => SpeakingEvent.word,
      'finished' => SpeakingEvent.finished,
      'cancelled' => SpeakingEvent.cancelled,
      _ => null,
    };
    if (e == null) return null;
    // Only the utterance asked for last counts: the end of one that was
    // replaced must not end the one that replaced it.
    final id = (call.arguments as Map?)?['id'];
    if (id is int && id != _id) return null;
    _events.add(e);
    if (e == SpeakingEvent.finished || e == SpeakingEvent.cancelled) {
      final d = _done;
      _done = null;
      if (d != null && !d.isCompleted) d.complete();
    }
    return null;
  }

  @override
  Stream<SpeakingEvent> get events => _events.stream;

  @override
  Future<List<SystemVoice>> voices() async {
    try {
      final list = await _channel.invokeListMethod<Object?>('voices') ?? const [];
      return [for (final v in list.whereType<Map<Object?, Object?>>()) SystemVoice.fromJson(v)];
    } on PlatformException catch (e) {
      diag('call', 'no voices: ${e.message}');
      return const [];
    }
  }

  @override
  Future<String?> language(String text) async {
    try {
      final r = await _channel.invokeMapMethod<String, Object?>('language', {'text': text});
      final p = (r?['confidence'] as num?)?.toDouble() ?? 0;
      final lang = r?['language'];
      return p >= 0.6 && lang is String ? lang : null;
    } on PlatformException {
      return null;
    }
  }

  @override
  Future<void> speak(String text, {SystemVoice? voice, required String language}) async {
    final prev = _done;
    if (prev != null && !prev.isCompleted) prev.complete();
    final done = _done = Completer<void>();
    await _channel.invokeMethod<void>('speak', {'id': ++_id, 'text': text, 'voice': voice?.id, 'language': language});
    return done.future;
  }

  @override
  Future<void> stop() => _channel.invokeMethod<void>('stop');

  @override
  void dispose() {
    unawaited(stop().catchError((_) {}));
    _channel.setMethodCallHandler(null);
    unawaited(_events.close());
  }
}
