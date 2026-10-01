import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';

import '../api.dart';
import '../diagnostics.dart';
import '../dictation.dart' show isHallucination;
import '../live.dart';
import '../models.dart';
import '../prefs.dart';
import '../speech_model.dart';
import 'capture.dart' show bargeInConfirm, doubleTalkMargin, echoWarmUp, loudnessDb, overPlayback;
import 'ears.dart';
import 'fillers.dart';
import 'greeting.dart';
import 'recognition.dart';
import 'recording.dart';
import 'sounds.dart';
import 'speech_text.dart';
import 'turns.dart';
import 'understood.dart';
import 'voice.dart';
import 'wav.dart';
import 'spoken_voice.dart';

String _base(String language) => language.split(RegExp('[-_]')).first.toLowerCase();

/// How many words a reply needs before its own language can change the
/// call's; shorter ones are too easily read as the wrong language.
const minWordsToSwitchLanguage = 8;

/// Calls as a trial (#494): the Mac only, behind an app setting that is on
/// by default while it is a trial. The setting is the device's, not the
/// organisation's — nothing on the instance changes. Beside it, the call's
/// tuning (#498): the pause that ends a turn, how long the person has to
/// speak before the agent stops, how long a turn stands as understood
/// before it is sent, and whether turns are recorded for diagnostics.
class CallSettings {
  CallSettings._();

  static const _key = 'call.enabled';

  /// Whether this build can call: the Mac, where the system's speech
  /// synthesis is reached through the app's own channel.
  static bool get supported => debugSupported ?? (!kIsWeb && Platform.isMacOS);

  @visibleForTesting
  static bool? debugSupported;

  static final enabled = ValueNotifier<bool>(true);

  /// The defaults are the values the trial started with (#494), but the
  /// pause: 0.7 s cut people off mid-sentence, so it is 1 s (#511).
  static const defaultPause = Duration(milliseconds: 1000);
  static const defaultBargeIn = Duration(milliseconds: 400);
  static const defaultWindow = Duration(milliseconds: 1500);

  /// The pause that ends a turn.
  static final pause = ValueNotifier<Duration>(defaultPause);

  /// How long the person has to speak while the agent speaks before it
  /// stops: shorter bursts are its own voice from the loudspeaker.
  static final bargeIn = ValueNotifier<Duration>(defaultBargeIn);

  /// How long a recognised turn stands as understood before it is sent;
  /// zero sends it at once.
  static final window = ValueNotifier<Duration>(defaultWindow);

  /// Every turn's audio and text kept on this Mac for diagnostics
  /// ([CallRecording]); off unless switched on.
  static final record = ValueNotifier<bool>(false);

  /// The call's short sounds (#500): on, quiet, unless changed.
  static final sounds = ValueNotifier<bool>(true);
  static const defaultVolume = 0.35;

  /// The sounds' volume, 0–1, under the voice's.
  static final volume = ValueNotifier<double>(defaultVolume);

  /// Whether the agent greets when the call connects (#506): on unless
  /// switched off.
  static final greeting = ValueNotifier<bool>(true);

  /// Whether the agent may hang up after its goodbye when the person ends
  /// the conversation (#517): on unless switched off.
  static final hangUp = ValueNotifier<bool>(true);

  static const _pauseKey = 'call.pause', _bargeInKey = 'call.bargeIn', _windowKey = 'call.window';
  static const _recordKey = 'call.record';
  static const _soundsKey = 'call.sounds', _volumeKey = 'call.volume';
  static const _greetingKey = 'call.greeting';
  static const _hangUpKey = 'call.agentHangsUp';

  static Future<void> load() async {
    try {
      final p = Prefs.instance;
      enabled.value = (await p.read(_key)) != 'off';
      pause.value = _ms(await p.read(_pauseKey)) ?? defaultPause;
      bargeIn.value = _ms(await p.read(_bargeInKey)) ?? defaultBargeIn;
      window.value = _ms(await p.read(_windowKey)) ?? defaultWindow;
      record.value = await p.read(_recordKey) == 'on';
      sounds.value = await p.read(_soundsKey) != 'off';
      final v = double.tryParse(await p.read(_volumeKey) ?? '');
      volume.value = v == null || v < 0 || v > 1 ? defaultVolume : v;
      greeting.value = await p.read(_greetingKey) != 'off';
      hangUp.value = await p.read(_hangUpKey) != 'off';
    } catch (_) {
      // Unreadable: the defaults.
    }
    // What is older than a week goes, whether or not a call comes.
    if (record.value) {
      try {
        await CallRecording.open();
      } catch (_) {}
    }
  }

  static Duration? _ms(String? v) {
    final n = int.tryParse(v ?? '');
    return n == null || n < 0 ? null : Duration(milliseconds: n);
  }

  static Future<void> _write(String key, String value) async {
    try {
      await Prefs.instance.write(key, value);
    } catch (_) {
      // Kept for this run.
    }
  }

  static Future<void> setEnabled(bool on) async {
    enabled.value = on;
    await _write(_key, on ? 'on' : 'off');
  }

  static Future<void> setPause(Duration d) async {
    pause.value = d;
    await _write(_pauseKey, '${d.inMilliseconds}');
  }

  static Future<void> setBargeIn(Duration d) async {
    bargeIn.value = d;
    await _write(_bargeInKey, '${d.inMilliseconds}');
  }

  static Future<void> setWindow(Duration d) async {
    window.value = d;
    await _write(_windowKey, '${d.inMilliseconds}');
  }

  static Future<void> setRecord(bool on) async {
    record.value = on;
    await _write(_recordKey, on ? 'on' : 'off');
    // Switched off, nothing recorded is kept any longer than asked.
    if (!on) await CallRecording.deleteAll();
  }

  static Future<void> setSounds(bool on) async {
    sounds.value = on;
    await _write(_soundsKey, on ? 'on' : 'off');
  }

  static Future<void> setGreeting(bool on) async {
    greeting.value = on;
    await _write(_greetingKey, on ? 'on' : 'off');
  }

  static Future<void> setHangUp(bool on) async {
    hangUp.value = on;
    await _write(_hangUpKey, on ? 'on' : 'off');
  }

  static Future<void> setVolume(double v) async {
    volume.value = v.clamp(0.0, 1.0);
    await _write(_volumeKey, '${volume.value}');
  }

  /// The tuning as a call starts with it.
  static CallTuning get tuning =>
      CallTuning(pause: pause.value, bargeIn: bargeIn.value, window: window.value, record: record.value);
}

