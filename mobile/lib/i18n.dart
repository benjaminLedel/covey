import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

/// The ten languages of the web interface, and the same wording (spec/27):
/// an agent that "parks" in one place and "waits" in the other is two
/// products to whoever uses both.
///
/// The catalogues under assets/locales are not written by hand. They are a
/// copy of the keys this app uses, taken from web/src/locales by
/// tool/sync_locales.dart, and test/locales_test.dart fails when the copy is
/// stale or a key used in lib/ is missing from it.
const supportedLanguages = ['en', 'de', 'es', 'fr', 'it', 'nl', 'pl', 'pt', 'ja', 'zh'];

class Strings {
  Strings(this.language, this._map, this._fallback);

  final String language;
  final Map<String, String> _map;
  final Map<String, String> _fallback;

  static Future<Strings> load(Locale locale) async {
    final lang = supportedLanguages.contains(locale.languageCode) ? locale.languageCode : 'en';
    final en = await _read('en');
    return Strings(lang, lang == 'en' ? en : await _read(lang), en);
  }

  static Future<Map<String, String>> _read(String lang) async {
    final raw = await rootBundle.loadString('assets/locales/$lang.json');
    return (jsonDecode(raw) as Map<String, dynamic>).map((k, v) => MapEntry(k, v as String));
  }

  /// Looks up a key the way i18next does on the web: `{{name}}` is
  /// interpolated, and a count of one picks the `_one` variant when there is
  /// one. A missing key shows itself instead of an empty line.
  String t(String key, {Map<String, Object?> args = const {}, int? count}) {
    String? s;
    if (count == 1) s = _map['${key}_one'] ?? _fallback['${key}_one'];
    s ??= _map[key] ?? _fallback[key] ?? key;
    final all = {...args, 'count': ?count};
    return s.replaceAllMapped(RegExp(r'\{\{\s*(\w+)\s*\}\}'), (m) => '${all[m[1]] ?? ''}');
  }

  static Strings of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<StringsScope>()!.strings;
}

class StringsScope extends InheritedWidget {
  const StringsScope({super.key, required this.strings, required super.child});

  final Strings strings;

  @override
  bool updateShouldNotify(StringsScope old) => old.strings != strings;
}

extension StringsContext on BuildContext {
  String t(String key, {Map<String, Object?> args = const {}, int? count}) =>
      Strings.of(this).t(key, args: args, count: count);
}
