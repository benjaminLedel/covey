import '../face.dart' show faceHash;

/// What of an agent's reply is spoken in a call (#494), and whether some
/// of it was left for the chat.
class SpokenText {
  const SpokenText(this.text, {this.cut = false});

  final String text;

  /// Part of the reply is not spoken: it is too long to listen to, or it is
  /// code. The call says that the rest is in the chat.
  final bool cut;
}

/// A reply longer than this, or of more sentences and not short, is cut to
/// its first two sentences: about fifteen seconds of listening.
const _speakAllUpTo = 320;
const _sentencesSpokenWhole = 4;
const _sentencesWhenCut = 2;

/// An agent's reply as it is to be heard: the markdown the chat renders
/// taken out (emphasis, headings, lists, quotes, tables), a link read as its
/// words, a bare address and code left out, and a long reply cut to its
/// first two sentences.
SpokenText textForSpeech(String reply) {
  var t = reply.replaceAll('\r\n', '\n');
  var cut = false;

  // Code is read in the chat, not aloud.
  final fenced = RegExp(r'```[\s\S]*?(```|$)');
  if (fenced.hasMatch(t)) {
    cut = true;
    t = t.replaceAll(fenced, '\n');
  }
  t = t
      // Images keep their description, links their words.
      .replaceAllMapped(RegExp(r'!\[([^\]]*)\]\([^)]*\)'), (m) => m[1]!)
      .replaceAllMapped(RegExp(r'\[([^\]]+)\]\([^)]*\)'), (m) => m[1]!)
      .replaceAllMapped(RegExp(r'<(https?://[^>]+)>'), (_) => '')
      .replaceAll(RegExp(r'https?://\S*[^\s.,;:!?)]'), '')
      .replaceAll(RegExp(r'<[^>\n]+>'), '')
      .replaceAllMapped(RegExp(r'`([^`\n]+)`'), (m) => m[1]!);

  final lines = <String>[];
  for (var line in t.split('\n')) {
    line = line.trim();
    if (line.isEmpty) continue;
    // A table's rule, a horizontal rule.
    if (RegExp(r'^[|:\-\s*_=]+$').hasMatch(line)) continue;
    line = line
        .replaceFirst(RegExp(r'^#{1,6}\s+'), '')
        .replaceFirst(RegExp(r'^>\s?'), '')
        .replaceFirst(RegExp(r'^([-*+•]|\d+[.)])\s+'), '')
        .replaceFirst(RegExp(r'^\[[ xX]\]\s+'), '');
    if (line.contains('|')) {
      line = line.split('|').map((c) => c.trim()).where((c) => c.isNotEmpty).join(', ');
    }
    line = line
        .replaceAllMapped(RegExp(r'(\*\*|__)(.+?)\1'), (m) => m[2]!)
        .replaceAllMapped(RegExp(r'(?<![\w*])[*_](?!\s)(.+?)(?<!\s)[*_](?![\w*])'), (m) => m[1]!)
        .replaceAllMapped(RegExp(r'~~(.+?)~~'), (m) => m[1]!)
        .replaceAll(RegExp(r'\s+'), ' ')
        .trim();
    if (line.isEmpty) continue;
    // A line without its own full stop is a list item or a heading: it
    // ends where it ends, heard as a pause.
    if (!RegExp(r'[.!?…:;,]$').hasMatch(line)) line = '$line.';
    lines.add(line);
  }
  var out = lines
      .join(' ')
      .replaceAllMapped(RegExp(r'\s+([.,!?;:])'), (m) => m[1]!)
      .replaceAll(RegExp(r'\(\s*\)'), '')
      .trim();

  final sentences = splitSentences(out);
  if (out.length > _speakAllUpTo || (sentences.length > _sentencesSpokenWhole && out.length > 200)) {
    var short = sentences.take(_sentencesWhenCut).join(' ');
    // One endless sentence: cut at a word.
    if (short.length > _speakAllUpTo * 1.5) {
      final at = short.lastIndexOf(' ', _speakAllUpTo);
      short = '${short.substring(0, at > 0 ? at : _speakAllUpTo)} …';
    }
    cut = cut || short.length < out.length;
    out = short;
  }
  return SpokenText(out, cut: cut);
}