/// The thresholds a call runs with (#498).
class CallTuning {
  const CallTuning({
    this.pause = CallSettings.defaultPause,
    this.bargeIn = CallSettings.defaultBargeIn,
    this.window = CallSettings.defaultWindow,
    this.record = false,
  });

  final Duration pause;
  final Duration bargeIn;
  final Duration window;
  final bool record;

  Map<String, Object?> toJson() => {
    'pause_ms': pause.inMilliseconds,
    'barge_in_ms': bargeIn.inMilliseconds,
    'window_ms': window.inMilliseconds,
  };
}

/// What the clean-up of a turn is told about the conversation (#498), so
/// names of agents, colleagues and systems come out right.
class CleanContext {
  const CleanContext({required this.agentName, this.names = const [], this.recent = const []});

  final String agentName;

  /// Other names that may be said: the conversation's members, the
  /// organisation's agents.
  final List<String> names;

  /// The last few lines of the conversation, oldest first, as `who: text`.
  final List<String> recent;

  /// The dictation clean-up's context fields (#362): where the text goes,
  /// the names, and the conversation before it — within its bounds.
  Map<String, String> toFields() {
    String cap(String s, int n) => s.length <= n ? s : s.substring(0, n);
    var before = recent.join('\n');
    if (before.isNotEmpty) before = '$before\n';
    final runes = before.runes.toList();
    if (runes.length > 600) before = String.fromCharCodes(runes.sublist(runes.length - 600));
    return {
      'window': cap('Spoken in a call with $agentName', 300),
      if (names.isNotEmpty) 'field': cap('Names in this conversation: ${names.join(', ')}', 300),
      if (before.isNotEmpty) 'before': before,
    };
  }
}

/// The seam to the instance: the direct conversation with the agent, what
/// is new in it, and writing into it.
abstract class CallBackend {
  /// Opens the conversation and returns what is in it now.
  Future<List<ConversationMessage>> open();

  /// The messages after [lastId].
  Future<List<ConversationMessage>> after(String lastId);

  /// Writes what the person said, marked as said in a call.
  Future<ConversationMessage> post(String text);

  /// The spoken form of the reply to the person's message [messageId],
  /// sentence by sentence while the instance writes it (#529); empty when
  /// it streams none.
  Stream<String> spokenReply(String messageId);

  /// Marks the conversation read up to [at]: what the call fetched is heard,
  /// and the instance sends no notification for it (#525).
  Future<void> read(DateTime at);

  /// Fires when the conversation may have moved.
  Stream<void> changes();

  /// A recognised turn cleaned up (#498), or null — the instance cannot, the
  /// request failed or took too long: the turn is sent as recognised.
  Future<String?> clean(String text, CleanContext context);

  /// The names that may come up in the conversation, for [clean].
  Future<List<String>> names();

  /// How the agent sounds at the voice provider in this conversation
  /// (#497): its covey voice's spoken voice and style hint. Nothing set on
  /// an instance from before them.
  Future<SpokenVoice> spokenVoice();

  /// What the greeting says (#506, #528): the person's name and the chat
  /// tone's address. Whatever cannot be read is left empty; it never
  /// throws.
  Future<GreetingFacts> greetingFacts();

  /// Whether the organisation lets this call's turns be recognised at its
  /// voice provider (#516), as `/speech/model` says. False on an instance
  /// from before it, and when it cannot be read; it never throws.
  Future<bool> transcribes();

  /// One turn recognised at the voice provider (#516): [wav] is the turn as
  /// the voice detector cut it, [language] the call's. Throws when it cannot
  /// be; the call then takes the device's text.
  Future<String> transcribe(Uint8List wav, {required String language});
}

/// The conversation API (#440, #447) for one agent's direct conversation.
class ApiCallBackend implements CallBackend {
  ApiCallBackend(this.api, this.agentId, {this.language = ''});

  final CoveyApi api;
  final String agentId;

  /// The call's language, so the voice assigned to the agent is one of that
  /// language (#518); empty leaves it to the server.
  final String language;
  String? _id;
  List<ConversationMember> _members = const [];

  /// The direct conversation, opened once: the greeting asks about it while
  /// the models still load (#506), before the call reads it.
  Future<Conversation>? _direct;

  Future<Conversation> _conversation() => _direct ??= api
      .openDirect(MemberRef('agent', agentId))
      .then(
        (c) {
          _id = c.id;
          _members = c.members;
          return c;
        },
        onError: (Object e) {
          _direct = null;
          throw e;
        },
      );

  /// GET /conversations/{id}/speech, asked once for the voice and the
  /// greeting.
  Future<Map<String, dynamic>>? _speech;

  Future<Map<String, dynamic>> _speechOf() => _speech ??= () async {
    final c = await _conversation();
    return await api.get(
          '/conversations/${c.id}/speech?agent=${Uri.encodeQueryComponent(agentId)}'
          '${language.isEmpty ? '' : '&lang=${Uri.encodeQueryComponent(language)}'}',
        )
        as Map<String, dynamic>;
  }();

  /// Set once the instance answered that it cannot clean up: not asked
  /// again for this call.
  bool _noClean = false;

  String get _conv => _id ?? (throw StateError('not open'));

  @override
  Future<List<ConversationMessage>> open() async {
    await _conversation();
    return (await api.conversationMessages(_conv)).messages;
  }

  @override
  Future<List<ConversationMessage>> after(String lastId) async {
    final out = <ConversationMessage>[];
    var from = lastId;
    for (var i = 0; i < 10; i++) {
      final page = await api.conversationMessages(_conv, after: from);
      out.addAll(page.messages);
      if (!page.more || page.messages.isEmpty) break;
      from = page.messages.last.id;
    }
    return out;
  }

  @override
  Future<ConversationMessage> post(String text) => api.postConversationMessage(_conv, text, via: 'call');

  @override
  Future<void> read(DateTime at) => api.markConversationRead(_conv, at);

  @override
  Stream<String> spokenReply(String messageId) => api.spokenReply(_conv, messageId);

  /// A short settle (#527): the reply is waited for, and every 100 ms of
  /// it is heard as silence.
  @override
  Stream<void> changes() =>
      LiveEvents.instance.of({'chat'}, conversationId: _id, settle: const Duration(milliseconds: 40));

  @override
  Future<List<String>> names() async {
    final out = <String>{for (final m in _members) m.name};
    try {
      for (final a in await api.agents()) {
        if (a.isColleague) out.add(a.displayName);
      }
    } on ApiException {
      // The members alone.
    }
    return [
      for (final n in out)
        if (n.trim().isNotEmpty) n.trim(),
    ];
  }

