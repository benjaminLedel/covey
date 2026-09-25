import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

import '../tool/locales.dart';

// The app's catalogues are a copy of the web's (spec/27). This fails when a
// key used in lib/ is not in the web's catalogues, or when the copy under
// assets/locales no longer matches them — `dart run tool/sync_locales.dart`.
void main() {
  test('assets/locales is the current copy of the web catalogues', () {
    final expected = expectedAssets(Directory('lib'), Directory('../web/src/locales'));
    for (final e in expected.entries) {
      final file = File('assets/locales/${e.key}.json');
      expect(file.existsSync(), isTrue, reason: '${file.path} is missing');
      expect(file.readAsStringSync(), e.value, reason: '${file.path} is stale — run `dart run tool/sync_locales.dart`');
    }
  });

  test('every language carries every key the English catalogue carries', () {
    final expected = expectedAssets(Directory('lib'), Directory('../web/src/locales'));
    final keys = RegExp(r'^  "([^"]+)":', multiLine: true);
    final en = keys.allMatches(expected['en']!).map((m) => m[1]).toSet();
    for (final lang in languages) {
      expect(keys.allMatches(expected[lang]!).map((m) => m[1]).toSet(), en, reason: lang);
    }
  });
}
