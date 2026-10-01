import 'dart:async';

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/call/ears.dart';
import 'package:covey_mobile/call/fillers.dart';
import 'package:covey_mobile/call/greeting.dart';
import 'package:covey_mobile/call/provider.dart';
import 'package:covey_mobile/call/sounds.dart';
import 'package:covey_mobile/call/voice.dart';
import 'package:covey_mobile/call/spoken_voice.dart';
import 'package:covey_mobile/models.dart';
import 'package:flutter/foundation.dart';

// Stand-ins for a call's microphone, instance and voice (#494).

class FakeEars implements CallEars {
  CallWindow? _onWindow;
  Completer<void>? gate;
  bool listening = false;
  bool closed = false;

  @override
  bool echoCancelled = false;

  @override
  DateTime? echoCancelledSince;

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
  Future<String> recognise(Uint8List pcm) async {
    recognitions++;
    return heard.isEmpty ? '' : heard.removeAt(0);
  }

  /// How often a turn was recognised.
  int recognitions = 0;

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
    feed(1056, voiced: false);
  }
}

class FakeBackend implements CallBackend {
  final messages = <ConversationMessage>[];
  final posted = <String>[];
  final _changes = StreamController<void>.broadcast();
  var _n = 0;

  ConversationMessage _add(
    String kind,
    String text, {
    String? author,
    String? taskId,
    String? replyTo,
    String messageKind = 'text',
    Map<String, String> meta = const {},
  }) {
    final m = ConversationMessage(
      id: 'm${_n++}',
      conversationId: 'c1',
      authorKind: kind,
      authorId: author,
      text: text,
      kind: messageKind,
      taskId: taskId,
      replyTo: replyTo,
      createdAt: DateTime(2026, 9, 30, 12).add(Duration(seconds: _n)),
      meta: meta,
    );
    messages.add(m);
    return m;
  }

  /// The agent writes into the conversation.
  /// [taskId] and [replyTo] as the triage writes a task's acknowledgement
  /// (#411); [kind] `result` as a task's result; [meta] as the server stores
  /// it (e.g. `spoken`, #502).
  void agentSays(
    String text, {
    String? taskId,
    String? replyTo,
    String kind = 'text',
    Map<String, String> meta = const {},
  }) {
    _add('agent', text, author: 'agent-1', taskId: taskId, replyTo: replyTo, messageKind: kind, meta: meta);
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

  /// The times the call marked the conversation read up to.
  final readUpTo = <DateTime>[];

  @override
  Future<void> read(DateTime at) async => readUpTo.add(at);

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

  SpokenVoice voice = const SpokenVoice();

  @override
  Future<SpokenVoice> spokenVoice() async => voice;

  GreetingFacts facts = const GreetingFacts(personName: 'Grace Hopper');

  @override
  Future<GreetingFacts> greetingFacts() async => facts;

  /// Whether the organisation recognises at its voice provider (#516).
  bool serverRecognises = false;

  /// What the voice provider answers for each next turn: a text, an
  /// exception, or a completer a test finishes when it likes. Empty: "".
  final serverHeard = <Object>[];

  /// What was sent to it: the WAV and the language.
  final transcribed = <(Uint8List, String)>[];

  @override
  Future<bool> transcribes() async => serverRecognises;

  @override
  Future<String> transcribe(Uint8List wav, {required String language}) async {
    transcribed.add((wav, language));
    final next = serverHeard.isEmpty ? '' : serverHeard.removeAt(0);
    if (next is Exception) throw next;
    if (next is Completer<String>) return next.future;
    return next as String;
  }
}

class FakeSpeaker implements Speaker {
  /// What was spoken, in which language.
  final spoken = <(String, String)>[];
  final _level = ValueNotifier<double?>(null);

  @override
  ValueListenable<double?> get level => _level;

  /// The provider voice's mouth, as its audio would drive it.
  set mouth(double? v) => _level.value = v;

  /// Whether the Mac's voice stands in for the voice provider.
  final fallbackNotifier = ValueNotifier<bool>(false);

  @override
  ValueListenable<bool> get fallback => fallbackNotifier;

  /// How loud what plays is, as the provider's audio would say.
  @override
  double? playbackDb;

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

  /// Each time the typing started (#526), at the volume it played at, and
  /// how it was ended.
  final fillers = <double>[];
  int fades = 0, fillerStops = 0;

  /// False: there is nothing to play the typing with.
  bool canThink = true;