  @override
  Future<String?> clean(String text, CleanContext context) async {
    // The instance says whether it can (#355); an older one does not say.
    if (_noClean || SpeechModel.instance.info?.clean == false) return null;
    try {
      return await api
          .cleanDictation(text, app: 'covey call', context: context.toFields(), turn: true)
          .timeout(const Duration(seconds: 4));
    } on ApiException catch (e) {
      // No endpoint (404) or no credential (409): not again this call.
      if (e.status == 404 || e.status == 409) _noClean = true;
      diag('call', 'clean-up skipped: ${e.status}');
      return null;
    } on TimeoutException {
      diag('call', 'clean-up took too long');
      return null;
    }
  }

  @override
  Future<SpokenVoice> spokenVoice() async {
    try {
      return SpokenVoice.fromConversation(await _speechOf());
    } on ApiException {
      // An instance from before spoken voices: the provider's default.
      return const SpokenVoice();
    }
  }

  @override
  Future<GreetingFacts> greetingFacts() async {
    Future<String> read(Future<String> Function() f) async {
      try {
        return await f();
      } catch (e) {
        diag('call', 'greeting without a detail: $e');
        return '';
      }
    }

    final got = await Future.wait([
      read(() async => (await api.me()).displayName),
      // An instance from before #506 does not say: the greeting's default.
      read(() async => (await _speechOf())['address'] as String? ?? ''),
    ]);
    return GreetingFacts(personName: got[0], address: got[1]);
  }

  @override
  Future<bool> transcribes() async {
    try {
      return (await api.speechModel()).transcribe;
    } catch (e) {
      diag('call', 'whether the voice provider recognises is not known: $e');
      return false;
    }
  }

  @override
  Future<String> transcribe(Uint8List wav, {required String language}) =>
      api.transcribeSpeech(wav, language: language, agent: agentId);
}

/// Where a call stands, as the face and the line under it show it.
enum CallMode {
  /// Models are fetched and loaded.
  preparing,

  /// The call listens; nobody speaks.
  listening,

  /// The person speaks, or what they said is being recognised.
  hearing,

  /// A recognised turn stands as understood, about to be sent (#498).
  understood,

  /// Waiting for the agent's reply.
  thinking,

  /// The agent's reply is spoken.
  speaking,

  /// The person muted the call.
  muted,

  failed,
  ended,
}

/// One line of the call's transcript.
class CallLine {
  CallLine({required this.mine, required this.text});
  final bool mine;
  final String text;
}

class _Utterance {
  _Utterance(this.text, {this.plain = false});
  final String text;

  /// The app's own words (still working on it, the rest is in the chat):
  /// said in the app's language, not cleaned, not cut.
  final bool plain;
}

/// A hands-free call with one agent (#494).
///
/// The microphone runs while the call is open. The voice detector and
/// [TurnSegmenter] cut a turn at a pause ([CallTuning.pause]), the
/// recogniser turns it into text on the device, the instance cleans it up
/// with the conversation as context, and the call shows it as understood
/// for a moment ([UnderstoodTurn]) before it goes into the direct
/// conversation as an ordinary message, marked `via: call` — so the chat's
/// own path answers it (the triage: an answer, a note to a task, a task).
/// Every message the agent writes into the conversation while the call is
/// open is spoken: the answer, the acknowledgement of a task, and the task's
/// result when it arrives later. When the person speaks while the agent
/// does, the agent stops.
///
/// Short sounds mark what happens (#500): ringing while the call connects,
/// connected, a turn heard, a task created, mute, unmute, hang-up. While the
/// agent thinks, it says one short filler in its own voice ([CallFillers]).
///
/// When the call connects, the agent greets (#506): a greeting the instance
/// writes from the situation (#513), asked as the line starts ringing and
/// synthesised as soon as it arrives, or a template greeting ([CallGreeter])
/// when it is not there in time ([GreetingInFlight]); said after the
/// connected sound; the person speaking stops it. It is part of the call,
/// not a message in the conversation.
///
/// When the person ends the conversation by voice, the agent's answer is its
/// goodbye, marked `end_call` (#517): the call speaks it, plays the hang-up
/// sound and ends. The person speaking during the goodbye keeps it open.
class CallController extends ChangeNotifier {
  CallController({
    required this.backend,
    required this.ears,
    required this.speaker,
    required this.agentId,
    required this.appLanguage,
    required this.words,
    this.agentName = '',
    this.tuning = const CallTuning(),
    this.recording,
    this.sounds,
    this.fillerAfter = const Duration(milliseconds: 300),
    this.pollEvery = const Duration(seconds: 5),
    this.serverBound = serverRecognitionBound,
    this.mayHangUp,
    this.hush,
    StartTimer? startTimer,
    this.greeter,
  }) : _timer = startTimer ?? Timer.new {
    _turns = TurnSegmenter(
      onSpeech: _onSpeech,
      onTurn: _onTurn,
      onDiscard: _onDiscard,
      onLull: _onLull,
      lullAfter: lullAfter(tuning.pause),
      endSilence: tuning.pause,
    );
    _fillers = CallFillers(
      voice: speaker,
      // The typing (#526), with the call's other sounds: off when they are.
      sound: () async => await sounds?.take(Earcon.typing),
      quiet: () => ended || muted || _speaking || _turns.speaking,
      after: fillerAfter,
      timer: startTimer,
    );
  }

  final CallBackend backend;
  final CallEars ears;
  final Speaker speaker;
  final String agentId;
  final String agentName;

  /// The app's language, for the voice until a reply says otherwise and
  /// for the app's own words.
  final String appLanguage;

  /// The app's own spoken words, by key: `call.restInChat`,
  /// `call.detailsInChat`.
  final String Function(String key) words;

  final CallTuning tuning;

  /// Opens the diagnostics recording when [CallTuning.record] is on; null
  /// records nothing.
  final Future<CallRecording> Function()? recording;

  /// The call's sounds; null plays none.
  final CallSounds? sounds;

  /// How long after the end of a turn was heard the typing starts — only
  /// while the reply's message has not arrived (#511, #526). It starts
  /// before the turn is sent: recognition, clean-up and the understood
  /// window take seconds, and the call is silent no longer than this.
  final Duration fillerAfter;

  /// The net under the event stream while a reply is awaited.
  final Duration pollEvery;

  /// How long a turn waits for the voice provider's text before it takes
  /// the device's (#516).
  final Duration serverBound;

  /// Whether the call ends after the agent's goodbye (#517), asked when the
  /// goodbye arrives; null: it does.
  final bool Function()? mayHangUp;

