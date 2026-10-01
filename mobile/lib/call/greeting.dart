import 'dart:async';
import 'dart:math' as math;

import '../prefs.dart';

/// What a call's greeting (#506) is made of: the person's name as the
/// instance knows it, and the address the chat tone sets — `du`, `sie`, or
/// empty when nothing says.
class GreetingFacts {
  const GreetingFacts({this.personName = '', this.address = ''});

  /// The display name of the seat calling (GET /auth/me).
  final String personName;

  /// `du`, `sie` or empty (GET /conversations/{id}/speech).
  final String address;

  /// Formal only when the chat tone says Sie; unknown is informal, which in
  /// the languages without a du/Sie is the plain friendly form.
  bool get formal => address.trim().toLowerCase() == 'sie';
}

/// The part of the day a greeting fits: until noon, the afternoon, and from
/// six in the evening through the night.
enum DayTime { morning, day, evening }

DayTime dayTimeOf(DateTime t) => t.hour >= 5 && t.hour < 12
    ? DayTime.morning
    : t.hour >= 12 && t.hour < 18
    ? DayTime.day
    : DayTime.evening;

/// A greeting chosen for one call: its [key] names the template (without the
/// names filled in), which the next call with the same agent avoids.
class Greeting {
  const Greeting({required this.key, required this.text, required this.language});
  final String key;
  final String text;
  final String language;

  @override
  String toString() => 'greeting $key';
}

/// One register of a language: the short openings a call is greeted with,
/// per part of the day (#528); `[…]` is left out when the name in it is not
/// known.
class _Register {
  const _Register({required this.morning, required this.day, required this.evening});

  final List<String> morning, day, evening;

  List<String> openers(DayTime t) => switch (t) {
    DayTime.morning => morning,
    DayTime.day => day,
    DayTime.evening => evening,
  };
}

class _Language {
  const _Language({required this.informal, required this.formal});
  final _Register informal, formal;
}