/// The sentences of [text]: split after . ! ? … where the next one starts
/// with a capital, a digit or a quote — "z. B. drei" and "3.5" stay whole.
List<String> splitSentences(String text) {
  final out = <String>[];
  var start = 0;
  for (final m in RegExp(r'[.!?…]+["”»)]?\s+(?=[\p{Lu}\p{N}"„«(])', unicode: true).allMatches(text)) {
    final s = text.substring(start, m.end).trim();
    // "z. B.", "e.g.", "Dr.": a single letter or a known abbreviation
    // before the stop is not the end of a sentence.
    if (RegExp(
      r'(^|[\s.])(\p{L}|dr|mr|mrs|ms|prof|ca|vs|bzw|usw|etc|inkl|evtl|ggf|nr|st|mme|sr|sra)\.$',
      unicode: true,
      caseSensitive: false,
    ).hasMatch(s)) {
      continue;
    }
    out.add(s);
    start = m.end;
  }
  final rest = text.substring(start).trim();
  if (rest.isNotEmpty) out.add(rest);
  return out;
}

/// One of the system's voices, as the Mac lists them.
class SystemVoice {
  const SystemVoice({required this.id, required this.name, required this.language, this.quality = 1});

  factory SystemVoice.fromJson(Map<Object?, Object?> j) => SystemVoice(
    id: j['id'] as String? ?? '',
    name: j['name'] as String? ?? '',
    language: j['language'] as String? ?? '',
    quality: (j['quality'] as num?)?.toInt() ?? 1,
  );

  final String id;
  final String name;

  /// BCP 47, `de-DE`.
  final String language;

  /// 1 default, 2 enhanced, 3 premium.
  final int quality;
}

/// The voices of macOS that are effects rather than speakers.
const _novelty = {
  'albert', 'bad news', 'bahh', 'bells', 'boing', 'bubbles', 'cellos', 'deranged', //
  'good news', 'hysterical', 'organ', 'pipe organ', 'princess', 'trinoids', 'whisper', 'wobble',
  'zarvox', 'jester', 'superstar', 'fred', 'junior', 'kathy', 'ralph',
};

bool _isNovelty(SystemVoice v) =>
    _novelty.contains(v.name.toLowerCase()) ||
    v.id.contains('speech.synthesis.voice.') &&
        _novelty.any((n) => v.id.toLowerCase().endsWith('.${n.replaceAll(' ', '')}'));

/// The voice an agent speaks with in [language] (#494): among the installed
/// voices of that language, the best that are there — premium, then
/// enhanced, then the rest — and among those always the same one for the
/// same agent, so two agents sound different and one agent sounds like
/// itself in every call. The robotic voices (Eloquence, the effects) only
/// when there is nothing else. Null: no voice for that language; the system
/// picks.
SystemVoice? chooseVoice(List<SystemVoice> voices, String language, String agentId) {
  final base = language.split(RegExp('[-_]')).first.toLowerCase();
  if (base.isEmpty) return null;
  final ofLanguage = voices.where((v) => v.language.split(RegExp('[-_]')).first.toLowerCase() == base).toList();
  if (ofLanguage.isEmpty) return null;
  final human = ofLanguage.where((v) => !_isNovelty(v) && !v.id.contains('eloquence')).toList();
  final pool = human.isEmpty ? ofLanguage : human;
  pool.sort((a, b) => b.quality != a.quality ? b.quality.compareTo(a.quality) : a.id.compareTo(b.id));
  // The best tier, widened by the next one down until there are at least
  // two to choose between — one premium voice for everybody would make all
  // agents sound alike.
  var tier = pool.first.quality;
  var picks = pool.where((v) => v.quality >= tier).toList();
  while (picks.length < 2 && tier > 1) {
    tier--;
    picks = pool.where((v) => v.quality >= tier).toList();
  }
  picks.sort((a, b) => a.id.compareTo(b.id));
  return picks[faceHash(agentId) % picks.length];
}
