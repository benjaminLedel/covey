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

  test('a toggle keeps its summary and its content (#371)', () {
    const md =
        '<details>\n<summary>Details zum Angebot</summary>\n\nPreis: 1200 €\n- geliefert bis Freitag\n\n</details>';
    final b = parseBlocks(md);
    expect(b, hasLength(1));
    expect(b.single.kind, BlockKind.toggle);
    expect(b.single.text, 'Details zum Angebot');
    expect(b.single.body, 'Preis: 1200 €\n- geliefert bis Freitag');
    expect(writeBlocks(b), md);
    // Written by hand on one line, and without a summary.
    final one = parseBlocks('<details><summary>Kurz</summary>\nInhalt\n</details>\nDanach');
    expect([for (final x in one) x.kind], [BlockKind.toggle, BlockKind.paragraph]);
    expect(one.first.text, 'Kurz');
    expect(parseBlocks('<details>\nnur Inhalt\n</details>').single.body, 'nur Inhalt');
  });

  test('an unclosed <details> is just a line', () {
    expect(parseBlocks('<details>\nText').first.kind, BlockKind.paragraph);
  });

  test('a quote beginning with an emoji is a callout; others stay quotes (#371)', () {
    final b = parseBlocks('> 💡 Erst die Zahlen prüfen\n> ⚠️ Achtung\n> Ein Zitat\n> 🇩🇪 Flagge');
    expect([for (final x in b) x.kind], [BlockKind.callout, BlockKind.callout, BlockKind.quote, BlockKind.callout]);
    expect(b.first.emoji, '💡');
    expect(b.first.text, 'Erst die Zahlen prüfen');
    expect(b[1].emoji, '⚠️');
    expect(writeBlocks(b), '> 💡 Erst die Zahlen prüfen\n> ⚠️ Achtung\n> Ein Zitat\n> 🇩🇪 Flagge');
  });

  test('the blank line after a table is the table\'s, not an empty paragraph (#371)', () {
    const md = '| A | B |\n| --- | --- |\n| 1 | 2 |\n\nDanach';
    final b = parseBlocks(md);
    expect([for (final x in b) x.kind], [BlockKind.table, BlockKind.paragraph]);
    expect(writeBlocks(b), md);
    // Two blank lines: the second is a line of its own.
    expect(parseBlocks('| A |\n| --- |\n\n\nDanach').length, 3);
  });
}