  /// Told true when the call starts and false when it ends (#525): the
  /// app's notifications hold still in between. Null tells nobody.
  final void Function(bool on)? hush;

  /// How the call greets; null does not.
  final CallGreeter? greeter;

  final StartTimer _timer;

  late final TurnSegmenter _turns;
  late final CallFillers _fillers;

  CallMode _mode = CallMode.preparing;
  CallMode get mode => _mode;

  /// What went wrong, when [mode] is [CallMode.failed].
  CallException? failure;

  bool muted = false;

  /// The person's voice, 0–1, smoothed.
  double level = 0;

  /// Counts the words the system's synthesiser reached: the face opens its
  /// mouth on each. The voice provider drives it by [Speaker.level] instead.
  final wordTicks = ValueNotifier<int>(0);

  /// The last lines said, oldest first.
  final lines = <CallLine>[];
  static const _keepLines = 4;

  /// The turn standing as understood, while it does.
  UnderstoodTurn? get understood => _understood;
  UnderstoodTurn? _understood;

  /// Whether turns are being recorded for diagnostics in this call.
  bool get recordingTurns => _recording != null;
  CallRecording? _recording;

  bool get ended => _mode == CallMode.ended;

  /// Whether this call's turns are recognised at the organisation's voice
  /// provider (#516), the device's recogniser beside it as the fallback. The
  /// call view says so while it is on; a provider that refuses turns it off
  /// for the rest of the call.
  bool get serverRecognition => _serverRecognition;
  bool _serverRecognition = false;

  final _queue = <_Utterance>[];
  bool _speaking = false;
  bool _recognising = false;
  bool _awaiting = false;
  bool _bargedIn = false;

  Timer? _poll;
  Timer? _giveUp;
  StreamSubscription<void>? _changes;
  StreamSubscription<SpeakingEvent>? _spoken;
  String? _lastId;
  final _seen = <String>{};
  String? _language;

  /// The tasks the conversation already knew of: a message of the agent
  /// naming another is the acknowledgement of a task just created.
  final _tasks = <String>{};

  /// The conversation's last lines, for the clean-up's context.
  final _recent = <String>[];
  static const _keepRecent = 6;
  List<String> _names = const [];

  final _started = DateTime.now();
  int _turnNo = 0;

  /// What was spoken last and until when, to tell the agent's own voice
  /// from the loudspeaker apart from the person.
  String _saying = '';
  DateTime _saidUntil = DateTime.fromMillisecondsSinceEpoch(0);

  /// The greeting is still to come: the connected sound plays, or its audio
  /// is awaited. The person speaking meanwhile drops it ([_greetingDropped]).
  bool _greetingPending = false;
  bool _greetingDropped = false;

  /// The agent's goodbye is queued or being said (#517): the call hangs up
  /// once it has been said, unless the person speaks meanwhile.
  bool _goodbye = false;

  /// Whether the call is about to hang up after the agent's goodbye.
  bool get endingAfterGoodbye => _goodbye;

  Future<void> _turnChain = Future.value();
  bool _fetching = false;
  bool _fetchAgain = false;

  /// Opens the call: loads the models, opens the conversation, starts
  /// hearing.
  Future<void> start() async {
    _set(CallMode.preparing);
    _hush(true);
    unawaited(sounds?.play(Earcon.ringing, loops: 2));
    unawaited(sounds?.preload());
    // Asked for and synthesised while the line rings and the models load.
    final greeting = (greeter?.enabled() ?? false) ? _composeGreeting() : null;
    // Asked while the models load; the answer is needed at the first turn.
    final transcribes = backend.transcribes().catchError((Object _) => false);
    try {
      await ears.prepare();
      // Hung up while the models loaded: what just loaded is freed again.
      if (ended) return await ears.close();
      final had = await backend.open();
      _serverRecognition = await transcribes;
      if (_serverRecognition) diag('call', 'turns recognised at the voice provider, on this Mac as the fallback');
      for (final m in had) {
        _seen.add(m.id);
        _remember(m);
        if (m.authorKind == 'agent' && m.taskId != null) _tasks.add(m.taskId!);
      }
      if (had.isNotEmpty) _lastId = had.last.id;
      final voices = await speaker.prepare(language: appLanguage);
      if (ended) return;
      unawaited(backend.names().then((n) => _names = n, onError: (_) {}));
      if (tuning.record && recording != null) {
        try {
          _recording = await recording!();
        } catch (e) {
          diag('call', 'no diagnostics recording: $e');
        }
      }
      _spoken = speaker.events.listen((e) {
        if (e == SpeakingEvent.word) wordTicks.value++;
        // The reply is heard: a filler makes way.
        if (e == SpeakingEvent.started) _fillers.replyAudio();
      });
      _changes = backend.changes().listen((_) => _fetch());
      _poll = Timer.periodic(pollEvery, (_) {
        if (_awaiting) _fetch();
      });
      await ears.listen(_onWindow);
      // Hung up while the microphone opened: nothing may keep it open.
      if (ended) return await ears.close();
      diag(
        'call',
        'open, $voices; pause ${tuning.pause.inMilliseconds} ms, barge-in ${tuning.bargeIn.inMilliseconds} ms, '
            'window ${tuning.window.inMilliseconds} ms${_recording == null ? '' : ', recording turns'}',
      );
      final connected = sounds?.play(Earcon.connected) ?? Future.value(Duration.zero);
      _set(CallMode.listening);
      if (greeting != null) unawaited(_greet(greeting, connected));
    } on CallException catch (e) {
      _fail(e);
    } on ApiException catch (e) {
      _fail(CallException(CallProblem.unavailable, e.message));
    } catch (e) {
      _fail(CallException(CallProblem.unavailable, '$e'));
    }
  }

  /// The greeting for this call on its way: chosen and synthesised while
  /// the line rings.
  GreetingInFlight _composeGreeting() {
    return GreetingInFlight(
      template: () async {
        final facts = await backend.greetingFacts();
        if (ended) return null;
        return greeter!.compose(agentId: agentId, language: appLanguage, facts: facts);
      },
      synthesise: (g) => ended ? Future.value(false) : speaker.prepareUtterance(g.text, language: g.language),
      log: (what) => diag('call', what),
    );
  }

  /// The greeting's waits, cancelled when it is said or dropped.
  final _greetingTimers = <Timer>[];

  Future<void> _after(Duration d) {
    final c = Completer<void>();
    _greetingTimers.add(_timer(d, c.complete));
    return c.future;
  }

