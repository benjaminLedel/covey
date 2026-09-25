// Copies the keys the app uses out of the web's catalogues:
//
//   cd mobile && dart run tool/sync_locales.dart
//
// Run it after using a new key in lib/ or after the wording changed on the
// web; test/locales_test.dart fails until it has run.
import 'dart:io';

import 'locales.dart';

void main() {
  final assets = expectedAssets(Directory('lib'), Directory('../web/src/locales'));
  for (final e in assets.entries) {
    File('assets/locales/${e.key}.json').writeAsStringSync(e.value);
  }
  stdout.writeln('wrote ${assets.length} catalogues to assets/locales');
}
