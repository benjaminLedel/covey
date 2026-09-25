import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Where the app keeps the one instance it is connected to.
///
/// Keychain on iOS, Keystore on Android — and the key only there, never in
/// preferences or a file. The instance URL is stored beside it because the
/// two are one account (spec/27); several profiles side by side are a later
/// step and change nothing about where they are kept.
class ProfileStore {
  ProfileStore([FlutterSecureStorage? storage]) : _s = storage ?? const FlutterSecureStorage();

  final FlutterSecureStorage _s;

  static const _instance = 'instance';
  static const _key = 'api_key';

  Future<({String instance, String key})?> read() async {
    final instance = await _s.read(key: _instance);
    final key = await _s.read(key: _key);
    if (instance == null || key == null) return null;
    return (instance: instance, key: key);
  }

  Future<void> write(String instance, String key) async {
    await _s.write(key: _instance, value: instance);
    await _s.write(key: _key, value: key);
  }

  Future<void> clear() async {
    await _s.delete(key: _instance);
    await _s.delete(key: _key);
  }
}