/// The greetings by language (the app's ten). `{name}` is the person —
/// the first name when informal, the full name when formal and there is a
/// surname.
const _greetings = <String, _Language>{
  'de': _Language(
    informal: _Register(
      morning: ['Guten Morgen[, {name}]!', 'Moin[, {name}]!', 'Morgen[, {name}]!'],
      day: ['Hallo[, {name}]!', 'Hi[, {name}]!', 'Hey[, {name}]!'],
      evening: ['Guten Abend[, {name}]!', 'Hallo[, {name}]!', 'Hi[, {name}]!'],
    ),
    formal: _Register(
      morning: ['Guten Morgen[, {name}].', 'Schönen guten Morgen[, {name}].', 'Einen guten Morgen[, {name}].'],
      day: ['Guten Tag[, {name}].', 'Schönen guten Tag[, {name}].', 'Hallo[, {name}].'],
      evening: ['Guten Abend[, {name}].', 'Schönen guten Abend[, {name}].', 'Hallo[, {name}].'],
    ),
  ),
  'en': _Language(
    informal: _Register(
      morning: ['Good morning[, {name}]!', 'Morning[, {name}]!', 'Hi[, {name}]!'],
      day: ['Hi[, {name}]!', 'Hello[, {name}]!', 'Hey[, {name}]!', 'Hi there[, {name}]!'],
      evening: ['Good evening[, {name}]!', 'Hi[, {name}]!', 'Evening[, {name}]!', 'Hey[, {name}]!'],
    ),
    formal: _Register(
      morning: ['Good morning[, {name}].'],
      day: ['Good afternoon[, {name}].', 'Hello[, {name}].'],
      evening: ['Good evening[, {name}].', 'Hello[, {name}].'],
    ),
  ),
  'es': _Language(
    informal: _Register(
      morning: ['¡Buenos días[, {name}]!', '¡Hola[, {name}]!', '¡Muy buenos días[, {name}]!'],
      day: ['¡Hola[, {name}]!', '¡Buenas[, {name}]!', '¡Qué tal[, {name}]!'],
      evening: ['¡Buenas tardes[, {name}]!', '¡Hola[, {name}]!', '¡Buenas[, {name}]!', '¡Buenas noches[, {name}]!'],
    ),
    formal: _Register(
      morning: ['Buenos días[, {name}].', 'Muy buenos días[, {name}].'],
      day: ['Buenas tardes[, {name}].', 'Hola[, {name}].', 'Muy buenas tardes[, {name}].'],
      evening: ['Buenas tardes[, {name}].', 'Hola[, {name}].', 'Buenas noches[, {name}].'],
    ),
  ),
  'fr': _Language(
    informal: _Register(
      morning: ['Bonjour[, {name}] !', 'Salut[, {name}] !', 'Coucou[, {name}] !', 'Bonjour à toi[, {name}] !'],
      day: ['Salut[, {name}] !', 'Bonjour[, {name}] !', 'Coucou[, {name}] !', 'Bonjour à toi[, {name}] !'],
      evening: ['Bonsoir[, {name}] !', 'Salut[, {name}] !', 'Coucou[, {name}] !', 'Bonsoir à toi[, {name}] !'],
    ),
    formal: _Register(
      morning: ['Bonjour[, {name}].', 'Bonjour à vous[, {name}].', 'Bonjour et bienvenue[, {name}].'],
      day: ['Bonjour[, {name}].', 'Bonjour à vous[, {name}].', 'Bonjour et bienvenue[, {name}].'],
      evening: ['Bonsoir[, {name}].', 'Bonsoir à vous[, {name}].', 'Bonsoir et bienvenue[, {name}].'],
    ),
  ),
  'it': _Language(
    informal: _Register(
      morning: ['Buongiorno[, {name}]!', 'Ciao[, {name}]!', 'Buongiorno a te[, {name}]!'],
      day: ['Ciao[, {name}]!', 'Ehi[, {name}]!', 'Salve[, {name}]!', 'Eccoti[, {name}]!'],
      evening: ['Buonasera[, {name}]!', 'Ciao[, {name}]!', 'Ehi[, {name}]!', 'Buonasera a te[, {name}]!'],
    ),
    formal: _Register(
      morning: ['Buongiorno[, {name}].', 'Buongiorno a Lei[, {name}].'],
      day: ['Buongiorno[, {name}].', 'Salve[, {name}].', 'Buon pomeriggio[, {name}].'],
      evening: ['Buonasera[, {name}].', 'Salve[, {name}].', 'Buonasera a Lei[, {name}].'],
    ),
  ),
  'nl': _Language(
    informal: _Register(
      morning: ['Goedemorgen[, {name}]!', 'Morgen[, {name}]!', 'Hoi[, {name}]!', 'Goeiemorgen[, {name}]!'],
      day: ['Hoi[, {name}]!', 'Hallo[, {name}]!', 'Hé[, {name}]!', 'Hallo daar[, {name}]!'],
      evening: ['Goedenavond[, {name}]!', 'Hoi[, {name}]!', 'Hallo[, {name}]!', 'Hé[, {name}]!'],
    ),
    formal: _Register(
      morning: ['Goedemorgen[, {name}].', 'Een goedemorgen[, {name}].'],
      day: ['Goedemiddag[, {name}].', 'Hallo[, {name}].', 'Een goedemiddag[, {name}].'],
      evening: ['Goedenavond[, {name}].', 'Hallo[, {name}].', 'Een goedenavond[, {name}].'],
    ),
  ),
  // The formal Polish forms leave out Pan and Pani: whom the call greets is
  // not known.
  'pl': _Language(
    informal: _Register(
      morning: ['Dzień dobry[, {name}]!', 'Cześć[, {name}]!', 'Hej[, {name}]!', 'Witaj[, {name}]!'],
      day: ['Cześć[, {name}]!', 'Hej[, {name}]!', 'Witaj[, {name}]!', 'Dzień dobry[, {name}]!'],
      evening: ['Dobry wieczór[, {name}]!', 'Cześć[, {name}]!', 'Hej[, {name}]!', 'Witaj[, {name}]!'],
    ),
    formal: _Register(
      morning: ['Dzień dobry[, {name}].', 'Witam serdecznie[, {name}].', 'Uprzejmie witam[, {name}].'],
      day: ['Dzień dobry[, {name}].', 'Witam serdecznie[, {name}].', 'Uprzejmie witam[, {name}].'],
      evening: ['Dobry wieczór[, {name}].', 'Witam serdecznie[, {name}].', 'Uprzejmie witam[, {name}].'],
    ),
  ),
  // Without words that take the speaker's or the listener's gender.
  'pt': _Language(
    informal: _Register(
      morning: ['Bom dia[, {name}]!', 'Olá[, {name}]!', 'Oi[, {name}]!'],
      day: ['Olá[, {name}]!', 'Oi[, {name}]!', 'Boa tarde[, {name}]!'],
      evening: ['Boa noite[, {name}]!', 'Olá[, {name}]!', 'Oi[, {name}]!'],
    ),
    formal: _Register(
      morning: ['Bom dia[, {name}].', 'Muito bom dia[, {name}].', 'Um bom dia[, {name}].'],
      day: ['Boa tarde[, {name}].', 'Olá[, {name}].', 'Uma boa tarde[, {name}].'],
      evening: ['Boa noite[, {name}].', 'Olá[, {name}].', 'Uma boa noite[, {name}].'],
    ),
  ),
  'ja': _Language(
    informal: _Register(
      morning: ['おはようございます[、{name}さん]！', 'おはよう[、{name}さん]！', '[{name}さん、]おはようございます！', 'おはよー[、{name}さん]！'],
      day: ['こんにちは[、{name}さん]！', '[{name}さん、]こんにちは！', 'どうも[、{name}さん]！', 'やあ[、{name}さん]！'],
      evening: ['こんばんは[、{name}さん]！', '[{name}さん、]こんばんは！', 'お疲れさまです[、{name}さん]！', 'やあ[、{name}さん]！'],
    ),
    formal: _Register(
      morning: ['おはようございます[、{name}様]。', '[{name}様、]おはようございます。'],
      day: ['こんにちは[、{name}様]。', '[{name}様、]こんにちは。'],
      evening: ['こんばんは[、{name}様]。', '[{name}様、]こんばんは。'],
    ),
  ),
  'zh': _Language(
    informal: _Register(
      morning: ['早上好[，{name}]！', '早[，{name}]！', '[{name}，]早上好！', '早啊[，{name}]！'],
      day: ['你好[，{name}]！', '嗨[，{name}]！', '[{name}，]你好！', '哈喽[，{name}]！'],
      evening: ['晚上好[，{name}]！', '嗨[，{name}]！', '[{name}，]晚上好！', '哈喽[，{name}]！'],
    ),
    formal: _Register(
      morning: ['早上好[，{name}]。', '[{name}，]早上好。', '您好[，{name}]。'],
      day: ['您好[，{name}]。', '[{name}，]您好。', '下午好[，{name}]。'],
      evening: ['晚上好[，{name}]。', '[{name}，]晚上好。', '您好[，{name}]。'],
    ),
  ),
};

