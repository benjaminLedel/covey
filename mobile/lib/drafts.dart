import 'package:flutter/foundation.dart';

/// What was typed into a conversation's composer and not sent yet (#621),
/// kept outside the screen that shows it.
///
/// The composer's controller belongs to the thread screen, and that screen
/// is disposed whenever the person looks at something else: the phone pops
/// its route, the wide layout replaces its detail pane. The text went with
/// it. Here it outlives the screen, keyed by the thread it was typed in, for
/// as long as the app runs. It is not written to disk: an unsent reply is
/// not a setting, and the settings file is not the place for what somebody
/// wrote to a colleague.
class Drafts {
  Drafts._();

  static final Drafts instance = Drafts._();

  final _texts = <String, String>{};

  /// The key of an agent's thread.
  static String agent(String agentId) => 'agent:$agentId';

  /// The key of a conversation.
  static String conversation(String conversationId) => 'conversation:$conversationId';

  /// The draft held for [key], empty when there is none.
  String read(String key) => _texts[key] ?? '';

  /// Keeps [text] for [key]; an empty or blank text removes the entry.
  void write(String key, String text) {
    if (text.trim().isEmpty) {
      _texts.remove(key);
    } else {
      _texts[key] = text;
    }
  }

  /// Forgets the draft of [key] — after it was sent.
  void clear(String key) => _texts.remove(key);

  /// Forgets every draft.
  @visibleForTesting
  void clearAll() => _texts.clear();
}