  /// Says the greeting once the connected sound has played: in the voice
  /// provider's audio when it is ready by then, or within [CallGreeter.wait]
  /// after; otherwise the Mac's voice says it. Listening goes on under it,
  /// so the person can interrupt.
  Future<void> _greet(GreetingInFlight greeting, Future<Duration> connected) async {
    _greetingPending = true;
    _greetingDropped = false;
    try {
      final sound = await connected;
      if (sound > Duration.zero) await _after(sound);
      if (ended || _greetingDropped) return;
      final c = await greeting.pick(_after(greeter!.wait));
      if (c == null || ended || _greetingDropped) return;
      final (g, ready) = c;
      unawaited(greeter!.remember(agentId, g));
      diag('call', ready ? '$g' : '$g in the Mac voice: the provider\'s audio was not ready');
      _greetingPending = false;
      _speaking = true;
      _saying = g.text;
      _say(CallLine(mine: false, text: g.text));
      _update();
      try {
        await speaker.speakPrepared(g.text, language: g.language, ready: ready);
      } catch (e) {
        diag('call', 'greeting failed: $e');
      }
      _speaking = false;
      _saidUntil = DateTime.now();
    } finally {
      for (final t in _greetingTimers) {
        t.cancel();
      }
      _greetingTimers.clear();
      _greetingPending = false;
      _update();
      unawaited(_speakNext());
    }
  }

  void _fail(CallException e) {
    if (ended) return;
    diag('call', 'failed: $e');
    unawaited(sounds?.stop());
    failure = e;
    _hush(false);
    unawaited(_shut());
    _set(CallMode.failed);
  }

  bool _hushed = false;

  /// Tells [hush], once for each change.
  void _hush(bool on) {
    if (_hushed == on) return;
    _hushed = on;
    hush?.call(on);
  }

  /// Mutes or unmutes: muted, the microphone is closed.
  Future<void> setMuted(bool on) async {
    if (ended || _mode == CallMode.failed || _mode == CallMode.preparing) return;
    if (muted != on) unawaited(sounds?.play(on ? Earcon.mute : Earcon.unmute));
    // Nothing is said into a muted call.
    if (on) _fillers.stop();
    muted = on;
    level = 0;
    _turns.reset();
    if (on) {
      await ears.pause();
    } else {
      await ears.listen(_onWindow);
    }
    _update();
  }

  /// Ends the call: the microphone closed, speaking stopped, nothing left
  /// running.
  Future<void> hangUp() async {
    if (ended) return;
    final wasOpen = _mode != CallMode.failed;
    _mode = CallMode.ended;
    if (!_disposed) notifyListeners();
    if (wasOpen) unawaited(sounds?.play(Earcon.hangUp));
    _hush(false);
    await _shut();
    diag('call', 'ended');
  }

  Future<void> _shut() async {
    _queue.clear();
    _ahead = null;
    _stopSpoken();
    _streamed.clear();
    _cutStreams.clear();
    _fillers.stop();
    _poll?.cancel();
    _giveUp?.cancel();
    _understood?.discard();
    // Not awaited: a cancelled subscription's future may belong to another
    // zone, and nothing here depends on it having finished.
    unawaited(_changes?.cancel());
    unawaited(_spoken?.cancel());
    _changes = _spoken = null;
    _turns.reset();
    try {
      await speaker.stop();
    } catch (_) {}
    try {
      await ears.close();
    } catch (e) {
      diag('call', 'closing the microphone: $e');
    }
  }

  DateTime _levelAt = DateTime.fromMillisecondsSinceEpoch(0);

  void _onWindow(Uint8List pcm, bool voiced, double l) {
    if (ended || muted) return;
    // While the person corrects a turn at the keyboard, the call does not
    // listen: the keys would be heard as a turn.
    if (_understood?.editing ?? false) return;
    // The agent's voice from the loudspeaker must not interrupt it: while
    // it speaks, the person has to be heard for a moment first — shorter
    // when the microphone is echo-cancelled (#507), which covers what the
    // call's own engine plays, not the Mac's synthesiser. Not during the
    // greeting: it comes before the canceller has learnt the room.
    final warming = _echoWarming;
    _turns.confirm = _greetingPending
        ? tuning.bargeIn
        : _speaking || _fillers.playing
        ? bargeInConfirm(tuning.bargeIn, echoCancelled: _echoCancelled, warmingUp: warming)
        : Duration.zero;
    if (warming && !_warmUpLogged && (_speaking || _fillers.playing)) {
      _warmUpLogged = true;
      diag('call', 'echo canceller settling: barge-in at the tuned ${tuning.bargeIn.inMilliseconds} ms');
    }
    // Double-talk (#511): while the call plays something, a window counts
    // as the person only when it is louder than what plays by a margin —
    // what of the agent's voice gets past the canceller is quieter.
    if (voiced && !_turns.speaking && (_greetingPending || _speaking || _fillers.playing)) {
      final played = speaker.playbackDb;
      final heard = loudnessDb(l);
      if (!overPlayback(heard, played)) {
        voiced = false;
        if (!_doubleTalkLogged) {
          _doubleTalkLogged = true;
          diag(
            'call',
            'voice under what plays: ${heard.toStringAsFixed(0)} dB against ${played!.toStringAsFixed(0)} dB '
                '+ ${doubleTalkMargin.toStringAsFixed(0)} — not a barge-in',
          );
        }
      }
    }
    _turns.add(pcm, voiced);
    level = l > level ? l : level * 0.85 + l * 0.15;
    final now = DateTime.now();
    if (now.difference(_levelAt) > const Duration(milliseconds: 40)) {
      _levelAt = now;
      _update();
    }
  }

  bool get _echoCancelled => ears.echoCancelled && !speaker.fallback.value;

  /// Voice processing started less than [echoWarmUp] ago (#511).
  bool get _echoWarming {
    final since = ears.echoCancelledSince;
    return _echoCancelled && since != null && DateTime.now().difference(since) < echoWarmUp;
  }

  bool _warmUpLogged = false;

  /// Logged once per utterance: a window held back as the agent's own voice.
  bool _doubleTalkLogged = false;

