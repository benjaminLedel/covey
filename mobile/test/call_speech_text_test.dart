import 'package:covey_mobile/call/speech_text.dart';
import 'package:flutter_test/flutter_test.dart';

// What of a reply is heard in a call (#494), and in which voice.

void main() {
  group('text for speech', () {
    test('markdown is taken out, links read as their words', () {
      final s = textForSpeech(
        '## Stand\n\n**Drei** Tickets sind _offen_:\n\n- #12 Login\n- #14 Export\n\n'
        'Details im [Board](https://example.org/board) oder unter https://example.org/x. Der Befehl `make test` läuft.',
      );
      expect(
        s.text,
        'Stand. Drei Tickets sind offen: #12 Login. #14 Export. Details im Board oder unter. Der Befehl make test läuft.',
      );
      expect(s.cut, isFalse);
    });

    test('code is not read aloud, and the rest is said to be in the chat', () {
      final s = textForSpeech('So geht es:\n```bash\nrm -rf build\nmake\n```\nDann neu starten.');
      expect(s.text, 'So geht es: Dann neu starten.');
      expect(s.cut, isTrue);
    });

    test('a table is read row by row', () {
      final s = textForSpeech('| Agent | Stand |\n|---|---|\n| Ada | fertig |');
      expect(s.text, 'Agent, Stand. Ada, fertig.');
    });

    test('a long reply is cut to its first two sentences', () {
      final long = [
        'Ich habe die drei Rechnungen von Initech geprüft.',
        'Zwei davon sind bezahlt, eine ist seit März offen.',
        'Die offene hat die Nummer 2024-117 und lautet auf 4.200 Euro.',
        'Ich habe eine Erinnerung vorbereitet, aber noch nicht verschickt.',
        'Soll ich sie heute noch senden oder erst mit dem Kunden sprechen?',
      ].join(' ');
      final s = textForSpeech(long);
      expect(s.cut, isTrue);
      expect(
        s.text,
        'Ich habe die drei Rechnungen von Initech geprüft. Zwei davon sind bezahlt, eine ist seit März offen.',
      );
    });

    test('a short reply is spoken whole', () {
      final s = textForSpeech('Gern, mach ich. Bis gleich!');
      expect(s.text, 'Gern, mach ich. Bis gleich!');
      expect(s.cut, isFalse);
    });

    test('abbreviations and numbers do not end a sentence', () {
      expect(splitSentences('Das kostet z. B. 3.5 Euro. Dr. Meier weiß es. Fertig!'), [
        'Das kostet z. B. 3.5 Euro.',
        'Dr. Meier weiß es.',
        'Fertig!',
      ]);
    });
  });

  group('voice choice', () {
    const voices = [
      SystemVoice(id: 'com.apple.voice.compact.de-DE.Anna', name: 'Anna', language: 'de-DE'),
      SystemVoice(id: 'com.apple.voice.enhanced.de-DE.Anna', name: 'Anna', language: 'de-DE', quality: 2),
      SystemVoice(id: 'com.apple.voice.enhanced.de-DE.Markus', name: 'Markus', language: 'de-DE', quality: 2),
      SystemVoice(id: 'com.apple.voice.enhanced.de-CH.Petra', name: 'Petra', language: 'de-CH', quality: 2),
      SystemVoice(id: 'com.apple.eloquence.de-DE.Grandpa', name: 'Grandpa', language: 'de-DE'),
      SystemVoice(id: 'com.apple.speech.synthesis.voice.Bahh', name: 'Bahh', language: 'en-US'),
      SystemVoice(id: 'com.apple.voice.compact.en-US.Samantha', name: 'Samantha', language: 'en-US'),
      SystemVoice(id: 'com.apple.voice.premium.en-GB.Jamie', name: 'Jamie', language: 'en-GB', quality: 3),
    ];

    test('the same agent always gets the same voice', () {
      final a = chooseVoice(voices, 'de', 'agent-1');
      for (var i = 0; i < 5; i++) {
        expect(chooseVoice([...voices.reversed], 'de', 'agent-1')?.id, a?.id);
      }
    });

    test('the best voices of the language are chosen from, and agents differ', () {
      final picked = {for (var i = 0; i < 40; i++) chooseVoice(voices, 'de-DE', 'agent-$i')!.id};
      expect(picked, {
        'com.apple.voice.enhanced.de-DE.Anna',
        'com.apple.voice.enhanced.de-DE.Markus',
        'com.apple.voice.enhanced.de-CH.Petra',
      });
    });

    test('a single premium voice is widened by the next tier, effects are left out', () {
      final picked = {for (var i = 0; i < 40; i++) chooseVoice(voices, 'en', 'agent-$i')!.id};
      expect(picked, {'com.apple.voice.premium.en-GB.Jamie', 'com.apple.voice.compact.en-US.Samantha'});
    });

    test('robotic voices only when there is nothing else, no voice for no language', () {
      const robots = [SystemVoice(id: 'com.apple.eloquence.fr-FR.Eddy', name: 'Eddy', language: 'fr-FR')];
      expect(chooseVoice(robots, 'fr', 'x')?.name, 'Eddy');
      expect(chooseVoice(voices, 'ja', 'x'), isNull);
    });
  });
}