/// The languages with greetings: the app's ten.
Iterable<String> get greetingLanguages => _greetings.keys;

String _base(String language) => language.split(RegExp('[-_]')).first.toLowerCase();

/// The name the greeting says: the first name when informal; when formal
/// the full name, and only when there is a surname — "Frau" or "Herr" is
/// not known, and a first name alone would be too familiar. Nothing for a
/// name that is an address.
String greetingName(String displayName, {required bool formal}) {
  final n = displayName.trim();
  if (n.isEmpty || n.contains('@')) return '';
  final parts = n.split(RegExp(r'\s+'));
  if (formal) return parts.length >= 2 ? parts.join(' ') : '';
  return parts.first;
}

final _slot = RegExp(r'\{(\w+)\}');

/// [template] with its slots filled; an optional part (`[…]`) whose slot is
/// empty is left out, and a required slot left empty drops the whole.
String? _fill(String template, Map<String, String> values) {
  final out = template.replaceAllMapped(RegExp(r'\[([^\]]*)\]'), (m) {
    final part = m[1]!;
    final empty = _slot.allMatches(part).any((s) => (values[s[1]] ?? '').isEmpty);
    return empty ? '' : part;
  });
  if (_slot.allMatches(out).any((s) => (values[s[1]] ?? '').isEmpty)) return null;
  return out.replaceAllMapped(_slot, (s) => values[s[1]]!);
}

/// The opening a key names, `lang.register.daytime.o<i>` — also in the
/// longer keys of before #528, which went on with `.i<j>.q<k>`.
int? _opener(String? key, String prefix) {
  if (key == null || !key.startsWith(prefix)) return null;
  final m = RegExp(r'^o(\d+)').firstMatch(key.substring(prefix.length));
  return m == null ? null : int.parse(m[1]!);
}

