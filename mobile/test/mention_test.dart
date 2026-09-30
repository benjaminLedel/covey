import 'package:covey_mobile/mention.dart';
import 'package:covey_mobile/models.dart';
import 'package:flutter_test/flutter_test.dart';

// The same cases as web/src/team/Erwaehnung.test.ts (#440): the app offers
// what the web offers, and inserts what the server matches.
void main() {
  group('openMention', () {
    test('opens on @ at the start and after a space', () {
      expect(openMention('@', 1), (start: 0, query: ''));
      expect(openMention('hi @Ad', 6), (start: 3, query: 'ad'));
    });
    test('stays shut inside a word and after the handle is finished', () {
      expect(openMention('mail@example', 12), isNull);
      expect(openMention('@ada ', 5), isNull);
    });
    test('reads only what stands before the caret', () {
      expect(openMention('@ki und mehr', 3), (start: 0, query: 'ki'));
    });
    test('takes letters beyond ASCII', () {
      expect(openMention('@Jürg', 5), (start: 0, query: 'jürg'));
    });
    test('a caret outside the text opens nothing', () {
      expect(openMention('@a', -1), isNull);
      expect(openMention('@a', 3), isNull);
    });
  });

  final lunch = Conversation(
    id: 'c1',
    kind: 'group',
    title: 'Mittag',
    members: [
      ConversationMember(kind: 'human', id: 'me', name: 'Ada Lovelace', role: 'owner'),
      ConversationMember(kind: 'human', id: 'h2', name: 'Bob Builder'),
      ConversationMember(kind: 'agent', id: 'a1', name: 'Bea Buchhalterin', slug: 'bea'),
      ConversationMember(kind: 'human', id: 'h3', name: 'Cid Left', left: true),
    ],
  );

  test('the candidates are the other active members, agents first, by slug or first name', () {
    final k = mentionCandidates(lunch, 'me');
    expect([for (final c in k) c.handle], ['bea', 'Bob']);
  });

  test('a direct conversation offers nobody', () {
    final direct = Conversation(id: 'd', kind: 'direct', members: lunch.members.take(2).toList());
    expect(mentionCandidates(direct, 'me'), isEmpty);
  });

  test('a match is a handle that starts with the query, or a name that contains it', () {
    final k = mentionCandidates(lunch, 'me');
    expect([for (final c in mentionMatches(k, '')) c.handle], ['bea', 'Bob']);
    expect([for (final c in mentionMatches(k, 'b')) c.handle], ['bea', 'Bob']);
    expect([for (final c in mentionMatches(k, 'buch')) c.handle], ['bea']);
    expect([for (final c in mentionMatches(k, 'uil')) c.handle], ['Bob']);
    expect(mentionMatches(k, 'zz'), isEmpty);
  });

  test('inserting puts the handle and a space in, and the caret after them', () {
    expect(insertMention('hi @b', 5, 3, 'bea'), (text: 'hi @bea ', caret: 8));
    expect(insertMention('@b  und', 2, 0, 'bea'), (text: '@bea und', caret: 5));
  });
}
