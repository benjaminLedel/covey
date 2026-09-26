import 'package:covey_mobile/anywhere.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('a space where the text before ends in a word (#362)', () {
    expect(seam('bis Freitag.', before: 'ich schicke es dir', after: ''), ' bis Freitag.');
  });

  test('no space after a space, a line break or an opening bracket', () {
    expect(seam('bis Freitag', before: 'ich schicke es dir ', after: ''), 'bis Freitag');
    expect(seam('Hallo', before: 'Zeile\n', after: ''), 'Hallo');
    expect(seam('siehe oben', before: 'Angebot (', after: ''), 'siehe oben');
    expect(seam('Zitat', before: 'er sagte „', after: ''), 'Zitat');
  });

  test('no space before punctuation that continues the text before', () {
    expect(seam(', und dann', before: 'erst das', after: ''), ', und dann');
  });

  test('a space after where a word follows directly', () {
    expect(seam('sehr', before: '', after: 'gut'), 'sehr ');
    expect(seam('sehr', before: '', after: '.'), 'sehr');
    expect(seam('sehr ', before: '', after: 'gut'), 'sehr ');
  });

  test('an empty field takes the text as it is', () {
    expect(seam('Hallo Ada', before: '', after: ''), 'Hallo Ada');
    expect(seam('', before: 'x', after: 'y'), '');
  });
}