  void _onSpeech() {
    // The person speaks: a filler would talk over them, and a greeting
    // still to come is not said any more.
    _fillers.stop();
    // Nor does the call hang up after a goodbye they speak into (#517).
    if (_goodbye) {
      _goodbye = false;
      diag('call', 'hang-up cancelled: the person spoke during the goodbye');
    }
    if (_greetingPending && !_greetingDropped) {
      diag('call', 'greeting dropped: the person spoke first');
      _greetingDropped = true;
    }
    if (_speaking) {
      // Barge-in: the person speaks, the agent stops mid-sentence, and what
      // it had still to say is dropped — it stands in the chat.
      final played = speaker.playbackDb;
      diag(
        'call',
        'barge-in, microphone ${loudnessDb(level).toStringAsFixed(0)} dB'
            '${played == null ? '' : ', playing ${played.toStringAsFixed(0)} dB'}',
      );
      _bargedIn = true;
      _queue.clear();
      unawaited(speaker.stop());
      // Nor is the rest of a reply still being written (#529).
      final cut = _spokenFor;
      if (cut != null && _streamed.containsKey(cut)) _cutStreams.add(cut);
      _stopSpoken();
    }
    _update();
  }

  /// The person's message whose reply is being streamed (#529), and what
  /// of the replies to the person's messages was spoken from a stream.
  String? _spokenFor;
  StreamSubscription<String>? _spokenSub;
  final _streamed = <String, String>{};

  /// The person's last message, whose reply is awaited: a reply to an
  /// earlier one does not end the wait (#531).
  String? _posted;

  /// Replies whose stream a barge-in cut: their message is not spoken.
  final _cutStreams = <String>{};

  /// Listens for the reply to [messageId] while the instance writes it,
  /// and speaks each sentence as it comes.
  void _listenSpoken(String messageId) {
    _stopSpoken();
    _spokenFor = messageId;
    _spokenSub = backend
        .spokenReply(messageId)
        .listen(
          (sentence) => _onSpoken(messageId, sentence),
          onError: (Object e) => diag('call', 'spoken reply: $e'),
          onDone: () {
            if (_spokenFor == messageId) _spokenFor = null;
            // The instance writes the reply's message before it closes the
            // stream: read it now rather than on an event that may not come
            // (#531).
            if (!ended) unawaited(_fetch());
          },
        );
  }

  void _stopSpoken() {
    unawaited(_spokenSub?.cancel());
    _spokenSub = null;
    _spokenFor = null;
  }

  void _onSpoken(String messageId, String sentence) {
    if (ended || _spokenFor != messageId) return;
    final before = _streamed[messageId];
    if (before == null) {
      // The reply has begun: the wait is over, though its message is not
      // written yet.
      _awaiting = false;
      _fillers.replyArrived();
      _giveUp?.cancel();
      diag('call', 'reply streaming');
    }
    _streamed[messageId] = before == null ? sentence : '$before $sentence';
    _queue.add(_Utterance(sentence));
    _update();
    unawaited(_speakNext());
  }

  /// What of [m] is still to be said when its reply was streamed: what
  /// follows the streamed sentences in its spoken form, and the word about
  /// the chat. When it says something else — a check dropped the spoken
  /// form, and the written one stands — what was heard stays the answer.
  List<_Utterance> _afterStream(ConversationMessage m, String streamed) {
    final all = _toSpeak(m);
    if (all.isEmpty) return all;
    String norm(String t) => t.trim().split(RegExp(r'\s+')).join(' ');
    final first = norm(all.first.text), heard = norm(streamed);
    final rest = first.startsWith(heard) ? first.substring(heard.length).trim() : '';
    return [if (rest.isNotEmpty) _Utterance(rest), ...all.skip(1)];
  }

  void _onDiscard() {
    _bargedIn = false;
    _ahead = null;
    _update();
  }

  /// The turn recognised ahead, in the person's pause before its end
  /// (#527): the length of its audio, and the work.
  ({int length, Future<_Heard> heard})? _ahead;

  /// The person paused partway to the end of a turn: what the turn will be
  /// unless they speak again is recognised — and cleaned up — now, so it is
  /// ready when the pause has run out. Speaking again makes another turn,
  /// of another length, and this one is not used.
  void _onLull(Uint8List pcm) {
    if (ended || muted) return;
    final heard = _hear(pcm);
    unawaited(heard.then((_) {}, onError: (_) {}));
    _ahead = (length: pcm.length, heard: heard);
  }

  /// One turn's audio recognised: its text, the language it was heard in,
  /// whether it goes nowhere, and its clean-up under way.
  Future<_Heard> _hear(Uint8List pcm) async {
    // At the voice provider when the organisation allows it (#516), the
    // device's recogniser alongside as the fallback.
    final heard = await recogniseTurn(
      device: () => ears.recognise(pcm),
      server: _serverRecognition ? () => _transcribe(pcm) : null,
      bound: serverBound,
    );
    final text = heard.text;
    if (text.isEmpty || isHallucination(text)) return _Heard(heard, dropped: 'nothing recognised');
    if ((DateTime.now().difference(_saidUntil) < const Duration(seconds: 2) || _speaking) &&
        looksLikeEcho(text, _saying)) {
      // The agent heard itself: what came in is what it had just said.
      return _Heard(heard, dropped: 'echo');
    }
    String? language;
    try {
      language = await speaker.language(text);
    } catch (_) {}
    // A short turn is not cleaned (#511): there is nothing to tidy in it,
    // and the clean-up only had the conversation to go on.
    if (turnWords(text).length < minCleanWords) return _Heard(heard, language: language);
    final watch = Stopwatch()..start();
    final clean = backend
        .clean(text, CleanContext(agentName: agentName, names: _names, recent: List.of(_recent)))
        .then((c) => c, onError: (Object _) => null)
        .then((c) => (c, watch.elapsedMilliseconds));
    return _Heard(heard, language: language, clean: clean);
  }

  void _onTurn(Uint8List pcm, TurnStats stats) {
    final bargedIn = _bargedIn;
    _bargedIn = false;
    unawaited(sounds?.play(Earcon.heard));
    _fillers.waiting();
    _turnChain = _turnChain.then((_) => _handleTurn(pcm, stats, bargedIn)).catchError((Object e) {
      diag('call', 'turn failed: $e');
    });
  }

