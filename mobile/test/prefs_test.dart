import 'dart:convert';
import 'dart:io';

import 'package:covey_mobile/call/call.dart';
import 'package:covey_mobile/prefs.dart';
import 'package:flutter_test/flutter_test.dart';

// The app's settings file (#357), and the call's diagnostics switch that
// was missing from it after it had been turned on (#511).

void main() {
  late Directory dir;

  setUp(() async {
    dir = await Directory.systemTemp.createTemp('prefs');
    Prefs.instance.atDirectory(dir);
  });

  tearDown(() async {
    Prefs.instance.inMemory({'tour.team': '1'});
    await dir.delete(recursive: true);
  });

  Map<String, dynamic> file() => jsonDecode(File('${dir.path}/prefs.json').readAsStringSync()) as Map<String, dynamic>;

  test('writes at the same time all reach the file', () async {
    await Future.wait([
      for (var i = 0; i < 20; i++) Prefs.instance.write('k$i', '$i'),
      Prefs.instance.write('call.record', 'on'),
    ]);
    final f = file();
    expect(f['call.record'], 'on');
    for (var i = 0; i < 20; i++) {
      expect(f['k$i'], '$i');
    }
  });

  test('a key another copy of the app wrote is kept', () async {
    await Prefs.instance.write('call.pause', '1000');
    File('${dir.path}/prefs.json').writeAsStringSync(jsonEncode({...file(), 'diagnostics.enabled': 'on'}));
    await Prefs.instance.write('call.record', 'on');
    expect(file(), {'call.pause': '1000', 'diagnostics.enabled': 'on', 'call.record': 'on'});
    await Prefs.instance.write('call.pause', null);
    expect(file().containsKey('call.pause'), isFalse);
  });

  test('the diagnostics recording, switched on, is on at the next start', () async {
    await CallSettings.setRecord(true);
    expect(file()['call.record'], 'on');
    CallSettings.record.value = false;
    Prefs.instance.atDirectory(dir); // a new start: read from the file
    await CallSettings.load();
    expect(CallSettings.record.value, isTrue);
    expect(CallSettings.tuning.record, isTrue, reason: 'a call started now records its turns');
    CallSettings.record.value = false;
  });
}
