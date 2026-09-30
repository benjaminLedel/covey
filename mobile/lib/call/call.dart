import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter/foundation.dart';

import '../api.dart';
import '../diagnostics.dart';
import '../dictation.dart' show isHallucination;
import '../live.dart';
import '../models.dart';
import '../prefs.dart';
import 'ears.dart';
import 'speech_text.dart';
import 'turns.dart';
import 'voice.dart';

/// Calls as a trial (#494): the Mac only, behind an app setting that is on
/// by default while it is a trial. The setting is the device's, not the
/// organisation's — nothing on the instance changes.
class CallSettings {
  CallSettings._();

  static const _key = 'call.enabled';

  /// Whether this build can call: the Mac, where the system's speech
  /// synthesis is reached through the app's own channel.
  static bool get supported => debugSupported ?? (!kIsWeb && Platform.isMacOS);

  @visibleForTesting
  static bool? debugSupported;

  static final enabled = ValueNotifier<bool>(true);

  static Future<void> load() async {
    try {
      enabled.value = (await Prefs.instance.read(_key)) != 'off';
    } catch (_) {
      // Unreadable: the default.
    }
  }

  static Future<void> setEnabled(bool on) async {
    enabled.value = on;
    try {
      await Prefs.instance.write(_key, on ? 'on' : 'off');
    } catch (_) {
      // Kept for this run.
    }
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

  /// Fires when the conversation may have moved.
  Stream<void> changes();
}

/// The conversation API (#440, #447) for one agent's direct conversation.
class ApiCallBackend implements CallBackend {
  ApiCallBackend(this.api, this.agentId);

  final CoveyApi api;
  final String agentId;
  String? _id;

  String get _conv => _id ?? (throw StateError('not open'));

  @override
  Future<List<ConversationMessage>> open() async {
    _id = (await api.openDirect(MemberRef('agent', agentId))).id;
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
  Stream<void> changes() =>
      LiveEvents.instance.of({'chat'}, conversationId: _id, settle: const Duration(milliseconds: 250));
}

/// Where a call stands, as the face and the line under it show it.
enum CallMode {
  /// Models are fetched and loaded.
  preparing,

  /// The call listens; nobody speaks.
  listening,

  /// The person speaks, or what they said is being recognised.
  hearing,

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
/// [TurnSegmenter] cut a turn at a pause of 0.7 s, the recogniser turns it
/// into text on the device, and the text goes into the direct conversation
/// as an ordinary message, marked `via: call` — so the chat's own path
/// answers it (the triage: an answer, a note to a task, a task). Every
/// message the agent writes into the conversation while the call is open is
/// spoken: the answer, the acknowledgement of a task, and the task's result
/// when it arrives later. When the person speaks while the agent does, the
/// agent stops.
class CallController extends ChangeNotifier {
  CallController({
    required this.backend,
    required this.ears,
    required this.speaker,
    required this.agentId,
    required this.appLanguage,
    required this.words,
    this.nudgeAfter = const Duration(seconds: 20),
    this.pollEvery = const Duration(seconds: 5),
  }) {
    _turns = TurnSegmenter(onSpeech: _onSpeech, onTurn: _onTurn, onDiscard: _onDiscard);
  }

  final CallBackend backend;
  final CallEars ears;
  final Speaker speaker;
  final String agentId;

  /// The app's language, for the voice until a reply says otherwise and
  /// for the app's own words.
  final String appLanguage;

  /// The app's own spoken words, by key: `call.stillWorking`,
  /// `call.restInChat`.
  final String Function(String key) words;

  /// How long the call waits for a reply before saying it is still being
  /// worked on — once per turn.
  final Duration nudgeAfter;

  /// The net under the event stream while a reply is awaited.
  final Duration pollEvery;

  late final TurnSegmenter _turns;

  CallMode _mode = CallMode.preparing;
  CallMode get mode => _mode;

  /// What went wrong, when [mode] is [CallMode.failed].
  CallException? failure;

  bool muted = false;

  /// The person's voice, 0–1, smoothed.
  double level = 0;

  /// Counts the words the synthesiser reached: the face opens its mouth
  /// on each.
  final wordTicks = ValueNotifier<int>(0);

  /// The last lines said, oldest first.
  final lines = <CallLine>[];
  static const _keepLines = 4;

  bool get ended => _mode == CallMode.ended;

  final _queue = <_Utterance>[];
  bool _speaking = false;
  bool _recognising = false;
  bool _awaiting = false;
  bool _nudged = false;
  Timer? _nudge;
  Timer? _poll;
  Timer? _giveUp;
  StreamSubscription<void>? _changes;
  StreamSubscription<SpeakingEvent>? _spoken;
  String? _lastId;
  final _seen = <String>{};
  String? _language;
  List<SystemVoice> _voices = const [];

  /// What was spoken last and until when, to tell the agent's own voice
  /// from the loudspeaker apart from the person.
  String _saying = '';
  DateTime _saidUntil = DateTime.fromMillisecondsSinceEpoch(0);

  Future<void> _turnChain = Future.value();
  bool _fetching = false;
  bool _fetchAgain = false;

