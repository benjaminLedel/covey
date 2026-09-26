import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// One connection: an instance, the key paired from one seat there, and the
/// organisation that seat is in (#417).
class Profile {
  const Profile({required this.instance, required this.key, this.orgId = '', this.label = ''});

  factory Profile.fromJson(Map<String, dynamic> j) => Profile(
    instance: j['instance'] as String? ?? '',
    key: j['key'] as String? ?? '',
    orgId: j['org_id'] as String? ?? '',
    label: j['label'] as String? ?? '',
  );

  final String instance;
  final String key;

  /// The organisation of the seat the key was paired from; empty for a
  /// connection saved before #417, until the app has asked.
  final String orgId;

  /// The organisation's name, as the list shows it.
  final String label;

  /// Two connections are the same when they reach the same organisation on
  /// the same instance; a new pairing there replaces the key.
  bool sameAs(Profile o) => instance == o.instance && orgId == o.orgId;

  Map<String, String> toJson() => {'instance': instance, 'key': key, 'org_id': orgId, 'label': label};
}

/// Where the app keeps the instances it is connected to.
///
/// Keychain on iOS, Keystore on Android — and the keys only there, never in
/// preferences or a file. An instance URL is stored beside its key because
/// the two are one account (spec/27).
///
/// Several connections side by side (#417): an API key belongs to one seat in
/// one organisation, on purpose — it cannot switch itself (switch-org is for
/// browser sessions only). A person in several organisations pairs each, and
/// the app switches between the connections. read/write/clear act on the
/// active one.
///
/// On the Mac the login keychain, not the data protection keychain the
/// plugin prefers (#407): that one is open only to an app signed with a team
/// and a keychain-access-groups entitlement, and this app runs outside the
/// sandbox without one — every write failed with errSecMissingEntitlement,
/// and the connection was never saved.
class ProfileStore {
  ProfileStore([FlutterSecureStorage? storage])
    : _s = storage ?? const FlutterSecureStorage(mOptions: MacOsOptions(usesDataProtectionKeychain: false));

  final FlutterSecureStorage _s;

  static const _profiles = 'profiles';
  static const _active = 'active_profile';
  // The single connection of the versions before #417.
  static const _instance = 'instance';
  static const _key = 'api_key';

  /// The keychain, one entry at a time; a test replaces these three.
  Future<String?> lesen(String key) => _s.read(key: key);
  Future<void> schreiben(String key, String value) => _s.write(key: key, value: value);
  Future<void> loeschen(String key) => _s.delete(key: key);

  /// Every saved connection, the one of an older version taken over.
  Future<List<Profile>> all() async {
    final raw = await lesen(_profiles);
    if (raw != null) {
      try {
        return [
          for (final p in jsonDecode(raw) as List) Profile.fromJson((p as Map).cast<String, dynamic>()),
        ].where((p) => p.instance.isNotEmpty && p.key.isNotEmpty).toList();
      } on FormatException {
        return [];
      }
    }
    final instance = await lesen(_instance);
    final key = await lesen(_key);
    if (instance == null || key == null) return [];
    return [Profile(instance: instance, key: key)];
  }

  /// The active connection, or null.
  Future<Profile?> active() async {
    final list = await all();
    if (list.isEmpty) return null;
    final i = int.tryParse(await lesen(_active) ?? '') ?? 0;
    return list[i.clamp(0, list.length - 1)];
  }

  Future<({String instance, String key})?> read() async {
    final p = await active();
    return p == null ? null : (instance: p.instance, key: p.key);
  }

  /// Saves a connection and makes it the active one. One to the same
  /// organisation on the same instance is replaced, and so is one saved
  /// before the organisation was known.
  Future<void> write(String instance, String key, {String orgId = '', String label = ''}) async {
    final neu = Profile(instance: instance, key: key, orgId: orgId, label: label);
    final list = await all();
    var i = list.indexWhere((p) => p.sameAs(neu) || (p.instance == instance && p.orgId.isEmpty));
    if (i < 0) {
      list.add(neu);
      i = list.length - 1;
    } else {
      list[i] = neu;
    }
    await _save(list, i);
  }

  /// Makes a saved connection the active one.
  Future<void> activate(Profile p) async {
    final list = await all();
    final i = list.indexWhere((x) => x.sameAs(p) && x.key == p.key);
    if (i >= 0) await schreiben(_active, '$i');
  }

  /// Removes the active connection; the first of the others becomes active.
  Future<void> clear() async {
    final list = await all();
    if (list.isNotEmpty) {
      final i = (int.tryParse(await lesen(_active) ?? '') ?? 0).clamp(0, list.length - 1);
      list.removeAt(i);
    }
    await _save(list, 0);
  }

  Future<void> _save(List<Profile> list, int active) async {
    if (list.isEmpty) {
      await loeschen(_profiles);
      await loeschen(_active);
    } else {
      await schreiben(_profiles, jsonEncode([for (final p in list) p.toJson()]));
      await schreiben(_active, '$active');
    }
    // The single entries of before #417 are taken over once written anew.
    await loeschen(_instance);
    await loeschen(_key);
  }
}

/// A ProfileStore in memory, for tests.
class MemoryProfileStore extends ProfileStore {
  MemoryProfileStore([Map<String, String>? values]) : values = values ?? {};

  final Map<String, String> values;

  @override
  Future<String?> lesen(String key) async => values[key];
  @override
  Future<void> schreiben(String key, String value) async => values[key] = value;
  @override
  Future<void> loeschen(String key) async => values.remove(key);
}
