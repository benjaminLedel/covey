import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/pairing.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

void main() {
  group('PairingCode.parse', () {
    test('reads what the web puts into the QR code', () {
      // The same string as pairingPayload() in web/src/components/MobilePairing.tsx.
      final p = PairingCode.parse('covey://pair?instance=https%3A%2F%2Fapp.covey.work&code=coveypair_abc-_');
      expect(p!.instance.toString(), 'https://app.covey.work');
      expect(p.code, 'coveypair_abc-_');
    });

    test('reads the https link of the QR code (#333), also under a path', () {
      final p = PairingCode.parse('https://app.covey.work/pair?code=coveypair_abc');
      expect(p!.instance.toString(), 'https://app.covey.work');
      expect(p.code, 'coveypair_abc');
      expect(PairingCode.parse('https://x.org:8443/covey/pair?code=coveypair_a')!.instance.toString(), 'https://x.org:8443/covey');
      expect(PairingCode.parse('https://app.covey.work/team/x?code=coveypair_abc'), isNull);
    });

    test('ignores QR codes that are not covey pairing codes', () {
      expect(PairingCode.parse('https://example.org'), isNull);
      expect(PairingCode.parse('WIFI:S:office;T:WPA;P:secret;;'), isNull);
      expect(PairingCode.parse('covey://pair?instance=https%3A%2F%2Fx.org&code=covey_akey'), isNull);
    });

    test('a QR code does not lift the HTTPS rule', () {
      expect(() => PairingCode.parse('covey://pair?instance=http%3A%2F%2Fevil.example&code=coveypair_x'), throwsFormatException);
    });
  });

  test('redeeming posts the code and the device, without a badge, and returns the key', () async {
    late http.Request seen;
    final token = await redeemPairing(
      PairingCode(Uri.parse('https://app.covey.work'), 'coveypair_x'),
      device: 'Adas iPhone (iOS)',
      client: MockClient((req) async {
        seen = req;
        // UTF-8 as the instance sends it — the key name carries a '·'.
        return http.Response.bytes(utf8.encode(jsonEncode({'token': 'covey_new', 'name': 'Mobile app · Adas iPhone (iOS)'})), 201);
      }),
    );
    expect(token, 'covey_new');
    expect(seen.url.toString(), 'https://app.covey.work/api/v1/auth/pair');
    expect(seen.headers.containsKey('Authorization'), isFalse);
    expect(jsonDecode(seen.body), {'code': 'coveypair_x', 'device': 'Adas iPhone (iOS)'});
  });

  test('a refused code is an ApiException with its status', () async {
    await expectLater(
      redeemPairing(PairingCode(Uri.parse('https://c.example'), 'coveypair_x'),
          device: 'd', client: MockClient((_) async => http.Response('{"error":"pairing code invalid, used or expired"}', 401))),
      throwsA(isA<ApiException>().having((e) => e.status, 'status', 401)),
    );
  });

  test('a thread link names its agent, in both shapes', () {
    const id = '11111111-2222-3333-4444-555555555555';
    expect(threadLinkAgent(Uri.parse('https://app.covey.work/team/$id')), id);
    expect(threadLinkAgent(Uri.parse('covey://team/$id')), id);
    expect(threadLinkAgent(Uri.parse('https://app.covey.work/team/not-an-id')), isNull);
    expect(threadLinkAgent(Uri.parse('https://app.covey.work/agents/$id')), isNull);
  });
}