  Future<void> _handleTurn(Uint8List pcm, TurnStats stats, bool bargedIn) async {
    if (ended) return;
    final n = ++_turnNo;
    final facts = <String, Object?>{
      'turn': n,
      'speech_ms': stats.speech.inMilliseconds,
      'silence_ms': stats.silence.inMilliseconds,
      'length_ms': stats.length.inMilliseconds,
      'cut': bargedIn ? 'barge-in' : stats.cut.name,
      'vad': {
        'windows': stats.windows,
        'voiced': stats.voicedWindows,
        'longest_run_ms': stats.longestRun.inMilliseconds,
      },
      'thresholds': {
        ...tuning.toJson(),
        'min_speech_ms': _turns.minSpeech.inMilliseconds,
        'max_turn_ms': _turns.maxTurn.inMilliseconds,
        'echo_cancelled': _echoCancelled,
      },
    };
    // Recognised ahead in the pause when the turn is what was heard then.
    final ahead = _ahead;
    _ahead = null;
    final reused = ahead != null && ahead.length == pcm.length;
    facts['ahead'] = reused;
    _recognising = true;
    _update();
    final _Heard h;
    try {
      h = await (reused ? ahead.heard : _hear(pcm));
    } finally {
      _recognising = false;
    }
    final heard = h.heard;
    final text = heard.text;
    facts['recogniser'] = heard.by.name;
    facts['recognise_ms'] = heard.elapsed.inMilliseconds;
    if (heard.serverProblem != null) facts['server_problem'] = heard.serverProblem;
    facts['raw'] = text;
    if (ended) return;
    if (h.dropped != null) {
      _logTurn(n, pcm, facts..['outcome'] = h.dropped);
      // Nothing goes to the agent: nothing to wait for.
      _fillers.stop();
      _update();
      return;
    }

    // The recogniser may take a short German turn for English (#511):
    // Parakeet cannot be pinned to the call's language, so a turn heard in
    // another one is flagged here.
    final callLanguage = _base(_language ?? appLanguage);
    final heardIn = h.language;
    facts['call_language'] = callLanguage;
    if (heardIn != null) {
      facts['recognised_language'] = heardIn;
      if (_base(heardIn) != callLanguage) {
        diag('call', 'turn $n recognised as $heardIn, the call is in $callLanguage');
      }
    }

    // Understood: shown while the clean-up runs, then for the window.
    final clean = h.clean;
    final u = _understood = UnderstoodTurn(text, window: tuning.window, cleaning: clean != null);
    u.addListener(_update);
    _update();
    if (clean == null) {
      facts['clean_kept'] = 'short';
    } else {
      unawaited(
        clean.then((r) {
          var (c, ms) = r;
          facts['clean_ms'] = ms;
          if (c != null && c.trim().isNotEmpty) {
            // A correction changes little; a rewrite goes as recognised.
            final edit = turnEdit(text, c);
            facts['clean_edit'] = double.parse(edit.toStringAsFixed(2));
            if (edit > maxTurnEdit) {
              facts['clean_kept'] = 'edited';
              facts['clean_rejected'] = c;
              c = null;
            }
          }
          u.cleanedUp(c);
        }),
      );
    }
    final shownAt = DateTime.now();
    final (sent, how) = await u.result;
    u.removeListener(_update);
    if (identical(_understood, u)) _understood = null;
    facts['cleaned'] = u.cleaned;
    facts['outcome'] = how.name;
    facts['understood_ms'] = DateTime.now().difference(shownAt).inMilliseconds;
    facts['sent'] = sent;
    _logTurn(n, pcm, facts);
    if (ended || sent == null) {
      _fillers.stop();
      _update();
      return;
    }
    await _post(sent);
  }

  /// The turn at the voice provider, in the call's language. A provider
  /// that refuses (409: the organisation turned it off; 404: an instance
  /// from before it) is not asked again in this call.
  Future<String> _transcribe(Uint8List pcm) async {
    try {
      return await backend.transcribe(wavOfPcm16(pcm), language: _language ?? appLanguage);
    } on ApiException catch (e) {
      if (e.status == 409 || e.status == 404) {
        _serverRecognition = false;
        diag('call', 'the voice provider does not recognise (${e.status}): on this Mac for the rest of the call');
        _update();
      }
      rethrow;
    }
  }

  /// One line per turn in the diagnostics log — the text only while the
  /// diagnostics recording is on — and the turn in the recording.
  void _logTurn(int n, Uint8List pcm, Map<String, Object?> facts) {
    final raw = facts['raw'] as String? ?? '';
    final cleaned = facts['cleaned'] as String?;
    final sent = facts['sent'] as String?;
    final detail = _recording != null;
    diag(
      'call',
      'turn $n: speech ${facts['speech_ms']} ms, silence ${facts['silence_ms']} ms, length ${facts['length_ms']} ms, '
          'cut ${facts['cut']}, recognised by ${facts['recogniser']} in ${facts['recognise_ms']} ms'
          '${facts['ahead'] == true ? ' (ahead, in the pause)' : ''}'
          '${facts['server_problem'] == null ? '' : ' (voice provider: ${facts['server_problem']})'}, raw ${raw.length} characters'
          '${cleaned == null ? '' : ', cleaned ${cleaned.length} in ${facts['clean_ms']} ms'}'
          '${facts['clean_edit'] == null ? '' : ', word edit ${facts['clean_edit']}'}'
          '${facts['clean_kept'] == null ? '' : ', sent as recognised (${facts['clean_kept']})'}'
          '${sent == null ? '' : ', sent ${sent.length}'}, ${facts['outcome']}'
          '${detail ? ' · raw «$raw»${cleaned == null ? '' : ' · cleaned «$cleaned»'}' : ''}',
    );
    final r = _recording;
    if (r != null) {
      final stamp = _started.toIso8601String().replaceAll(RegExp(r'[:.]'), '-');
      unawaited(r.keep('$stamp-${n.toString().padLeft(3, '0')}', pcm, facts));
    }
  }

  Future<void> _post(String text) async {
    _say(CallLine(mine: true, text: text));
    _recent.add('Person: $text');
    _trimRecent();
    _awaiting = true;
    _fillers.waiting();
    _giveUp?.cancel();
    // Thinking does not last for ever: a task's result may come in an hour,
    // and it is spoken then whether or not the face still thinks.
    _giveUp = Timer(const Duration(minutes: 2), () {
      _awaiting = false;
      _update();
    });
    _update();
    try {
      final m = await backend.post(text);
      _seen.add(m.id);
      _posted = m.id;
      _listenSpoken(m.id);
      diag('call', 'turn posted, ${text.length} characters');
    } on ApiException catch (e) {
      _awaiting = false;
      _fillers.stop();
      _say(CallLine(mine: false, text: e.message));
      _update();
      return;
    }
    unawaited(_fetch());
  }

  void _remember(ConversationMessage m) {
    final t = m.text.trim();
    if (t.isEmpty) return;
    final who = m.authorKind == 'agent' ? (agentName.isEmpty ? 'Agent' : agentName) : 'Person';
    _recent.add('$who: ${t.length > 300 ? '${t.substring(0, 300)}…' : t}');
    _trimRecent();
  }

  void _trimRecent() {
    if (_recent.length > _keepRecent) _recent.removeRange(0, _recent.length - _keepRecent);
  }

