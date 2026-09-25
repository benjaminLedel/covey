import 'package:covey_mobile/rich/blocks.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('plain text comes back exactly as it went in (#344)', () {
    // A transcript, a note from before the editor: lines, empty lines, a
    // line that merely starts with a number or a dash inside a sentence.
    const texts = [
      'Milch kaufen',
      'Ada: Globex will bis Freitag eine Antwort.\nOtto: Ich schaue mir den Merge an.\n\nVera: Die Hilfeseiten sind indexiert.',
      '',
      'Der Rabatt liegt 4 % über dem Rahmen – nicht 12.',
    ];
    for (final t in texts) {
      expect(writeBlocks(parseBlocks(t)), t);
    }
  });

  test('the structures the editor knows survive a round trip', () {
    const md =
        '# Weekly\n'
        '## Aufgaben\n'
        '- [ ] Ada schickt das Angebot\n'
        '- [x] Otto prüft den Merge\n'
        '- Punkt\n'
        '1. eins\n'
        '2. zwei\n'
        '> Zitat mit **fett** und _kursiv_\n'
        '---\n'
        '![](covey-media://11111111-2222-3333-4444-555555555555)\n'
        '| Name | Status |\n'
        '| --- | --- |\n'
        '| Ada | a \\| b |';
    final blocks = parseBlocks(md);
    expect(blocks.map((b) => b.kind).toList(), [
      BlockKind.heading1,
      BlockKind.heading2,
      BlockKind.todo,
      BlockKind.todo,
      BlockKind.bullet,
      BlockKind.numbered,
      BlockKind.numbered,
      BlockKind.quote,
      BlockKind.divider,
      BlockKind.image,
      BlockKind.table,
    ]);
    expect(blocks[3].checked, isTrue);
    expect(blocks[9].ref, 'covey-media://11111111-2222-3333-4444-555555555555');
    expect(blocks[10].rows, [
      ['Name', 'Status'],
      ['Ada', 'a | b'],
    ]);
    expect(writeBlocks(blocks), md);
  });

  test('numbered items are counted from their run', () {
    expect(writeBlocks(parseBlocks('3. a\n7. b\nx\n9. c')), '1. a\n2. b\nx\n1. c');
  });
}