/// A greeting in [language] at [time]: one short opening (#528) — a call
/// is answered with a hello, not with a speech —, other than the one in
/// [last] where there is another. Null for a language without greetings.
Greeting? composeGreeting({
  required String language,
  required GreetingFacts facts,
  required DateTime time,
  String? last,
  math.Random? random,
}) {
  final lang = _base(language);
  final l = _greetings[lang];
  if (l == null) return null;
  final formal = facts.formal;
  final reg = formal ? l.formal : l.informal;
  final daytime = dayTimeOf(time);
  final openers = reg.openers(daytime);
  final prefix = '$lang.${formal ? 'formal' : 'informal'}.${daytime.name}.';
  final before = _opener(last, prefix);
  var choices = [
    for (var o = 0; o < openers.length; o++)
      if (o != before) o,
  ];
  if (choices.isEmpty) choices = [for (var o = 0; o < openers.length; o++) o];
  final o = choices[(random ?? math.Random()).nextInt(choices.length)];
  final text = _fill(openers[o], {'name': greetingName(facts.personName, formal: formal)});
  if (text == null) return null;
  return Greeting(key: '${prefix}o$o', text: text, language: lang);
}

/// Which greeting each agent said last, so the next call says another.
abstract class GreetingMemory {
  Future<String?> last(String agentId);
  Future<void> remember(String agentId, String key);
}

/// [GreetingMemory] in the app's settings on this device.
class PrefsGreetingMemory implements GreetingMemory {
  const PrefsGreetingMemory();

  static String _key(String agentId) => 'call.greeting.last.$agentId';

  @override
  Future<String?> last(String agentId) async {
    try {
      return await Prefs.instance.read(_key(agentId));
    } catch (_) {
      return null;
    }
  }

  @override
  Future<void> remember(String agentId, String key) async {
    try {
      await Prefs.instance.write(_key(agentId), key);
    } catch (_) {
      // Kept for nothing: the next call may repeat it.
    }
  }
}

/// How a call greets (#506): whether at all ([enabled], read as the call
/// starts), which greeting, and how long the call waits after the connected
/// sound for the voice provider's audio before the Mac's voice says it.
class CallGreeter {
  CallGreeter({
    required this.enabled,
    this.memory = const PrefsGreetingMemory(),
    DateTime Function()? now,
    math.Random? random,
    this.wait = const Duration(milliseconds: 1500),
  }) : _now = now ?? DateTime.now,
       _random = random ?? math.Random();

  final bool Function() enabled;
  final GreetingMemory memory;
  final DateTime Function() _now;
  final math.Random _random;
  final Duration wait;

  /// The greeting for this call with [agentId], remembered as its last.
  Future<Greeting?> choose({required String agentId, required String language, required GreetingFacts facts}) async {
    final g = await compose(agentId: agentId, language: language, facts: facts);
    if (g != null) await remember(agentId, g);
    return g;
  }

  /// A template greeting for this call with [agentId], other than the last
  /// one said; not yet remembered — [remember] it once it is said.
  Future<Greeting?> compose({required String agentId, required String language, required GreetingFacts facts}) async =>
      composeGreeting(
        language: language,
        facts: facts,
        time: _now(),
        last: await memory.last(agentId),
        random: _random,
      );

  /// Keeps [g] as the last greeting to [agentId].
  Future<void> remember(String agentId, Greeting g) => memory.remember(agentId, g.key);
}

/// The greeting of one call on its way (#506, #528): chosen and
/// synthesised as the line starts ringing, said as the call connects. The
/// instance's written greeting (#513) is no longer asked for: a call is
/// answered with a short hello, which needs no model.
class GreetingInFlight {
  GreetingInFlight({
    required Future<Greeting?> Function() template,
    required this.synthesise,
    void Function(String what)? log,
  }) : _log = log ?? ((_) {}) {
    _audio = template().then(
      (g) {
        _greeting = g;
        if (g == null) return false;
        return synthesise(g).catchError((Object e) {
          _log('greeting not synthesised: $e');
          return false;
        });
      },
      onError: (Object e) {
        _log('no greeting: $e');
        return false;
      },
    );
  }

  /// Makes a greeting's audio ahead; true when it is ready.
  final Future<bool> Function(Greeting g) synthesise;
  final void Function(String what) _log;

  Greeting? _greeting;
  late final Future<bool> _audio;

  /// What to say as the call connects, and whether the provider's audio for
  /// it is ready; null when there is nothing. Waits at most for [deadline]:
  /// then the greeting chosen by then, in the Mac's voice.
  Future<(Greeting, bool)?> pick(Future<void> deadline) async {
    final ready = await Future.any<bool>([_audio, deadline.then((_) => false)]);
    final g = _greeting;
    return g == null ? null : (g, ready);
  }
}