  /// Holds the typing's start until completed; null answers at once.
  Completer<void>? fillerGate;

  /// How long each fade lasted.
  final fadedOver = <Duration>[];

  @override
  Future<bool> thinking(Pcm pcm, {required double volume}) async {
    if (!canThink) return false;
    await fillerGate?.future;
    fillers.add(volume);
    return true;
  }

  @override
  Future<void> fadeFiller(Duration over) async {
    fades++;
    fadedOver.add(over);
  }

  @override
  Future<void> stopFiller() async => fillerStops++;

  /// The greeting's synthesis ahead: what was asked, and when it is ready.
  final prepared = <String>[];
  Completer<bool> ready = Completer<bool>();

  /// Per text, when it is ready; a text not in it takes [ready].
  final readyFor = <String, Completer<bool>>{};

  @override
  Future<bool> prepareUtterance(String text, {required String language}) {
    prepared.add(text);
    return (readyFor[text] ?? ready).future;
  }

  /// What was spoken as prepared, and whether the provider's audio was.
  final spokenPrepared = <(String, bool)>[];

  @override
  Future<void> speakPrepared(String text, {required String language, required bool ready}) {
    spokenPrepared.add((text, ready));
    return speak(text, language: language);
  }

  @override
  void dispose() {}
}

/// Where a call's sounds go in a test: each sound is loaded as that many
/// samples as its place in [Earcon], so what plays says which it was.
class FakeEarcons implements EarconOutput {
  final played = <(Earcon, double, int)>[];
  int stops = 0;

  @override
  Future<void> playEarcon(Pcm pcm, {required double volume, int loops = 1}) async =>
      played.add((Earcon.values[pcm.samples.length - 1], volume, loops));

  @override
  Future<void> stopEarcon() async => stops++;

  List<Earcon> get names => [for (final p in played) p.$1];

  CallSounds sounds({bool Function()? enabled, double Function()? volume}) => CallSounds(
    output: this,
    enabled: enabled ?? () => true,
    volume: volume ?? () => 0.35,
    load: (e) async => Pcm(Float32List(e.index + 1), 44100),
  );
}

const words = {'call.restInChat': 'Der Rest steht im Chat.', 'call.detailsInChat': 'Die Details stehen im Chat.'};

CallController fakeCall(
  FakeEars ears,
  FakeBackend backend,
  FakeSpeaker speaker, {
  Duration fillerAfter = const Duration(milliseconds: 800),
  CallTuning tuning = const CallTuning(window: Duration.zero),
  CallSounds? sounds,
  StartTimer? startTimer,
  CallGreeter? greeter,
  Duration serverBound = const Duration(milliseconds: 200),
}) => CallController(
  backend: backend,
  ears: ears,
  speaker: speaker,
  agentId: 'agent-1',
  appLanguage: 'de',
  words: (k) => words[k] ?? k,
  fillerAfter: fillerAfter,
  agentName: 'Ada Lovelace',
  tuning: tuning,
  sounds: sounds,
  startTimer: startTimer,
  greeter: greeter,
  serverBound: serverBound,
);

/// A clock a test moves by hand, for the timers a call's fillers start
/// ([StartTimer]).
class FakeClock {
  Duration now = Duration.zero;
  final _timers = <_FakeTimer>[];

  Timer start(Duration after, void Function() fire) {
    final t = _FakeTimer(now + after, fire);
    _timers.add(t);
    return t;
  }

  /// Moves on by [d], firing every timer due on the way, in order, and
  /// letting what each starts run before the next.
  Future<void> advance(Duration d) async {
    final end = now + d;
    while (true) {
      final due = _timers.where((t) => t.isActive && t.at <= end).toList()..sort((a, b) => a.at.compareTo(b.at));
      if (due.isEmpty) break;
      final t = due.first;
      now = t.at;
      t.active = false;
      t.fire();
      for (var i = 0; i < 10; i++) {
        await Future<void>.delayed(Duration.zero);
      }
    }
    now = end;
    for (var i = 0; i < 10; i++) {
      await Future<void>.delayed(Duration.zero);
    }
  }
}

class _FakeTimer implements Timer {
  _FakeTimer(this.at, this.fire);
  final Duration at;
  final void Function() fire;
  bool active = true;

  @override
  void cancel() => active = false;

  @override
  bool get isActive => active;

  @override
  int get tick => 0;
}
