// The app's wording is the web's wording (spec/27). This file decides which
// keys the app carries and what they say; tool/sync_locales.dart writes the
// result into assets/locales, and test/locales_test.dart checks that what is
// there is what this would write.
import 'dart:convert';
import 'dart:io';

const languages = ['en', 'de', 'es', 'fr', 'it', 'nl', 'pl', 'pt', 'ja', 'zh'];

/// Keys the code builds at runtime (`status.${state}`) and a scan for
/// literals therefore cannot see. Every key under these prefixes is carried.
const dynamicPrefixes = ['status.', 'inbox.type.', 'mobile.art_', 'mobile.modell_', 'mobile.block_', 'mobile.nstatus_'];

/// Every key the app uses: the literals in `t('…')` under lib/, any other
/// string literal that names a key of the catalogue — a key picked in a
/// `switch` and handed to `t` afterwards (#382) —, the keys under
/// [dynamicPrefixes], and the `_one` plural of any of them.
Set<String> usedKeys(Directory lib, Map<String, String> english) {
  final literal = RegExp(r"""\bt\(\s*'([A-Za-z0-9_.]+)'""");
  final named = RegExp(r"""'([a-z][A-Za-z0-9_]*\.[A-Za-z0-9_.]+)'""");
  final keys = <String>{};
  for (final f in lib.listSync(recursive: true).whereType<File>().where((f) => f.path.endsWith('.dart'))) {
    final code = f.readAsStringSync();
    for (final m in literal.allMatches(code)) {
      keys.add(m[1]!);
    }
    for (final m in named.allMatches(code)) {
      if (english.containsKey(m[1])) keys.add(m[1]!);
    }
  }
  keys.addAll(english.keys.where((k) => dynamicPrefixes.any(k.startsWith)));
  keys.addAll([for (final k in keys) '${k}_one'].where(english.containsKey));
  return keys;
}

/// A web catalogue, flattened to dotted keys the way i18next reads it.
Map<String, String> flatten(Map<String, dynamic> m, [String prefix = '']) {
  final out = <String, String>{};
  m.forEach((k, v) {
    if (v is Map<String, dynamic>) {
      out.addAll(flatten(v, '$prefix$k.'));
    } else if (v is String) {
      out['$prefix$k'] = v;
    }
  });
  return out;
}

Map<String, String> webCatalogue(Directory web, String lang) =>
    flatten(jsonDecode(File('${web.path}/$lang.json').readAsStringSync()) as Map<String, dynamic>);

/// What `assets/locales/<lang>.json` should contain, as the exact text.
Map<String, String> expectedAssets(Directory lib, Directory web) {
  final en = webCatalogue(web, 'en');
  final keys = usedKeys(lib, en).toList()..sort();
  final missing = keys.where((k) => !en.containsKey(k)).toList();
  if (missing.isNotEmpty) {
    throw StateError('keys used in lib/ but missing from web/src/locales/en.json: ${missing.join(', ')}');
  }
  const enc = JsonEncoder.withIndent('  ');
  return {
    for (final lang in languages)
      lang: () {
        final cat = webCatalogue(web, lang);
        return '${enc.convert({for (final k in keys) k: ?cat[k]})}\n';
      }(),
  };
}
