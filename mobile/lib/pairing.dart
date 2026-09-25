import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;

import 'api.dart';

/// A pairing, in either of the two shapes the web hands out
/// (web/src/components/MobilePairing.tsx):
///
/// - `https://<instance>/pair?code=coveypair_…` — the QR code (#333)
/// - `covey://pair?instance=<instance>&code=coveypair_…` — "Open in the app"
///
/// The first is an ordinary link, so on app.covey.work the phone's own camera
/// opens the app with it; the instance is the link's own origin. The second is
/// for the desktop app and for any host the app does not claim. Either way
/// the address goes through [parseInstance] like a typed one: a link does not
/// get to lift the HTTPS rule.
class PairingCode {
  PairingCode(this.instance, this.code);

  final Uri instance;
  final String code;

  /// Returns null for anything that is not a covey pairing code — a QR code
  /// on a poster, a URL, a Wi-Fi login. Throws [FormatException] when it is
  /// one but the address inside is not acceptable.
  static PairingCode? parse(String raw) {
    final uri = Uri.tryParse(raw.trim());
    if (uri == null) return null;
    final code = uri.queryParameters['code'] ?? '';
    if (!code.startsWith('coveypair_')) return null;
    if (uri.scheme == 'covey') {
      final instance = uri.queryParameters['instance'] ?? '';
      if (uri.host != 'pair' || instance.isEmpty) return null;
      return PairingCode(parseInstance(instance), code);
    }
    if ((uri.scheme == 'https' || uri.scheme == 'http') && uri.path.endsWith('/pair')) {
      // An instance may live under a path; everything before /pair is it.
      final base = uri.path.substring(0, uri.path.length - '/pair'.length);
      return PairingCode(parseInstance('${uri.scheme}://${uri.authority}$base'), code);
    }
    return null;
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

/// The agent a thread link points at — `https://<instance>/team/<id>`, the
/// address of the web's team surface, or `covey://team/<id>`. Null for any
/// other link.
String? threadLinkAgent(Uri uri) {
  final segments = uri.scheme == 'covey' ? [uri.host, ...uri.pathSegments] : uri.pathSegments;
  final i = segments.indexOf('team');
  if (i < 0 || i + 1 >= segments.length) return null;
  final id = segments[i + 1];
  return RegExp(r'^[0-9a-fA-F-]{36}$').hasMatch(id) ? id : null;
}
