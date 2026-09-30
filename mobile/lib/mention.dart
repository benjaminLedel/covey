import 'models.dart';

/// Mentions in a group (#440), as the web has them (web/src/team/Erwaehnung.tsx):
/// an agent in a group answers only when it is addressed, so "@" is the way
/// to address it, and typing the whole handle from memory is where it goes
/// wrong. Typing "@" offers the members that match what follows; a tap puts
/// the handle in. What goes in is what the server matches (chat.Addressed):
/// an agent's slug, a person's first name.
class MentionCandidate {
  const MentionCandidate({required this.member, required this.handle});

  final ConversationMember member;
  final String handle;

  String get name => member.name;
}

/// The "@…" the caret stands in: where the "@" is and what follows it,
/// lower-cased — or null. The "@" opens at the start or after white space,
/// never inside a word ("mail@example" is an address).
({int start, String query})? openMention(String text, int caret) {
  if (caret < 0 || caret > text.length) return null;
  final before = text.substring(0, caret);
  final m = RegExp(r'(^|\s)@([\p{L}\p{N}_.-]*)$', unicode: true).firstMatch(before);
  if (m == null) return null;
  final query = m.group(2)!;
  return (start: before.length - query.length - 1, query: query.toLowerCase());
}

/// The other members of a group as mention candidates, agents first — they
/// are the ones a mention wakes.
List<MentionCandidate> mentionCandidates(Conversation c, String meId) {
  if (!c.group) return const [];
  // Two passes rather than a sort: Dart's sort is not stable, and the
  // members keep the order the instance lists them in.
  final others = [
    for (final m in c.active)
      if (m.agent) m,
    for (final m in c.active)
      if (!m.agent && !(m.human && m.id == meId)) m,
  ];
  String first(String name) => name.trim().split(RegExp(r'\s+')).first;
  return [
    for (final m in others) MentionCandidate(member: m, handle: m.agent && m.slug.isNotEmpty ? m.slug : first(m.name)),
  ];
}

/// Who matches what follows the "@": the handle starting with it, or the
/// name containing it; six at most.
List<MentionCandidate> mentionMatches(List<MentionCandidate> all, String query) => all
    .where((k) => query.isEmpty || k.handle.toLowerCase().startsWith(query) || k.name.toLowerCase().contains(query))
    .take(6)
    .toList();

/// The text with the open mention replaced by "@handle " and where the caret
/// goes after it.
({String text, int caret}) insertMention(String text, int caret, int start, String handle) {
  final before = text.substring(0, start);
  final after = text.substring(caret).replaceFirst(RegExp(r'^\s+'), '');
  return (text: '$before@$handle $after', caret: before.length + handle.length + 2);
}
