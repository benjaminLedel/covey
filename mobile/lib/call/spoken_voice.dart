/// How an agent sounds in a call (#497): what the organisation's voice
/// provider is told — which of its voices, a short English style hint and
/// the speed. The instance resolves it per conversation from the agent's
/// covey voice (spec/24): the `speech` field of the chat voice in effect,
/// and a style hint that is the voice's own or derived from the chat tone.
class SpokenVoice {
  const SpokenVoice({this.voice = '', this.instructions = '', this.speed = 0});

  /// The voice's name at the provider; empty uses the organisation's
  /// default voice.
  final String voice;

  /// How the provider is to speak, in a short English line.
  final String instructions;

  /// 1 is the voice's own speed; 0 leaves it at that.
  final double speed;

  /// The answer of `GET /conversations/{id}/speech`: its `speech` (null
  /// when the voice sets none) and its `instructions`.
  factory SpokenVoice.fromConversation(Map<String, dynamic> j) {
    final s = j['speech'];
    final speech = s is Map ? s : const {};
    final own = speech['instructions'];
    final derived = j['instructions'];
    return SpokenVoice(
      voice: speech['voice'] is String ? speech['voice'] as String : '',
      instructions: own is String && own.isNotEmpty ? own : (derived is String ? derived : ''),
      speed: (speech['speed'] as num?)?.toDouble() ?? 0,
    );
  }

  @override
  bool operator ==(Object other) =>
      other is SpokenVoice && other.voice == voice && other.instructions == instructions && other.speed == speed;

  @override
  int get hashCode => Object.hash(voice, instructions, speed);

  @override
  String toString() => 'provider voice ${voice.isEmpty ? 'default' : voice}${speed == 0 ? '' : ' ×$speed'}';
}