  /// Reads what is new; every message of the agent is spoken.
  Future<void> _fetch() async {
    if (ended || _mode == CallMode.preparing) return;
    if (_fetching) {
      _fetchAgain = true;
      return;
    }
    _fetching = true;
    try {
      do {
        _fetchAgain = false;
        final last = _lastId;
        final msgs = last == null ? await backend.open() : await backend.after(last);
        if (ended) return;
        // Read as soon as fetched, before the instance's notifier gets to
        // it: the person hears it in the call (#525).
        final newest = msgs
            .map((m) => m.createdAt)
            .nonNulls
            .fold<DateTime?>(null, (a, b) => a == null || b.isAfter(a) ? b : a);
        if (newest != null) unawaited(_markRead(newest));
        for (final m in msgs) {
          _lastId = m.id;
          if (!_seen.add(m.id)) continue;
          _remember(m);
          if (m.authorKind != 'agent' || m.text.trim().isEmpty) continue;
          if (m.authorId != null && m.authorId != agentId) continue;
          final task = m.taskId;
          if (task != null && _tasks.add(task) && m.kind == 'text' && m.replyTo != null) {
            // The turn became a task: its acknowledgement, not a result.
            unawaited(sounds?.play(Earcon.task));
          }
          final to = m.replyTo;
          // The reply to an earlier turn, read late, is spoken, but the
          // wait for the last one goes on (#531).
          if (to == null || to == _posted) {
            _awaiting = false;
            _fillers.replyArrived();
            _giveUp?.cancel();
          }
          _say(CallLine(mine: false, text: textForSpeech(m.text).text));
          if (to != null && _cutStreams.remove(to)) {
            // Its stream was cut by the person speaking: the rest stands in
            // the chat, and a goodbye they spoke into does not hang up.
            _streamed.remove(to);
            continue;
          }
          final streamed = to == null ? null : _streamed.remove(to);
          if (to != null && to == _spokenFor) _stopSpoken();
          _queue.addAll(streamed == null ? _toSpeak(m) : _afterStream(m, streamed));
          if (m.meta['end_call'] == 'true') {
            if (mayHangUp?.call() ?? true) {
              _goodbye = true;
              diag('call', 'the agent says goodbye: hanging up after it');
            } else {
              diag('call', 'the agent says goodbye: the call stays open, hanging up is switched off');
            }
          }
        }
      } while (_fetchAgain && !ended);
    } on ApiException catch (e) {
      diag('call', 'reading the conversation: ${e.message}');
    } finally {
      _fetching = false;
    }
    _update();
    unawaited(_speakNext());
  }

  Future<void> _markRead(DateTime at) async {
    try {
      await backend.read(at);
    } catch (e) {
      diag('call', 'marking the conversation read: $e');
    }
  }

  /// What of an agent's message the call speaks: its spoken form when it
  /// has one (#502), with a word that the details are in the chat when that
  /// form left some out; otherwise the written message.
  List<_Utterance> _toSpeak(ConversationMessage m) {
    final spoken = (m.meta['spoken'] ?? '').trim();
    if (spoken.isEmpty) return [_Utterance(m.text)];
    return [
      _Utterance(spoken),
      if (m.meta['details_in_chat'] == 'true') _Utterance(words('call.detailsInChat'), plain: true),
    ];
  }

  Future<void> _speakNext() async {
    if (_speaking || ended || _greetingPending) return;
    while (_queue.isNotEmpty && !ended) {
      final u = _queue.removeAt(0);
      String text;
      String lang;
      var cut = false;
      if (u.plain) {
        text = u.text;
        lang = appLanguage;
      } else {
        final s = textForSpeech(u.text);
        if (s.text.isEmpty) continue;
        text = s.text;
        cut = s.cut;
        // A call keeps one language: the app's, or the one a reply was
        // clearly written in. A German reply full of English terms ("Merge
        // Request", "Tests") must not tip it over, so only a sentence long
        // enough to judge can change it, and only when the recogniser is sure.
        final judgeable = s.text.trim().split(RegExp(r'\s+')).length >= minWordsToSwitchLanguage;
        lang = (judgeable ? await speaker.language(s.text) : null) ?? _language ?? appLanguage;
        _language = lang;
      }
      if (cut) _queue.insert(0, _Utterance(words('call.restInChat'), plain: true));
      _speaking = true;
      _saying = text;
      _doubleTalkLogged = false;
      _update();
      try {
        await speaker.speak(text, language: lang);
      } catch (e) {
        diag('call', 'speaking failed: $e');
      }
      _speaking = false;
      _saidUntil = DateTime.now();
      _update();
    }
    // The goodbye has been said and nobody spoke into it (#517).
    if (_goodbye && _queue.isEmpty && !ended) {
      _goodbye = false;
      diag('call', 'goodbye said: hanging up');
      await hangUp();
    }
  }

  void _say(CallLine line) {
    lines.add(line);
    if (lines.length > _keepLines) lines.removeRange(0, lines.length - _keepLines);
  }

  bool _disposed = false;

  void _set(CallMode m) {
    _mode = m;
    if (!_disposed) notifyListeners();
  }

  /// The mode from what is going on, most pressing first.
  void _update() {
    if (_disposed) return;
    if (ended || _mode == CallMode.failed || _mode == CallMode.preparing) {
      notifyListeners();
      return;
    }
    _mode = muted
        ? CallMode.muted
        : _speaking
        ? CallMode.speaking
        : (_turns.speaking || _recognising)
        ? CallMode.hearing
        : _understood != null
        ? CallMode.understood
        : _awaiting
        ? CallMode.thinking
        : CallMode.listening;
    notifyListeners();
  }

  @override
  void dispose() {
    unawaited(hangUp());
    _fillers.dispose();
    _disposed = true;
    wordTicks.dispose();
    super.dispose();
  }
}

/// A turn's audio recognised (#527): the text, the language it was heard
/// in, why it goes nowhere — null when it goes to the agent — and its
/// clean-up with how long it took, null for a short turn.
class _Heard {
  _Heard(this.heard, {this.dropped, this.language, this.clean});

  final TurnText heard;
  final String? dropped;
  final String? language;
  final Future<(String?, int)>? clean;
}

/// How far into the pause that ends a turn it is recognised ahead (#527):
/// half way, not sooner than 0.3 s — a breath within a sentence is
/// shorter — and never at its end.
Duration lullAfter(Duration pause) {
  final half = pause ~/ 2;
  return half < const Duration(milliseconds: 300) ? const Duration(milliseconds: 300) : half;
}