  /// Opens the call: loads the models, opens the conversation, starts
  /// hearing.
  Future<void> start() async {
    _set(CallMode.preparing);
    try {
      await ears.prepare();
      // Hung up while the models loaded: what just loaded is freed again.
      if (ended) return await ears.close();
      final had = await backend.open();
      for (final m in had) {
        _seen.add(m.id);
      }
      if (had.isNotEmpty) _lastId = had.last.id;
      _voices = await speaker.voices();
      if (ended) return;
      _spoken = speaker.events.listen((e) {
        if (e == SpeakingEvent.word) wordTicks.value++;
      });
      _changes = backend.changes().listen((_) => _fetch());
      _poll = Timer.periodic(pollEvery, (_) {
        if (_awaiting) _fetch();
      });
      await ears.listen(_onWindow);
      // Hung up while the microphone opened: nothing may keep it open.
      if (ended) return await ears.close();
      diag('call', 'open, ${_voices.length} voices');
      _set(CallMode.listening);
    } on CallException catch (e) {
      _fail(e);
    } on ApiException catch (e) {
      _fail(CallException(CallProblem.unavailable, e.message));
    } catch (e) {
      _fail(CallException(CallProblem.unavailable, '$e'));
    }
  }

  void _fail(CallException e) {
    if (ended) return;
    diag('call', 'failed: $e');
    failure = e;
    unawaited(_shut());
    _set(CallMode.failed);
  }

  /// Mutes or unmutes: muted, the microphone is closed.
  Future<void> setMuted(bool on) async {
    if (ended || _mode == CallMode.failed || _mode == CallMode.preparing) return;
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
    _mode = CallMode.ended;
    if (!_disposed) notifyListeners();
    await _shut();
    diag('call', 'ended');
  }

  Future<void> _shut() async {
    _queue.clear();
    _nudge?.cancel();
    _poll?.cancel();
    _giveUp?.cancel();
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
    // The agent's voice from the loudspeaker must not interrupt it: while
    // it speaks, the person has to be heard for a moment first.
    _turns.confirm = _speaking ? const Duration(milliseconds: 400) : Duration.zero;
    _turns.add(pcm, voiced);
    level = l > level ? l : level * 0.85 + l * 0.15;
    final now = DateTime.now();
    if (now.difference(_levelAt) > const Duration(milliseconds: 40)) {
      _levelAt = now;
      _update();
    }
  }

  void _onSpeech() {
    if (_speaking) {
      // Barge-in: the person speaks, the agent stops mid-sentence, and what
      // it had still to say is dropped — it stands in the chat.
      diag('call', 'barge-in');
      _queue.clear();
      unawaited(speaker.stop());
    }
    _update();
  }

  void _onDiscard() => _update();

  void _onTurn(Uint8List pcm) {
    _turnChain = _turnChain.then((_) => _handleTurn(pcm)).catchError((Object e) {
      diag('call', 'turn failed: $e');
    });
  }

  Future<void> _handleTurn(Uint8List pcm) async {
    if (ended) return;
    _recognising = true;
    _update();
    String text;
    try {
      text = (await ears.recognise(pcm)).trim();
    } finally {
      _recognising = false;
    }
    if (ended) return;
    if (text.isEmpty || isHallucination(text)) {
      _update();
      return;
    }
    // The agent heard itself: what came in is what it had just said.
    if (DateTime.now().difference(_saidUntil) < const Duration(seconds: 2) || _speaking) {
      if (looksLikeEcho(text, _saying)) {
        diag('call', 'echo dropped, ${text.length} characters');
        _update();
        return;
      }
    }
    _say(CallLine(mine: true, text: text));
    _awaiting = true;
    _nudged = false;
    _nudge?.cancel();
    _nudge = Timer(nudgeAfter, _stillWorking);
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
      diag('call', 'turn posted, ${text.length} characters');
    } on ApiException catch (e) {
      _awaiting = false;
      _nudge?.cancel();
      _say(CallLine(mine: false, text: e.message));
      _update();
      return;
    }
    unawaited(_fetch());
  }

  void _stillWorking() {
    if (!_awaiting || _nudged || ended) return;
    _nudged = true;
    _queue.add(_Utterance(words('call.stillWorking'), plain: true));
    unawaited(_speakNext());
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
        for (final m in msgs) {
          _lastId = m.id;
          if (!_seen.add(m.id)) continue;
          if (m.authorKind != 'agent' || m.text.trim().isEmpty) continue;
          if (m.authorId != null && m.authorId != agentId) continue;
          _awaiting = false;
          _nudge?.cancel();
          _giveUp?.cancel();
          _say(CallLine(mine: false, text: textForSpeech(m.text).text));
          _queue.add(_Utterance(m.text));
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

  Future<void> _speakNext() async {
    if (_speaking || ended) return;
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
        lang = await speaker.language(s.text) ?? _language ?? appLanguage;
        _language = lang;
      }
      if (cut) _queue.insert(0, _Utterance(words('call.restInChat'), plain: true));
      _speaking = true;
      _saying = text;
      _update();
      try {
        await speaker.speak(text, voice: chooseVoice(_voices, lang, agentId), language: lang);
      } catch (e) {
        diag('call', 'speaking failed: $e');
      }
      _speaking = false;
      _saidUntil = DateTime.now();
      _update();
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
        : _awaiting
        ? CallMode.thinking
        : CallMode.listening;
    notifyListeners();
  }

  @override
  void dispose() {
    unawaited(hangUp());
    _disposed = true;
    wordTicks.dispose();
    super.dispose();
  }
}
