import 'dart:async';

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/ears.dart';
import 'package:covey_mobile/call/voice.dart';
import 'package:covey_mobile/call/voice_choice.dart';
import 'package:covey_mobile/models.dart';
import 'package:flutter/foundation.dart';

// Stand-ins for a call's microphone, instance and voice (#494).

class FakeEars implements CallEars {
  CallWindow? _onWindow;
  Completer<void>? gate;
  bool listening = false;
  bool closed = false;

  /// What each next turn is recognised as.
  final heard = <String>[];

  @override
  Future<void> prepare() => gate?.future ?? Future.value();

  @override
  Future<void> listen(CallWindow onWindow) async {
    _onWindow = onWindow;
    listening = true;
  }

  @override
  Future<void> pause() async => listening = false;

  @override
  Future<String> recognise(Uint8List pcm) async => heard.isEmpty ? '' : heard.removeAt(0);

  @override
  Future<void> close() async {
    listening = false;
    closed = true;
  }

  /// [ms] of the person's voice at [level], or of silence.
  void feed(int ms, {required bool voiced, double level = 0.6}) {
    for (var i = 0; i < ms ~/ 32; i++) {
      if (listening) _onWindow?.call(Uint8List(1024), voiced, voiced ? level : 0.05);
    }
  }

  /// A whole turn: voice, then the pause that ends it.
  void say(String text, {int ms = 640}) {
    heard.add(text);
    feed(ms, voiced: true);
    feed(736, voiced: false);
  }
}

class FakeBackend implements CallBackend {
  final messages = <ConversationMessage>[];
  final posted = <String>[];
  final _changes = StreamController<void>.broadcast();
  var _n = 0;

  ConversationMessage _add(String kind, String text, {String? author}) {
    final m = ConversationMessage(
      id: 'm${_n++}',
      conversationId: 'c1',
      authorKind: kind,
      authorId: author,
      text: text,
      kind: 'text',
      createdAt: DateTime(2026, 9, 30, 12).add(Duration(seconds: _n)),
    );
    messages.add(m);
    return m;
  }

  /// The agent writes into the conversation.
  void agentSays(String text) {
    _add('agent', text, author: 'agent-1');
    _changes.add(null);
  }

  @override
  Future<List<ConversationMessage>> open() async => List.of(messages);

  @override
  Future<List<ConversationMessage>> after(String lastId) async {
    final i = messages.indexWhere((m) => m.id == lastId);
    return messages.sublist(i + 1);
  }

  @override
  Future<ConversationMessage> post(String text) async {
    posted.add(text);
    return _add('human', text, author: 'me');
  }

  @override
  Stream<void> changes() => _changes.stream;

  /// What each clean-up answers: a text, null (skipped), or an error.
  Object? Function(String text)? cleanAs;
  final cleaned = <(String, CleanContext)>[];

  @override
  Future<String?> clean(String text, CleanContext context) async {
    cleaned.add((text, context));
    final c = cleanAs?.call(text);
    if (c is Exception) throw c;
    return c as String?;
  }

  @override
  Future<List<String>> names() async => const ['Ada Lovelace', 'Grace'];

  SpokenVoice? voice;

  @override
  Future<SpokenVoice?> spokenVoice() async => voice;
}

class FakeSpeaker implements Speaker {
  /// What was spoken, in which language.
  final spoken = <(String, String)>[];
  final _level = ValueNotifier<double?>(null);

  @override
  ValueListenable<double?> get level => _level;

  /// The own voice's mouth, as the synthesised audio would drive it.
  set mouth(double? v) => _level.value = v;

  @override
  Future<String> prepare({required String language}) async => 'fake voices';
  int stops = 0;
  Completer<void>? _current;
  final _events = StreamController<SpeakingEvent>.broadcast();

  bool get speaking => _current != null;

  @override
  Stream<SpeakingEvent> get events => _events.stream;

  @override
  Future<String?> language(String text) async => RegExp(r'\b(the|is|and)\b').hasMatch(text) ? 'en' : 'de';

  @override
  Future<void> speak(String text, {required String language}) {
    spoken.add((text, language));
    _events.add(SpeakingEvent.started);
    return (_current = Completer<void>()).future;
  }

  /// One word reached.
  void word() => _events.add(SpeakingEvent.word);

  /// The utterance ends by itself.
  void finish() {
    final c = _current;
    _current = null;
    _events.add(SpeakingEvent.finished);
    c?.complete();
  }

  @override
  Future<void> stop() async {
    stops++;
    final c = _current;
    _current = null;
    if (c != null) {
      _events.add(SpeakingEvent.cancelled);
      c.complete();
    }
  }

  @override
  void dispose() {}
}

const words = {'call.stillWorking': 'Ich bin noch dran.', 'call.restInChat': 'Der Rest steht im Chat.'};

CallController fakeCall(
  FakeEars ears,
  FakeBackend backend,
  FakeSpeaker speaker, {
  Duration nudgeAfter = const Duration(seconds: 20),
  CallTuning tuning = const CallTuning(window: Duration.zero),
}) => CallController(
  backend: backend,
  ears: ears,
  speaker: speaker,
  agentId: 'agent-1',
  appLanguage: 'de',
  words: (k) => words[k] ?? k,
  nudgeAfter: nudgeAfter,
  agentName: 'Ada Lovelace',
  tuning: tuning,
);
