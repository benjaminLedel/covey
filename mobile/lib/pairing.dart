import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;

import 'api.dart';

/// What the web's QR code carries (#330, web/src/components/MobilePairing.tsx):
///
///   covey://pair?instance=<address>&code=coveypair_…
///
/// The address is the page's own origin, so it is whatever name the person
/// reaches the instance by. It goes through [parseInstance] like a typed one:
/// a QR code does not get to lift the HTTPS rule.
class PairingCode {
  PairingCode(this.instance, this.code);

  final Uri instance;
  final String code;

  /// Returns null for anything that is not a covey pairing code — a QR code
  /// on a poster, a URL, a Wi-Fi login. Throws [FormatException] when it is
  /// one but the address inside is not acceptable.
  static PairingCode? parse(String raw) {
    final uri = Uri.tryParse(raw.trim());
    if (uri == null || uri.scheme != 'covey' || uri.host != 'pair') return null;
    final code = uri.queryParameters['code'] ?? '';
    final instance = uri.queryParameters['instance'] ?? '';
    if (!code.startsWith('coveypair_') || instance.isEmpty) return null;
    return PairingCode(parseInstance(instance), code);
  }
}

/// The name the key will carry in the web's list — `Mobile app · <this>`.
/// What the phone calls itself, and the platform, so two phones of one
/// person can be told apart when one of them is lost.
String deviceName() {
  final os = Platform.isIOS ? 'iOS' : (Platform.isAndroid ? 'Android' : Platform.operatingSystem);
  final host = Platform.localHostname;
  return host.isEmpty || host == 'localhost' ? os : '$host ($os)';
}

/// Exchanges the code for an API key. The one request the app makes without
/// a badge — the code is the badge, and it works once.
Future<String> redeemPairing(PairingCode p, {http.Client? client, String? device}) async {
  final c = client ?? http.Client();
  final http.Response res;
  try {
    res = await c
        .post(
          p.instance.replace(path: '${p.instance.path}/api/v1/auth/pair'),
          headers: {'Content-Type': 'application/json', 'Accept': 'application/json'},
          body: jsonEncode({'code': p.code, 'device': device ?? deviceName()}),
        )
        .timeout(const Duration(seconds: 20));
  } catch (e) {
    throw ApiException(0, e.toString());
  }
  if (res.statusCode != 201) {
    throw ApiException(res.statusCode, 'HTTP ${res.statusCode}');
  }
  final body = jsonDecode(utf8.decode(res.bodyBytes)) as Map<String, dynamic>;
  final token = body['token'] as String? ?? '';
  if (!token.startsWith('covey_')) throw ApiException(res.statusCode, 'no key in the answer');
  return token;
}
