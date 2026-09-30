import 'dart:async';
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
      morning: [
        'Guten Morgen[, {name}]!',
        'Moin[, {name}]!',
        'Morgen[, {name}]!',
        'Guten Morgen[, {name}], schön, dass du anrufst!',
      ],
      day: ['Hallo[, {name}]!', 'Hi[, {name}]!', 'Hey[, {name}]!', 'Schön, dass du anrufst[, {name}]!'],
      evening: [
        'Guten Abend[, {name}]!',
        'Hallo[, {name}]!',
        'Hi[, {name}]!',
        'Guten Abend[, {name}], schön, dass du anrufst!',
      ],
      intros: [
        'Hier ist {agent}[ aus dem Team {dept}].',
        '{agent} hier[, aus dem Team {dept}].',
        'Du sprichst mit {agent}[ aus dem Team {dept}].',
        'Hier spricht {agent}[ aus dem Team {dept}].',
      ],
      questions: [
        'Was kann ich für dich tun?',
        'Wie kann ich dir helfen?',
        'Was gibt’s?',
        'Worum geht’s?',
        'Was liegt an?',
        'Womit kann ich dir helfen?',
      ],
    ),
    formal: _Register(
      morning: [
        'Guten Morgen[, {name}].',
        'Schönen guten Morgen[, {name}].',
        'Einen guten Morgen[, {name}].',
        'Guten Morgen[, {name}], schön, dass Sie anrufen.',
      ],
      day: [
        'Guten Tag[, {name}].',
        'Schönen guten Tag[, {name}].',
        'Hallo[, {name}].',
        'Guten Tag[, {name}], schön, dass Sie anrufen.',
      ],
      evening: [
        'Guten Abend[, {name}].',
        'Schönen guten Abend[, {name}].',
        'Hallo[, {name}].',
        'Guten Abend[, {name}], schön, dass Sie anrufen.',
      ],
      intros: [
        'Hier ist {agent}[ aus dem Team {dept}].',
        'Sie sprechen mit {agent}[ aus dem Team {dept}].',
        'Am Apparat ist {agent}[ aus dem Team {dept}].',
        'Hier spricht {agent}[ aus dem Team {dept}].',
      ],
      questions: [
        'Was kann ich für Sie tun?',
        'Wie kann ich Ihnen helfen?',
        'Womit kann ich Ihnen helfen?',
        'Worum geht es bei Ihnen?',
        'Was darf ich für Sie tun?',
      ],
    ),
  ),
  'en': _Language(
    informal: _Register(
      morning: ['Good morning[, {name}]!', 'Morning[, {name}]!', 'Hi[, {name}]!', 'Hey[, {name}], good morning!'],
      day: ['Hi[, {name}]!', 'Hello[, {name}]!', 'Hey[, {name}]!', 'Hi there[, {name}]!'],
      evening: ['Good evening[, {name}]!', 'Hi[, {name}]!', 'Evening[, {name}]!', 'Hey[, {name}]!'],
      intros: [
        'It’s {agent}[ from {dept}].',
        '{agent} here[, from {dept}].',
        'You’ve got {agent}[ from {dept}].',
        '{agent} speaking[, from {dept}].',
      ],
      questions: [
        'What can I do for you?',
        'How can I help?',
        'What’s up?',
        'What can I help you with?',
        'What’s on your mind?',
        'What do you need?',
      ],
    ),
    formal: _Register(
      morning: [
        'Good morning[, {name}].',
        'Hello[, {name}], good morning.',
        'Good morning[, {name}], thanks for calling.',
        'Good morning[, {name}], nice to hear from you.',
      ],
      day: [
        'Good afternoon[, {name}].',
        'Hello[, {name}].',
        'Good afternoon[, {name}], thanks for calling.',
        'Good afternoon[, {name}], nice to hear from you.',
      ],
      evening: [
        'Good evening[, {name}].',
        'Hello[, {name}].',
        'Good evening[, {name}], thanks for calling.',
        'Good evening[, {name}], nice to hear from you.',
      ],
      intros: [
        'This is {agent}[ from {dept}].',
        'You’re speaking with {agent}[ from {dept}].',
        '{agent} speaking[, from {dept}].',
        'You’ve reached {agent}[ from {dept}].',
      ],
      questions: [
        'How may I help you?',
        'What can I do for you?',
        'How can I help you today?',
        'What can I help you with?',
        'How can I be of help?',
      ],
    ),
  ),
  'es': _Language(
    informal: _Register(
      morning: [
        '¡Buenos días[, {name}]!',
        '¡Hola[, {name}], buenos días!',
        '¡Hola[, {name}]!',
        '¡Muy buenos días[, {name}]!',
      ],
      day: ['¡Hola[, {name}]!', '¡Buenas[, {name}]!', '¡Hola[, {name}], qué tal!', '¡Qué tal[, {name}]!'],
      evening: ['¡Buenas tardes[, {name}]!', '¡Hola[, {name}]!', '¡Buenas[, {name}]!', '¡Buenas noches[, {name}]!'],
      intros: [
        'Soy {agent}[, del equipo de {dept}].',
        'Te habla {agent}[, del equipo de {dept}].',
        'Aquí {agent}[, del equipo de {dept}].',
        'Hablas con {agent}[, del equipo de {dept}].',
      ],
      questions: [
        '¿Qué puedo hacer por ti?',
        '¿En qué te puedo ayudar?',
        '¿Cómo te ayudo?',
        '¿Qué necesitas?',
        'Cuéntame.',
      ],
    ),
    formal: _Register(
      morning: [
        'Buenos días[, {name}].',
        'Hola[, {name}], buenos días.',
        'Muy buenos días[, {name}].',
        'Buenos días[, {name}], gracias por llamar.',
      ],
      day: [
        'Buenas tardes[, {name}].',
        'Hola[, {name}].',
        'Hola[, {name}], gracias por llamar.',
        'Muy buenas tardes[, {name}].',
      ],
      evening: [
        'Buenas tardes[, {name}].',
        'Hola[, {name}].',
        'Buenas tardes[, {name}], gracias por llamar.',
        'Buenas noches[, {name}].',
      ],
      intros: [
        'Le habla {agent}[, del equipo de {dept}].',
        'Soy {agent}[, del equipo de {dept}].',
        'Está hablando con {agent}[, del equipo de {dept}].',
        'Aquí {agent}[, del equipo de {dept}].',
      ],
      questions: [
        '¿En qué puedo ayudarle?',
        '¿Qué puedo hacer por usted?',
        '¿Cómo puedo ayudarle?',
        '¿En qué le puedo servir?',
        '¿Qué necesita?',
      ],
    ),
  ),
  'fr': _Language(
    informal: _Register(
      morning: ['Bonjour[, {name}] !', 'Salut[, {name}] !', 'Coucou[, {name}] !', 'Bonjour à toi[, {name}] !'],
      day: ['Salut[, {name}] !', 'Bonjour[, {name}] !', 'Coucou[, {name}] !', 'Bonjour à toi[, {name}] !'],
      evening: ['Bonsoir[, {name}] !', 'Salut[, {name}] !', 'Coucou[, {name}] !', 'Bonsoir à toi[, {name}] !'],
      intros: [
        'C’est {agent}[, de l’équipe {dept}].',
        'Ici {agent}[, de l’équipe {dept}].',
        '{agent} à l’appareil[, de l’équipe {dept}].',
        'Tu es avec {agent}[, de l’équipe {dept}].',
      ],
      questions: [
        'Qu’est-ce que je peux faire pour toi ?',
        'Comment je peux t’aider ?',
        'Je t’écoute.',
        'Qu’est-ce qui t’amène ?',
        'Dis-moi tout.',
      ],
    ),
    formal: _Register(
      morning: [
        'Bonjour[, {name}].',
        'Bonjour[, {name}], merci de votre appel.',
        'Bonjour à vous[, {name}].',
        'Bonjour et bienvenue[, {name}].',
      ],
      day: [
        'Bonjour[, {name}].',
        'Bonjour[, {name}], merci de votre appel.',
        'Bonjour à vous[, {name}].',
        'Bonjour et bienvenue[, {name}].',
      ],
      evening: [
        'Bonsoir[, {name}].',
        'Bonsoir[, {name}], merci de votre appel.',
        'Bonsoir à vous[, {name}].',
        'Bonsoir et bienvenue[, {name}].',
      ],
      intros: [
        'Ici {agent}[, de l’équipe {dept}].',
        'Vous êtes en ligne avec {agent}[, de l’équipe {dept}].',
        '{agent} à l’appareil[, de l’équipe {dept}].',
        'C’est {agent}[, de l’équipe {dept}].',
      ],
      questions: [
        'Que puis-je faire pour vous ?',
        'Comment puis-je vous aider ?',
        'En quoi puis-je vous être utile ?',
        'Qu’est-ce qui vous amène ?',
        'Je vous écoute.',
      ],
    ),
  ),
  'it': _Language(
    informal: _Register(
      morning: [
        'Buongiorno[, {name}]!',
        'Ciao[, {name}]!',
        'Ciao[, {name}], buongiorno!',
        'Buongiorno a te[, {name}]!',
      ],
      day: ['Ciao[, {name}]!', 'Ehi[, {name}]!', 'Salve[, {name}]!', 'Eccoti[, {name}]!'],
      evening: ['Buonasera[, {name}]!', 'Ciao[, {name}]!', 'Ehi[, {name}]!', 'Buonasera a te[, {name}]!'],
      intros: [
        'Sono {agent}[, del team {dept}].',
        'Qui {agent}[, del team {dept}].',
        'Ti parla {agent}[, del team {dept}].',
        '{agent} al telefono[, del team {dept}].',
      ],
      questions: [
        'Cosa posso fare per te?',
        'Come posso aiutarti?',
        'Dimmi pure.',
        'Di cosa hai bisogno?',
        'Cosa ti serve?',
      ],
    ),
    formal: _Register(
      morning: [
        'Buongiorno[, {name}].',
        'Salve[, {name}], buongiorno.',
        'Buongiorno[, {name}], grazie della chiamata.',
        'Buongiorno a Lei[, {name}].',
      ],
      day: [
        'Buongiorno[, {name}].',
        'Salve[, {name}].',
        'Buongiorno[, {name}], grazie della chiamata.',
        'Buon pomeriggio[, {name}].',
      ],
      evening: [
        'Buonasera[, {name}].',
        'Salve[, {name}].',
        'Buonasera[, {name}], grazie della chiamata.',
        'Buonasera a Lei[, {name}].',
      ],
      intros: [
        'Sono {agent}[, del team {dept}].',
        'Le parla {agent}[, del team {dept}].',
        '{agent} al telefono[, del team {dept}].',
        'È in linea con {agent}[, del team {dept}].',
      ],
      questions: [
        'Come posso aiutarLa?',
        'Cosa posso fare per Lei?',
        'In cosa posso esserLe utile?',
        'Di cosa ha bisogno?',
        'Mi dica pure.',
      ],
    ),
  ),
  'nl': _Language(
    informal: _Register(
      morning: ['Goedemorgen[, {name}]!', 'Morgen[, {name}]!', 'Hoi[, {name}]!', 'Goeiemorgen[, {name}]!'],
      day: ['Hoi[, {name}]!', 'Hallo[, {name}]!', 'Hé[, {name}]!', 'Hallo daar[, {name}]!'],
      evening: ['Goedenavond[, {name}]!', 'Hoi[, {name}]!', 'Hallo[, {name}]!', 'Hé[, {name}]!'],
      intros: [
        'Met {agent}[ van het team {dept}].',
        'Je spreekt met {agent}[ van het team {dept}].',
        'Je hebt {agent}[ van het team {dept}] aan de lijn.',
        '{agent} hier[, van het team {dept}].',
      ],
      questions: [
        'Wat kan ik voor je doen?',
        'Waarmee kan ik je helpen?',
        'Vertel het maar.',
        'Wat is er?',
        'Waar gaat het over?',
      ],
    ),
    formal: _Register(
      morning: [
        'Goedemorgen[, {name}].',
        'Hallo[, {name}], goedemorgen.',
        'Goedemorgen[, {name}], fijn dat u belt.',
        'Een goedemorgen[, {name}].',
      ],
      day: [
        'Goedemiddag[, {name}].',
        'Hallo[, {name}].',
        'Goedemiddag[, {name}], fijn dat u belt.',
        'Een goedemiddag[, {name}].',
      ],
      evening: [
        'Goedenavond[, {name}].',
        'Hallo[, {name}].',
        'Goedenavond[, {name}], fijn dat u belt.',
        'Een goedenavond[, {name}].',
      ],
      intros: [
        'U spreekt met {agent}[ van het team {dept}].',
        'Met {agent}[ van het team {dept}].',
        'U hebt {agent}[ van het team {dept}] aan de lijn.',
        '{agent} hier[, van het team {dept}].',
      ],
      questions: [
        'Wat kan ik voor u doen?',
        'Waarmee kan ik u helpen?',
        'Hoe kan ik u helpen?',
        'Waar kan ik u mee helpen?',
        'Waar gaat het om?',
      ],
    ),
  ),
  // The formal Polish forms leave out Pan and Pani: whom the call greets is
  // not known.
  'pl': _Language(
    informal: _Register(
      morning: ['Dzień dobry[, {name}]!', 'Cześć[, {name}]!', 'Hej[, {name}]!', 'Witaj[, {name}]!'],
      day: ['Cześć[, {name}]!', 'Hej[, {name}]!', 'Witaj[, {name}]!', 'Dzień dobry[, {name}]!'],
      evening: ['Dobry wieczór[, {name}]!', 'Cześć[, {name}]!', 'Hej[, {name}]!', 'Witaj[, {name}]!'],
      intros: [
        'Tu {agent}[ z zespołu {dept}].',
        'Mówi {agent}[ z zespołu {dept}].',
        'Z tej strony {agent}[ z zespołu {dept}].',
        'Tu mówi {agent}[ z zespołu {dept}].',
      ],
      questions: [
        'Co mogę dla ciebie zrobić?',
        'W czym mogę ci pomóc?',
        'Jak mogę ci pomóc?',
        'Czego potrzebujesz?',
        'O co chodzi?',
      ],
    ),
    formal: _Register(
      morning: [
        'Dzień dobry[, {name}].',
        'Witam serdecznie[, {name}].',
        'Dzień dobry[, {name}], dziękuję za telefon.',
        'Uprzejmie witam[, {name}].',
      ],
      day: [
        'Dzień dobry[, {name}].',
        'Witam serdecznie[, {name}].',
        'Dzień dobry[, {name}], dziękuję za telefon.',
        'Uprzejmie witam[, {name}].',
      ],
      evening: [
        'Dobry wieczór[, {name}].',
        'Witam serdecznie[, {name}].',
        'Dobry wieczór[, {name}], dziękuję za telefon.',
        'Uprzejmie witam[, {name}].',
      ],
      intros: [
        'Tu {agent}[ z zespołu {dept}].',
        'Mówi {agent}[ z zespołu {dept}].',
        'Z tej strony {agent}[ z zespołu {dept}].',
        'Tu mówi {agent}[ z zespołu {dept}].',
      ],
      questions: [
        'W czym mogę pomóc?',
        'Czym mogę służyć?',
        'Jak mogę pomóc?',
        'Czego dotyczy sprawa?',
        'W czym mogę dziś pomóc?',
      ],
    ),
  ),
  // Without words that take the speaker's or the listener's gender.
  'pt': _Language(
    informal: _Register(
      morning: ['Bom dia[, {name}]!', 'Olá[, {name}]!', 'Oi[, {name}], bom dia!', 'Oi[, {name}]!'],
      day: ['Olá[, {name}]!', 'Oi[, {name}]!', 'Boa tarde[, {name}]!', 'Oi[, {name}], boa tarde!'],
      evening: ['Boa noite[, {name}]!', 'Olá[, {name}]!', 'Oi[, {name}]!', 'Oi[, {name}], boa noite!'],
      intros: [
        'Aqui é {agent}[, da equipe {dept}].',
        'É {agent}[, da equipe {dept}].',
        'Quem fala é {agent}[, da equipe {dept}].',
        'Aqui quem fala é {agent}[, da equipe {dept}].',
      ],
      questions: [
        'Em que posso ajudar?',
        'O que posso fazer por você?',
        'Como posso ajudar?',
        'Do que você precisa?',
        'O que manda?',
      ],
    ),
    formal: _Register(
      morning: ['Bom dia[, {name}].', 'Olá[, {name}], bom dia.', 'Muito bom dia[, {name}].', 'Um bom dia[, {name}].'],
      day: ['Boa tarde[, {name}].', 'Olá[, {name}], boa tarde.', 'Olá[, {name}].', 'Uma boa tarde[, {name}].'],
      evening: ['Boa noite[, {name}].', 'Olá[, {name}], boa noite.', 'Olá[, {name}].', 'Uma boa noite[, {name}].'],
      intros: [
        'Aqui é {agent}[, da equipe {dept}].',
        'Fala {agent}[, da equipe {dept}].',
        'Quem fala é {agent}[, da equipe {dept}].',
        'Está falando com {agent}[, da equipe {dept}].',
      ],
      questions: [
        'Em que posso ser útil?',
        'Como posso ajudar?',
        'Em que posso ajudar hoje?',
        'Como posso ser útil?',
        'Do que precisa?',
      ],
    ),
  ),
  'ja': _Language(
    join: '',
    informal: _Register(
      morning: ['おはようございます[、{name}さん]！', 'おはよう[、{name}さん]！', '[{name}さん、]おはようございます！', 'おはよー[、{name}さん]！'],
      day: ['こんにちは[、{name}さん]！', '[{name}さん、]こんにちは！', 'どうも[、{name}さん]！', 'やあ[、{name}さん]！'],
      evening: ['こんばんは[、{name}さん]！', '[{name}さん、]こんばんは！', 'お疲れさまです[、{name}さん]！', 'やあ[、{name}さん]！'],
      intros: [
        '[{dept}チームの]{agent}です。',
        'こちら[{dept}チームの]{agent}です。',
        '[{dept}チームの]{agent}だよ。',
        'はい、[{dept}チームの]{agent}です。',
      ],
      questions: ['何か手伝えることある？', 'どうしたの？', '何でも聞いてね。', 'どんな用件？', '何かあった？'],
    ),
    formal: _Register(
      morning: [
        'おはようございます[、{name}様]。',
        '[{name}様、]おはようございます。',
        '[{name}様、]お電話ありがとうございます。',
        'いつもお世話になっております[、{name}様]。',
      ],
      day: ['こんにちは[、{name}様]。', '[{name}様、]こんにちは。', '[{name}様、]お電話ありがとうございます。', 'いつもお世話になっております[、{name}様]。'],
      evening: ['こんばんは[、{name}様]。', '[{name}様、]こんばんは。', '[{name}様、]お電話ありがとうございます。', 'いつもお世話になっております[、{name}様]。'],
      intros: [
        '[{dept}チームの]{agent}でございます。',
        '[{dept}チームの]{agent}と申します。',
        'はい、[{dept}チームの]{agent}でございます。',
        '[{dept}チームの]{agent}が承ります。',
      ],
      questions: ['ご用件をお伺いします。', 'どのようなご用件でしょうか。', '何かお手伝いできることはございますか。', '本日はどのようなご用件でしょうか。', 'どういったことでお困りでしょうか。'],
    ),
  ),
  'zh': _Language(
    join: '',
    informal: _Register(
      morning: ['早上好[，{name}]！', '早[，{name}]！', '[{name}，]早上好！', '早啊[，{name}]！'],
      day: ['你好[，{name}]！', '嗨[，{name}]！', '[{name}，]你好！', '哈喽[，{name}]！'],
      evening: ['晚上好[，{name}]！', '嗨[，{name}]！', '[{name}，]晚上好！', '哈喽[，{name}]！'],
      intros: ['我是[{dept}团队的]{agent}。', '这里是[{dept}团队的]{agent}。', '[{dept}团队的]{agent}在这儿。', '我是{agent}[，{dept}团队的]。'],
      questions: ['有什么我可以帮你的吗？', '需要我做点什么？', '你说吧。', '怎么了？', '有什么事吗？'],
    ),
    formal: _Register(
      morning: ['早上好[，{name}]。', '[{name}，]早上好。', '您好[，{name}]。', '早上好[，{name}]，感谢您的来电。'],
      day: ['您好[，{name}]。', '[{name}，]您好。', '下午好[，{name}]。', '您好[，{name}]，感谢您的来电。'],
      evening: ['晚上好[，{name}]。', '[{name}，]晚上好。', '您好[，{name}]。', '晚上好[，{name}]，感谢您的来电。'],
      intros: [
        '我是[{dept}团队的]{agent}。',
        '这里是[{dept}团队的]{agent}。',
        '[{dept}团队的]{agent}为您服务。',
        '[{dept}团队的]{agent}很高兴为您服务。',
      ],
      questions: ['请问有什么可以帮您？', '请问您需要什么帮助？', '有什么可以为您效劳的？', '请问有什么需要？', '请问今天有什么可以帮您？'],
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

  /// The caller's clock, which the written greeting (#513) is asked with.
  DateTime get now => _now();

  /// The greeting for this call with [agentId], remembered as its last.
  Future<Greeting?> choose({
    required String agentId,
    required String agentName,
    required String language,
    required GreetingFacts facts,
  }) async {
    final g = await compose(agentId: agentId, agentName: agentName, language: language, facts: facts);
    if (g != null) await remember(agentId, g);
    return g;
  }

  /// A template greeting for this call with [agentId], other than the last
  /// one said; not yet remembered — [remember] it once it is said.
  Future<Greeting?> compose({
    required String agentId,
    required String agentName,
    required String language,
    required GreetingFacts facts,
  }) async => composeGreeting(
    language: language,
    agentName: agentName,
    facts: facts,
    time: _now(),
    last: await memory.last(agentId),
    random: _random,
  );

  /// Keeps [g] as the last greeting to [agentId]; a written one (#513) is
  /// not a template and leaves the last template as it was.
  Future<void> remember(String agentId, Greeting g) async {
    if (g.key != writtenGreetingKey) await memory.remember(agentId, g.key);
  }
}

/// The key of a greeting the instance wrote (#513).
const writtenGreetingKey = 'written';

/// [t] in ISO 8601 with its offset (`2026-09-30T09:12:00+02:00`), as the
/// written greeting (#513) takes the caller's clock.
String isoWithOffset(DateTime t) {
  String two(int n) => n.abs().toString().padLeft(2, '0');
  final o = t.timeZoneOffset;
  final sign = o.isNegative ? '-' : '+';
  final m = o.inMinutes.abs();
  return '${t.year.toString().padLeft(4, '0')}-${two(t.month)}-${two(t.day)}'
      'T${two(t.hour)}:${two(t.minute)}:${two(t.second)}$sign${two(m ~/ 60)}:${two(m % 60)}';
}

/// The English name of [t]'s weekday, as the written greeting is told it.
String weekdayName(DateTime t) =>
    const ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'][t.weekday - 1];

/// The greeting of one call on its way (#513): the one the instance writes
/// from the situation, asked as the line starts ringing and synthesised as
/// soon as it arrives, and a template greeting as the fallback — made
/// ready when the written one is not there (the instance answered nothing,
/// or failed) or when the call connects before it is.
class GreetingInFlight {
  GreetingInFlight({
    required Future<Greeting?> written,
    required this.template,
    required this.synthesise,
    void Function(String what)? log,
  }) : _log = log ?? ((_) {}) {
    written.then(
      (g) {
        _writtenSettled = true;
        _written = g;
        if (_picked) return;
        if (g == null) {
          _startTemplate();
        } else {
          _prepare(g).then((ok) {
            _writtenAudio = ok;
            if (!ok) _startTemplate();
            _changed();
          });
        }
        _changed();
      },
      onError: (Object e) {
        _log('no written greeting: $e');
        _writtenSettled = true;
        _startTemplate();
        _changed();
      },
    );
  }

  /// Chooses the template greeting, when it is needed.
  final Future<Greeting?> Function() template;

  /// Makes a greeting's audio ahead; true when it is ready.
  final Future<bool> Function(Greeting g) synthesise;
  final void Function(String what) _log;

  bool _writtenSettled = false;
  Greeting? _written;
  bool? _writtenAudio;

  bool _templateStarted = false;
  bool _templateSettled = false;
  Greeting? _templateGreeting;
  bool? _templateAudio;

  Completer<void>? _change;

  /// Chosen: what arrives after is not made any more.
  bool _picked = false;

  void _changed() {
    final c = _change;
    _change = null;
    if (c != null && !c.isCompleted) c.complete();
  }

  Future<bool> _prepare(Greeting g) => synthesise(g).catchError((Object e) {
    _log('greeting not synthesised: $e');
    return false;
  });

  void _startTemplate() {
    if (_templateStarted || _picked) return;
    _templateStarted = true;
    template().then(
      (g) {
        _templateGreeting = g;
        _templateSettled = true;
        _changed();
        if (g != null && !_picked) {
          _prepare(g).then((ok) {
            _templateAudio = ok;
            _changed();
          });
        }
      },
      onError: (Object e) {
        _log('no template greeting: $e');
        _templateSettled = true;
        _changed();
      },
    );
  }

  /// What to say as the call connects, and whether the provider's audio for
  /// it is ready; null when there is nothing. Waits at most for [deadline]:
  /// the written greeting in the provider's voice first, the template when
  /// the written one is not coming or not ready by then; at the deadline
  /// whatever is known, in the Mac's voice when its audio is not ready.
  Future<(Greeting, bool)?> pick(Future<void> deadline) async {
    final c = await _choose(deadline);
    _picked = true;
    return c;
  }

  Future<(Greeting, bool)?> _choose(Future<void> deadline) async {
    if (!_writtenSettled) _startTemplate();
    var due = false;
    unawaited(
      deadline.then((_) {
        due = true;
        _changed();
      }),
    );
    while (true) {
      final w = _written, t = _templateGreeting;
      if (w != null && _writtenAudio == true) return (w, true);
      final writtenOut = _writtenSettled && (w == null || _writtenAudio == false);
      if (writtenOut || due) {
        if (t != null && _templateAudio == true) return (t, true);
        // Nothing that could still become ready: say what there is.
        final templateOut = !_templateStarted || (_templateSettled && (t == null || _templateAudio == false));
        if (due || templateOut) {
          final g = w ?? t;
          if (g != null) return (g, false);
          if (_templateSettled || due) return null;
        }
      }
      await (_change ??= Completer<void>()).future;
    }
  }
}
