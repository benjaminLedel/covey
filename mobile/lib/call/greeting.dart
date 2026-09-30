import 'dart:math' as math;

import '../prefs.dart';

/// What a call's greeting (#506) is made of beside the agent's name: the
/// person's name as the instance knows it, the agent's department, and the
/// address the chat tone sets — `du`, `sie`, or empty when nothing says.
class GreetingFacts {
  const GreetingFacts({this.personName = '', this.department = '', this.address = ''});

  /// The display name of the seat calling (GET /auth/me).
  final String personName;

  /// The agent's department, empty when it has none or it is unknown.
  final String department;

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

/// One register of a language: an opening per part of the day, the agent
/// introducing itself, and the open question. A greeting is one of each,
/// joined; `[…]` is left out when a name in it is not known.
class _Register {
  const _Register({
    required this.morning,
    required this.day,
    required this.evening,
    required this.intros,
    required this.questions,
  });

  final List<String> morning, day, evening, intros, questions;

  List<String> openers(DayTime t) => switch (t) {
    DayTime.morning => morning,
    DayTime.day => day,
    DayTime.evening => evening,
  };
}

class _Language {
  const _Language({required this.informal, required this.formal, this.join = ' '});
  final _Register informal, formal;

  /// Between the parts: a space, none in Japanese and Chinese.
  final String join;
}

/// The greetings by language (the app's ten). `{name}` is the person —
/// the first name when informal, the full name when formal and there is a
/// surname —, `{agent}` the agent, `{dept}` its department.
const _greetings = <String, _Language>{
  'de': _Language(
    informal: _Register(
      morning: ['Guten Morgen[, {name}]!', 'Moin[, {name}]!', 'Morgen[, {name}]!'],
      day: ['Hallo[, {name}]!', 'Hi[, {name}]!', 'Hey[, {name}]!'],
      evening: ['Guten Abend[, {name}]!', 'Hallo[, {name}]!', 'Hi[, {name}]!'],
      intros: ['Hier ist {agent}[ aus dem Team {dept}].', '{agent} hier[, aus dem Team {dept}].'],
      questions: ['Was kann ich für dich tun?', 'Wie kann ich dir helfen?', 'Was gibt’s?', 'Worum geht’s?'],
    ),
    formal: _Register(
      morning: ['Guten Morgen[, {name}].', 'Schönen guten Morgen[, {name}].', 'Einen guten Morgen[, {name}].'],
      day: ['Guten Tag[, {name}].', 'Schönen guten Tag[, {name}].', 'Hallo[, {name}].'],
      evening: ['Guten Abend[, {name}].', 'Schönen guten Abend[, {name}].', 'Hallo[, {name}].'],
      intros: ['Hier ist {agent}[ aus dem Team {dept}].', 'Sie sprechen mit {agent}[ aus dem Team {dept}].'],
      questions: ['Was kann ich für Sie tun?', 'Wie kann ich Ihnen helfen?', 'Womit kann ich Ihnen helfen?'],
    ),
  ),
  'en': _Language(
    informal: _Register(
      morning: ['Good morning[, {name}]!', 'Morning[, {name}]!', 'Hi[, {name}]!'],
      day: ['Hi[, {name}]!', 'Hello[, {name}]!', 'Hey[, {name}]!'],
      evening: ['Good evening[, {name}]!', 'Hi[, {name}]!', 'Evening[, {name}]!'],
      intros: ['It’s {agent}[ from {dept}].', '{agent} here[, from {dept}].'],
      questions: ['What can I do for you?', 'How can I help?', 'What’s up?', 'What can I help you with?'],
    ),
    formal: _Register(
      morning: [
        'Good morning[, {name}].',
        'Hello[, {name}], good morning.',
        'Good morning[, {name}], thanks for calling.',
      ],
      day: ['Good afternoon[, {name}].', 'Hello[, {name}].', 'Good afternoon[, {name}], thanks for calling.'],
      evening: ['Good evening[, {name}].', 'Hello[, {name}].', 'Good evening[, {name}], thanks for calling.'],
      intros: ['This is {agent}[ from {dept}].', 'You’re speaking with {agent}[ from {dept}].'],
      questions: ['How may I help you?', 'What can I do for you?', 'How can I help you today?'],
    ),
  ),
  'es': _Language(
    informal: _Register(
      morning: ['¡Buenos días[, {name}]!', '¡Hola[, {name}], buenos días!', '¡Hola[, {name}]!'],
      day: ['¡Hola[, {name}]!', '¡Buenas[, {name}]!', '¡Hola[, {name}], qué tal!'],
      evening: ['¡Buenas tardes[, {name}]!', '¡Hola[, {name}]!', '¡Buenas[, {name}]!'],
      intros: ['Soy {agent}[, del equipo de {dept}].', 'Te habla {agent}[, del equipo de {dept}].'],
      questions: ['¿Qué puedo hacer por ti?', '¿En qué te puedo ayudar?', '¿Cómo te ayudo?'],
    ),
    formal: _Register(
      morning: ['Buenos días[, {name}].', 'Hola[, {name}], buenos días.', 'Muy buenos días[, {name}].'],
      day: ['Buenas tardes[, {name}].', 'Hola[, {name}].', 'Hola[, {name}], gracias por llamar.'],
      evening: ['Buenas tardes[, {name}].', 'Hola[, {name}].', 'Buenas tardes[, {name}], gracias por llamar.'],
      intros: ['Le habla {agent}[, del equipo de {dept}].', 'Soy {agent}[, del equipo de {dept}].'],
      questions: ['¿En qué puedo ayudarle?', '¿Qué puedo hacer por usted?', '¿Cómo puedo ayudarle?'],
    ),
  ),
  'fr': _Language(
    informal: _Register(
      morning: ['Bonjour[, {name}] !', 'Salut[, {name}] !', 'Coucou[, {name}] !'],
      day: ['Salut[, {name}] !', 'Bonjour[, {name}] !', 'Coucou[, {name}] !'],
      evening: ['Bonsoir[, {name}] !', 'Salut[, {name}] !', 'Coucou[, {name}] !'],
      intros: ['C’est {agent}[, de l’équipe {dept}].', 'Ici {agent}[, de l’équipe {dept}].'],
      questions: ['Qu’est-ce que je peux faire pour toi ?', 'Comment je peux t’aider ?', 'Je t’écoute.'],
    ),
    formal: _Register(
      morning: ['Bonjour[, {name}].', 'Bonjour[, {name}], merci de votre appel.', 'Bonjour à vous[, {name}].'],
      day: ['Bonjour[, {name}].', 'Bonjour[, {name}], merci de votre appel.', 'Bonjour à vous[, {name}].'],
      evening: ['Bonsoir[, {name}].', 'Bonsoir[, {name}], merci de votre appel.', 'Bonsoir à vous[, {name}].'],
      intros: ['Ici {agent}[, de l’équipe {dept}].', 'Vous êtes en ligne avec {agent}[, de l’équipe {dept}].'],
      questions: ['Que puis-je faire pour vous ?', 'Comment puis-je vous aider ?', 'En quoi puis-je vous être utile ?'],
    ),
  ),
  'it': _Language(
    informal: _Register(
      morning: ['Buongiorno[, {name}]!', 'Ciao[, {name}]!', 'Ciao[, {name}], buongiorno!'],
      day: ['Ciao[, {name}]!', 'Ehi[, {name}]!', 'Salve[, {name}]!'],
      evening: ['Buonasera[, {name}]!', 'Ciao[, {name}]!', 'Ehi[, {name}]!'],
      intros: ['Sono {agent}[, del team {dept}].', 'Qui {agent}[, del team {dept}].'],
      questions: ['Cosa posso fare per te?', 'Come posso aiutarti?', 'Dimmi pure.'],
    ),
    formal: _Register(
      morning: [
        'Buongiorno[, {name}].',
        'Salve[, {name}], buongiorno.',
        'Buongiorno[, {name}], grazie della chiamata.',
      ],
      day: ['Buongiorno[, {name}].', 'Salve[, {name}].', 'Buongiorno[, {name}], grazie della chiamata.'],
      evening: ['Buonasera[, {name}].', 'Salve[, {name}].', 'Buonasera[, {name}], grazie della chiamata.'],
      intros: ['Sono {agent}[, del team {dept}].', 'Le parla {agent}[, del team {dept}].'],
      questions: ['Come posso aiutarLa?', 'Cosa posso fare per Lei?', 'In cosa posso esserLe utile?'],
    ),
  ),
  'nl': _Language(
    informal: _Register(
      morning: ['Goedemorgen[, {name}]!', 'Morgen[, {name}]!', 'Hoi[, {name}]!'],
      day: ['Hoi[, {name}]!', 'Hallo[, {name}]!', 'Hé[, {name}]!'],
      evening: ['Goedenavond[, {name}]!', 'Hoi[, {name}]!', 'Hallo[, {name}]!'],
      intros: ['Met {agent}[ van het team {dept}].', 'Je spreekt met {agent}[ van het team {dept}].'],
      questions: ['Wat kan ik voor je doen?', 'Waarmee kan ik je helpen?', 'Vertel het maar.'],
    ),
    formal: _Register(
      morning: ['Goedemorgen[, {name}].', 'Hallo[, {name}], goedemorgen.', 'Goedemorgen[, {name}], fijn dat u belt.'],
      day: ['Goedemiddag[, {name}].', 'Hallo[, {name}].', 'Goedemiddag[, {name}], fijn dat u belt.'],
      evening: ['Goedenavond[, {name}].', 'Hallo[, {name}].', 'Goedenavond[, {name}], fijn dat u belt.'],
      intros: ['U spreekt met {agent}[ van het team {dept}].', 'Met {agent}[ van het team {dept}].'],
      questions: ['Wat kan ik voor u doen?', 'Waarmee kan ik u helpen?', 'Hoe kan ik u helpen?'],
    ),
  ),
  // The formal Polish forms leave out Pan and Pani: whom the call greets is
  // not known.
  'pl': _Language(
    informal: _Register(
      morning: ['Dzień dobry[, {name}]!', 'Cześć[, {name}]!', 'Hej[, {name}]!'],
      day: ['Cześć[, {name}]!', 'Hej[, {name}]!', 'Witaj[, {name}]!'],
      evening: ['Dobry wieczór[, {name}]!', 'Cześć[, {name}]!', 'Hej[, {name}]!'],
      intros: ['Tu {agent}[ z zespołu {dept}].', 'Mówi {agent}[ z zespołu {dept}].'],
      questions: ['Co mogę dla ciebie zrobić?', 'W czym mogę ci pomóc?', 'Jak mogę ci pomóc?'],
    ),
    formal: _Register(
      morning: ['Dzień dobry[, {name}].', 'Witam serdecznie[, {name}].', 'Dzień dobry[, {name}], dziękuję za telefon.'],
      day: ['Dzień dobry[, {name}].', 'Witam serdecznie[, {name}].', 'Dzień dobry[, {name}], dziękuję za telefon.'],
      evening: [
        'Dobry wieczór[, {name}].',
        'Witam serdecznie[, {name}].',
        'Dobry wieczór[, {name}], dziękuję za telefon.',
      ],
      intros: ['Tu {agent}[ z zespołu {dept}].', 'Mówi {agent}[ z zespołu {dept}].'],
      questions: ['W czym mogę pomóc?', 'Czym mogę służyć?', 'Jak mogę pomóc?'],
    ),
  ),
  // Without words that take the speaker's or the listener's gender.
  'pt': _Language(
    informal: _Register(
      morning: ['Bom dia[, {name}]!', 'Olá[, {name}]!', 'Oi[, {name}], bom dia!'],
      day: ['Olá[, {name}]!', 'Oi[, {name}]!', 'Boa tarde[, {name}]!'],
      evening: ['Boa noite[, {name}]!', 'Olá[, {name}]!', 'Oi[, {name}]!'],
      intros: ['Aqui é {agent}[, da equipe {dept}].', 'É {agent}[, da equipe {dept}].'],
      questions: ['Em que posso ajudar?', 'O que posso fazer por você?', 'Como posso ajudar?'],
    ),
    formal: _Register(
      morning: ['Bom dia[, {name}].', 'Olá[, {name}], bom dia.', 'Muito bom dia[, {name}].'],
      day: ['Boa tarde[, {name}].', 'Olá[, {name}], boa tarde.', 'Olá[, {name}].'],
      evening: ['Boa noite[, {name}].', 'Olá[, {name}], boa noite.', 'Olá[, {name}].'],
      intros: ['Aqui é {agent}[, da equipe {dept}].', 'Fala {agent}[, da equipe {dept}].'],
      questions: ['Em que posso ser útil?', 'Como posso ajudar?', 'Em que posso ajudar hoje?'],
    ),
  ),
  'ja': _Language(
    join: '',
    informal: _Register(
      morning: ['おはようございます[、{name}さん]！', 'おはよう[、{name}さん]！', '[{name}さん、]おはようございます！'],
      day: ['こんにちは[、{name}さん]！', '[{name}さん、]こんにちは！', 'どうも[、{name}さん]！'],
      evening: ['こんばんは[、{name}さん]！', '[{name}さん、]こんばんは！', 'お疲れさまです[、{name}さん]！'],
      intros: ['[{dept}チームの]{agent}です。', 'こちら[{dept}チームの]{agent}です。'],
      questions: ['何か手伝えることある？', 'どうしたの？', '何でも聞いてね。'],
    ),
    formal: _Register(
      morning: ['おはようございます[、{name}様]。', '[{name}様、]おはようございます。', '[{name}様、]お電話ありがとうございます。'],
      day: ['こんにちは[、{name}様]。', '[{name}様、]こんにちは。', '[{name}様、]お電話ありがとうございます。'],
      evening: ['こんばんは[、{name}様]。', '[{name}様、]こんばんは。', '[{name}様、]お電話ありがとうございます。'],
      intros: ['[{dept}チームの]{agent}でございます。', '[{dept}チームの]{agent}と申します。'],
      questions: ['ご用件をお伺いします。', 'どのようなご用件でしょうか。', '何かお手伝いできることはございますか。'],
    ),
  ),
  'zh': _Language(
    join: '',
    informal: _Register(
      morning: ['早上好[，{name}]！', '早[，{name}]！', '[{name}，]早上好！'],
      day: ['你好[，{name}]！', '嗨[，{name}]！', '[{name}，]你好！'],
      evening: ['晚上好[，{name}]！', '嗨[，{name}]！', '[{name}，]晚上好！'],
      intros: ['我是[{dept}团队的]{agent}。', '这里是[{dept}团队的]{agent}。'],
      questions: ['有什么我可以帮你的吗？', '需要我做点什么？', '你说吧。'],
    ),
    formal: _Register(
      morning: ['早上好[，{name}]。', '[{name}，]早上好。', '您好[，{name}]。'],
      day: ['您好[，{name}]。', '[{name}，]您好。', '下午好[，{name}]。'],
      evening: ['晚上好[，{name}]。', '[{name}，]晚上好。', '您好[，{name}]。'],
      intros: ['我是[{dept}团队的]{agent}。', '这里是[{dept}团队的]{agent}。'],
      questions: ['请问有什么可以帮您？', '请问您需要什么帮助？', '有什么可以为您效劳的？'],
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

/// The key's parts, `lang.register.daytime.o<i>.i<j>.q<k>`.
(int, int)? _openerAndQuestion(String? key, String prefix) {
  if (key == null || !key.startsWith(prefix)) return null;
  final m = RegExp(r'o(\d+)\.i\d+\.q(\d+)$').firstMatch(key);
  return m == null ? null : (int.parse(m[1]!), int.parse(m[2]!));
}

/// A greeting in [language] at [time]: one opening, introduction and
/// question, never the one in [last]; when [last] was of the same kind, one
/// that differs in its opening and its question as well, so it does not
/// sound the same. Null for a language without greetings.
Greeting? composeGreeting({
  required String language,
  required String agentName,
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
  final before = _openerAndQuestion(last, prefix);
  final all = <(int, int, int)>[
    for (var o = 0; o < openers.length; o++)
      for (var i = 0; i < reg.intros.length; i++)
        for (var q = 0; q < reg.questions.length; q++) (o, i, q),
  ];
  String keyOf((int, int, int) c) => '${prefix}o${c.$1}.i${c.$2}.q${c.$3}';
  var choices = before == null
      ? all.where((c) => keyOf(c) != last).toList()
      : all.where((c) => c.$1 != before.$1 && c.$3 != before.$2).toList();
  if (choices.isEmpty) choices = all.where((c) => keyOf(c) != last).toList();
  if (choices.isEmpty) choices = all;
  final c = choices[(random ?? math.Random()).nextInt(choices.length)];
  final values = {
    'name': greetingName(facts.personName, formal: formal),
    'agent': agentName.trim(),
    'dept': facts.department.trim(),
  };
  final parts = [
    _fill(openers[c.$1], values),
    // Without the agent's name it does not introduce itself.
    _fill(reg.intros[c.$2], values),
    _fill(reg.questions[c.$3], values),
  ].whereType<String>().where((p) => p.isNotEmpty);
  return Greeting(key: keyOf(c), text: parts.join(l.join), language: lang);
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
  Future<Greeting?> choose({
    required String agentId,
    required String agentName,
    required String language,
    required GreetingFacts facts,
  }) async {
    final g = composeGreeting(
      language: language,
      agentName: agentName,
      facts: facts,
      time: _now(),
      last: await memory.last(agentId),
      random: _random,
    );
    if (g != null) await memory.remember(agentId, g.key);
    return g;
  }
}
