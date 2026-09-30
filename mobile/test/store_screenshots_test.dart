import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/chrome.dart';
import 'package:covey_mobile/dictation.dart';
import 'package:covey_mobile/host.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/screens/connect.dart';
import 'package:covey_mobile/screens/home.dart';
import 'package:covey_mobile/screens/notes.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

// The App Store screenshots (mobile/store/): the app's real screens, fed
// made-up demo data by a fake instance, drawn at the sizes App Store Connect
// asks for, in English and German. Skipped unless STORE_SHOTS=1, so a plain
// `flutter test` stays fast:
//
//   STORE_SHOTS=1 flutter test test/store_screenshots_test.dart
//
// The pictures land in store/screenshots/<device>/<locale>/, or under
// STORE_SHOTS_DIR when that is set. Nothing here comes from a real
// installation: every name, text and address is invented.

final _on = Platform.environment['STORE_SHOTS'] == '1';
final _outDir = Platform.environment['STORE_SHOTS_DIR'] ?? 'store/screenshots';

/// A screen size App Store Connect takes: the pixels, the scale the device
/// draws at, the safe areas in points, and the system it runs.
class _Device {
  const _Device(this.name, this.pixels, this.scale, this.platform, {this.top = 0, this.bottom = 0});

  final String name;
  final Size pixels;
  final double scale;
  final TargetPlatform platform;
  final double top, bottom;

  bool get mac => platform == TargetPlatform.macOS;
}

const _devices = [
  // iPhone 6.9" (iPhone 16 Pro Max class): 440 x 956 points at 3x.
  _Device('iphone-6.9', Size(1320, 2868), 3, TargetPlatform.iOS, top: 62, bottom: 34),
  // iPad 13" (iPad Pro M4 class): 1376 x 1032 points at 2x, landscape, where
  // the list and the thread stand side by side with room for both.
  _Device('ipad-13', Size(2752, 2064), 2, TargetPlatform.iOS, top: 24, bottom: 20),
  // Mac, 16:10 at 2x: a 1440 x 900 point window.
  _Device('mac', Size(2880, 1800), 2, TargetPlatform.macOS),
];

const _locales = {'en-US': Locale('en'), 'de-DE': Locale('de')};

// --- The demo organisation ------------------------------------------------

const _meId = '00000000-0000-4000-8000-00000000a0a0';
const _jonasId = '00000000-0000-4000-8000-00000000b0b0';
const _lenaId = '00000000-0000-4000-8000-00000000c0c0';

const _mira = '10000000-0000-4000-8000-000000000001';
const _theo = '10000000-0000-4000-8000-000000000002';
const _nora = '10000000-0000-4000-8000-000000000003';
const _emil = '10000000-0000-4000-8000-000000000004';
const _ida = '10000000-0000-4000-8000-000000000005';
const _paul = '10000000-0000-4000-8000-000000000006';

/// Everything the fake instance says, in one language.
class _Demo {
  _Demo(this.de);

  final bool de;
  final now = DateTime.now();

  String _t(String en, String de) => this.de ? de : en;
  String at(Duration ago) => now.subtract(ago).toUtc().toIso8601String();

  Map<String, Object?> get me => {
    'ID': _meId,
    'Email': 'ada@example.org',
    'DisplayName': 'Ada Berger',
    'Role': 'org_admin',
    'TeamSurface': true,
    'CanWrite': true,
    'CanChat': true,
    'OrgID': 'org-demo',
  };

  List<Object?> get memberships => [
    {'org_id': 'org-demo', 'org_name': _t('Northwind Studio', 'Nordwind Studio'), 'role': 'org_admin'},
  ];

  List<Object?> get departments => [
    {'id': 'd-support', 'name': 'Support', 'color': '#d95f4a'},
    {'id': 'd-dev', 'name': _t('Engineering', 'Entwicklung'), 'color': '#3f8ccb'},
    {'id': 'd-mkt', 'name': 'Marketing', 'color': '#6d8c5a'},
  ];

  Map<String, Object?> _agent(String id, String slug, String name, String job, String dept, String status) => {
    'id': id,
    'slug': slug,
    'display_name': name,
    'job_title': job,
    'status': status,
    'department_id': dept,
    'hired_at': '2026-06-01T08:00:00Z',
  };

  List<Object?> get agents => [
    _agent(_mira, 'mira', 'Mira', _t('Customer support', 'Kundensupport'), 'd-support', 'working'),
    _agent(_paul, 'paul', 'Paul', _t('Billing questions', 'Rechnungsfragen'), 'd-support', 'sleeping'),
    _agent(_theo, 'theo', 'Theo', _t('Developer', 'Entwickler'), 'd-dev', 'working'),
    _agent(_emil, 'emil', 'Emil', _t('Quality assurance', 'Qualitätssicherung'), 'd-dev', 'working'),
    _agent(_nora, 'nora', 'Nora', _t('Content and blog', 'Inhalte und Blog'), 'd-mkt', 'sleeping'),
    _agent(_ida, 'ida', 'Ida', _t('Search and findability', 'Suche und Auffindbarkeit'), 'd-mkt', 'sleeping'),
  ];

  Map<String, Object?> get inbox => {
    'pending': 1,
    'items': [
      {
        'type': 'approval',
        'id': 'i1',
        'agent_id': _mira,
        'agent_name': 'Mira',
        'agent_slug': 'mira',
        'title': _t(
          'Send the reply to the customer about the late delivery?',
          'Die Antwort an den Kunden zur verspäteten Lieferung senden?',
        ),
        'created_at': at(const Duration(minutes: 12)),
      },
    ],
  };

  Map<String, Object?> get threads => {
    'threads': [
      {
        'agent_id': _theo,
        'unread': 2,
        'last_at': at(const Duration(minutes: 3)),
        'last_text': _t(
          'The fix for the login page is up for review.',
          'Der Fix für die Anmeldeseite liegt zur Prüfung bereit.',
        ),
        'last_kind': 'answer',
      },
    ],
  };

  Map<String, Object?> _member(String kind, String id, String name, {String slug = '', String role = 'member'}) => {
    'kind': kind,
    'id': id,
    'name': name,
    if (slug.isNotEmpty) 'slug': slug,
    'role': role,
    'joined_at': '2026-09-01T08:00:00Z',
    'muted': false,
  };

  Map<String, Object?> _message(
    String conv,
    String id,
    String kind,
    String authorId,
    String name,
    String text,
    Duration ago,
  ) => {
    'id': id,
    'conversation_id': conv,
    'author_kind': kind,
    'author_id': authorId,
    'author_name': name,
    'text': text,
    'kind': 'text',
    'created_at': at(ago),
  };

  String get launchTitle => _t('Autumn release', 'Herbst-Release');

  Map<String, Object?> launch({int unread = 0}) => {
    'id': 'c-launch',
    'kind': 'group',
    'title': launchTitle,
    'created_at': at(const Duration(days: 2)),
    'last_message_at': at(const Duration(minutes: 8)),
    'members': [
      _member('human', _meId, 'Ada Berger', role: 'owner'),
      _member('human', _jonasId, 'Jonas Weber'),
      _member('agent', _nora, 'Nora', slug: 'nora'),
      _member('agent', _emil, 'Emil', slug: 'emil'),
    ],
    'unread': unread,
    'muted': false,
    'last': launchMessages.last,
  };

  List<Map<String, Object?>> get launchMessages => [
    _message(
      'c-launch',
      'm0',
      'human',
      _meId,
      'Ada Berger',
      _t('Morning. Where do we stand with the release?', 'Morgen. Wie steht es um das Release?'),
      const Duration(minutes: 44),
    ),
    _message(
      'c-launch',
      'm1',
      'human',
      _jonasId,
      'Jonas Weber',
      _t(
        'Release notes are due on Thursday. Who has the changelog?',
        'Die Release Notes sind Donnerstag fällig. Wer hat das Changelog?',
      ),
      const Duration(minutes: 40),
    ),
    _message(
      'c-launch',
      'm2',
      'human',
      _meId,
      'Ada Berger',
      _t(
        '@Nora can you draft the notes from the merged changes?',
        '@Nora kannst du die Notes aus den gemergten Änderungen entwerfen?',
      ),
      const Duration(minutes: 34),
    ),
    _message(
      'c-launch',
      'm3',
      'agent',
      _nora,
      'Nora',
      _t(
        'Yes. I read the merged changes since the last release and put a draft into the wiki. '
            'Three points still need a sentence from Jonas.',
        'Ja. Ich habe die Änderungen seit dem letzten Release gelesen und einen Entwurf ins Wiki gelegt. '
            'Drei Punkte brauchen noch einen Satz von Jonas.',
      ),
      const Duration(minutes: 30),
    ),
    _message(
      'c-launch',
      'm4',
      'agent',
      _emil,
      'Emil',
      _t(
        'The checkout test on the staging site passed this morning. I will run it again after the last merge.',
        'Der Checkout-Test auf der Staging-Seite lief heute früh durch. Nach dem letzten Merge teste ich noch einmal.',
      ),
      const Duration(minutes: 12),
    ),
    _message(
      'c-launch',
      'm5',
      'human',
      _jonasId,
      'Jonas Weber',
      _t('Thanks, I will add my three sentences today.', 'Danke, meine drei Sätze kommen heute.'),
      const Duration(minutes: 8),
    ),
  ];

  Map<String, Object?> get lena => {
    'id': 'c-lena',
    'kind': 'direct',
    'title': '',
    'created_at': at(const Duration(days: 3)),
    'last_message_at': at(const Duration(hours: 26)),
    'members': [_member('human', _meId, 'Ada Berger'), _member('human', _lenaId, 'Lena Hoffmann')],
    'unread': 0,
    'muted': false,
    'last': _message(
      'c-lena',
      'l1',
      'human',
      _lenaId,
      'Lena Hoffmann',
      _t('The workshop room is booked for Friday.', 'Der Workshop-Raum ist für Freitag gebucht.'),
      const Duration(hours: 26),
    ),
  };

  Map<String, Object?> get conversations => {
    'conversations': [launch(unread: 1), lena],
  };

  /// The direct thread with Theo, told as a colleague would.
  Map<String, Object?> get theoThread => {
    'pending': false,
    'entries': [
      {
        'kind': 'message',
        'id': 'e1',
        'author': 'chat:ada@example.org',
        'text': _t(
          'Customers write that the login page hangs after the password reset. Can you take a look?',
          'Kunden schreiben, dass die Anmeldeseite nach dem Passwort-Reset hängt. Kannst du dir das ansehen?',
        ),
        'at': at(const Duration(minutes: 52)),
      },
      {
        'kind': 'message',
        'id': 'e2',
        'author': 'agent',
        'text': _t(
          'I will look at it. I found the ticket from Mira in the support queue and linked it to a task.',
          'Mache ich. Ich habe Miras Ticket in der Support-Warteschlange gefunden und mit einer Aufgabe verknüpft.',
        ),
        'at': at(const Duration(minutes: 51)),
      },
      {
        'kind': 'question',
        'id': 'e3',
        'task_id': 't-login',
        'task_title': _t('Login hangs after password reset', 'Anmeldung hängt nach Passwort-Reset'),
        'author': 'agent',
        'text': _t(
          'The reset link keeps the old session cookie. Should I end all sessions of the user on a reset, '
              'or only the one in the browser?',
          'Der Reset-Link behält das alte Sitzungs-Cookie. Soll ich beim Reset alle Sitzungen des Nutzers beenden '
              'oder nur die im Browser?',
        ),
        'task_state': 'in_progress',
        'at': at(const Duration(minutes: 30)),
      },
      {
        'kind': 'message',
        'id': 'e4',
        'author': 'chat:ada@example.org',
        'text': _t('All sessions, that is safer.', 'Alle Sitzungen, das ist sicherer.'),
        'at': at(const Duration(minutes: 22)),
      },
      {
        'kind': 'result',
        'id': 'e5',
        'task_id': 't-login',
        'task_title': _t('Login hangs after password reset', 'Anmeldung hängt nach Passwort-Reset'),
        'author': 'agent',
        'text': _t(
          '## Result\n- A reset ends every session of the user\n- Test for the case added',
          '## Ergebnis\n- Ein Reset beendet alle Sitzungen des Nutzers\n- Test für den Fall ergänzt',
        ),
        'said': _t(
          'Done. A reset now ends all sessions, and a test covers it. '
              'The fix for the login page is up for review.',
          'Erledigt. Ein Reset beendet jetzt alle Sitzungen, ein Test deckt es ab. '
              'Der Fix für die Anmeldeseite liegt zur Prüfung bereit.',
        ),
        'task_state': 'done',
        'at': at(const Duration(minutes: 3)),
      },
    ],
  };

  Map<String, Object?> _note(
    String id,
    String kind,
    String title,
    String body, {
    String summary = '',
    int seconds = 0,
    required Duration ago,
    String status = '',
    List<String> tags = const [],
  }) => {
    'id': id,
    'kind': kind,
    'title': title,
    'body': body,
    'summary': summary,
    'duration_seconds': seconds,
    'created_at': at(ago),
    'status': status,
    'tags': tags,
  };

  Map<String, Object?> get meetingNote => _note(
    'n1',
    'meeting',
    _t('Weekly planning', 'Wochenplanung'),
    _t(
      'Ada: Let us start with the release.\nJonas: The notes are almost done, three sentences are missing.\n'
          'Ada: Then Friday for the workshop.',
      'Ada: Fangen wir mit dem Release an.\nJonas: Die Notes sind fast fertig, drei Sätze fehlen.\n'
          'Ada: Dann Freitag für den Workshop.',
    ),
    summary: _t(
      '## Decisions\n- Release notes go out on Thursday\n- Workshop on Friday\n\n## To do\n'
          '- [ ] Jonas adds three sentences to the notes\n- [x] Ada books the room',
      '## Entscheidungen\n- Release Notes gehen Donnerstag raus\n- Workshop am Freitag\n\n## Zu tun\n'
          '- [ ] Jonas ergänzt drei Sätze in den Notes\n- [x] Ada bucht den Raum',
    ),
    seconds: 1520,
    ago: const Duration(hours: 2),
    tags: [_t('team', 'team')],
  );

  Map<String, Object?> get notes => {
    'summarize': true,
    'notes': [
      meetingNote,
      _note(
        'n2',
        'voice',
        '',
        _t(
          'Ask Theo whether the reset fix also covers the mobile app.',
          'Theo fragen, ob der Reset-Fix auch die mobile App abdeckt.',
        ),
        ago: const Duration(hours: 3),
        status: 'todo',
      ),
      _note(
        'n3',
        'text',
        _t('Ideas for the blog', 'Ideen für den Blog'),
        _t(
          'How our support agent hands a ticket to a person\nWhat we learned from the first month',
          'Wie unser Support-Agent ein Ticket an einen Menschen übergibt\nWas wir aus dem ersten Monat gelernt haben',
        ),
        ago: const Duration(days: 1, hours: 2),
        status: 'doing',
      ),
      _note(
        'n4',
        'voice',
        '',
        _t(
          'Order new headsets for the support team before the end of the month.',
          'Neue Headsets für das Support-Team noch vor Monatsende bestellen.',
        ),
        ago: const Duration(days: 2, hours: 5),
        status: 'done',
      ),
      _note(
        'n5',
        'meeting',
        _t('Call with the design agency', 'Call mit der Designagentur'),
        _t('Ada: The new icons look good.', 'Ada: Die neuen Icons sehen gut aus.'),
        seconds: 1805,
        ago: const Duration(days: 4),
      ),
    ],
  };

  List<Object?> get humans => [
    {'id': _meId, 'display_name': 'Ada Berger', 'email': 'ada@example.org', 'job_title': 'Head of Operations'},
    {'id': _jonasId, 'display_name': 'Jonas Weber', 'email': 'jonas@example.org', 'job_title': 'Product'},
    {'id': _lenaId, 'display_name': 'Lena Hoffmann', 'email': 'lena@example.org', 'job_title': 'Office'},
  ];

  /// The fake instance: every read the app makes, answered from above.
  http.Client client() => MockClient((req) async {
    final p = req.url.path.replaceFirst('/api/v1', '');
    if (req.method != 'GET') {
      if (p.endsWith('/read')) return http.Response('', 204);
      if (p.startsWith('/me/notes')) {
        final sent = jsonDecode(req.body) as Map<String, dynamic>;
        return _json(
          _note('n9', sent['kind'] as String? ?? 'voice', '', sent['body'] as String? ?? '', ago: Duration.zero),
          201,
        );
      }
      return _json({}, 201);
    }
    final Object? body = switch (p) {
      '/auth/me' => me,
      '/auth/memberships' => memberships,
      '/version' => {'version': '0.12.0'},
      '/inbox' => inbox,
      '/departments' => departments,
      '/agents' => agents,
      '/me/threads' => threads,
      '/conversations' => conversations,
      '/conversations/c-launch' => launch(),
      '/conversations/c-launch/messages' => {'messages': launchMessages, 'more': false, 'pending': false},
      '/agents/$_theo/thread' => theoThread,
      '/me/notes' => notes,
      '/me/notes/n1' => meetingNote,
      '/org/chart' => {'humans': humans, 'agents': agents},
      '/me/reachable-agents' => {
        'agents': [for (final a in agents) (a! as Map)['id']],
      },
      '/speech/model' => {'enabled': true, 'name': 'parakeet-v3', 'ready': true},
      _ => null,
    };
    if (body == null) {
      if (p.endsWith('/thread')) return _json({'entries': [], 'pending': false});
      return http.Response('', 404);
    }
    return _json(body);
  });
}

http.Response _json(Object body, [int status = 200]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

/// Dictation without a microphone: it has "heard" a sentence, with a level
/// history for the waveform.
class _HeardDictation extends Dictation {
  _HeardDictation(this.heard) {
    for (var i = 0; i < Dictation.historyLength; i++) {
      final x = i / 7.0;
      levels.add((0.25 + 0.55 * (0.5 + 0.5 * math.sin(x)) * (0.6 + 0.4 * math.sin(x * 0.37 + 1))).clamp(0.05, 1.0));
    }
  }

  final String heard;
  bool _on = false;

  @override
  bool get running => _on;

  @override
  String get text => _on ? heard : '';

  @override
  Future<bool> start({bool continuous = false}) async {
    _on = true;
    notifyListeners();
    return true;
  }

  @override
  Future<String> stop() async {
    _on = false;
    notifyListeners();
    return heard;
  }
}

// --- Drawing --------------------------------------------------------------

/// The fonts the app bundles, loaded for real: a test draws text in boxes
/// otherwise.
Future<void> _loadFonts() async {
  final manifest = jsonDecode(await rootBundle.loadString('FontManifest.json')) as List;
  for (final f in manifest.cast<Map<String, dynamic>>()) {
    final loader = FontLoader(f['family'] as String);
    for (final a in (f['fonts'] as List).cast<Map<String, dynamic>>()) {
      loader.addFont(rootBundle.load(a['asset'] as String));
    }
    await loader.load();
  }
}

/// The traffic lights the Mac window draws itself, painted where they sit,
/// so the picture reads as a window.
class _TrafficLights extends StatelessWidget {
  const _TrafficLights();

  @override
  Widget build(BuildContext context) {
    // In the window's own points: the zoom does not move them.
    final top = (MacChrome.height - 12) / 2 * WindowZoom.scale.value;
    return Positioned(
      left: 20 * WindowZoom.scale.value,
      top: top,
      child: const Row(
        children: [
          _Light(Color(0xFFFF5F57)),
          SizedBox(width: 8),
          _Light(Color(0xFFFEBC2E)),
          SizedBox(width: 8),
          _Light(Color(0xFF28C840)),
        ],
      ),
    );
  }
}

class _Light extends StatelessWidget {
  const _Light(this.color);

  final Color color;

  @override
  Widget build(BuildContext context) => Container(
    width: 12,
    height: 12,
    decoration: BoxDecoration(
      color: color,
      shape: BoxShape.circle,
      border: Border.all(color: Colors.black.withValues(alpha: 0.12), width: 0.5),
    ),
  );
}

class _Stage {
  _Stage(this.tester, this.device, this.localeName);

  final WidgetTester tester;
  final _Device device;
  final String localeName;
  final _shot = GlobalKey();
  final _nav = GlobalKey<NavigatorState>();
  late final _Demo demo = _Demo(localeName == 'de-DE');
  late final CoveyApi api = CoveyApi(Uri.parse('https://demo.example.org'), 'covey_demo', client: demo.client());

  NavigatorState get nav => _nav.currentState!;

  Future<void> setUp() async {
    tester.view.physicalSize = device.pixels;
    tester.view.devicePixelRatio = device.scale;
    tester.view.padding = FakeViewPadding(top: device.top * device.scale, bottom: device.bottom * device.scale);
    tester.view.viewPadding = FakeViewPadding(top: device.top * device.scale, bottom: device.bottom * device.scale);
    Host.debugOverride = device.platform;
    debugDefaultTargetPlatformOverride = device.platform;
    WindowZoom.scale.value = device.mac ? WindowZoom.standard : 1;
  }

  void tearDown() {
    Host.debugOverride = null;
    debugDefaultTargetPlatformOverride = null;
    WindowZoom.scale.value = 1;
    tester.view.reset();
  }

  Future<void> pumpApp(Widget home) async {
    final strings = (await tester.runAsync(() => Strings.load(_locales[localeName]!)))!;
    await tester.pumpWidget(
      MaterialApp(
        debugShowCheckedModeBanner: false,
        navigatorKey: _nav,
        locale: _locales[localeName],
        theme: coveyTheme(Brightness.light),
        builder: (context, child) => RepaintBoundary(
          key: _shot,
          child: Stack(
            children: [
              Positioned.fill(
                child: WindowZoom(
                  child: StringsScope(strings: strings, child: child!),
                ),
              ),
              if (device.mac) const _TrafficLights(),
            ],
          ),
        ),
        home: home,
      ),
    );
    await settle();
  }

  Future<void> settle([int rounds = 4]) async {
    for (var i = 0; i < rounds; i++) {
      await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
      for (var j = 0; j < 6; j++) {
        await tester.pump(const Duration(milliseconds: 60));
      }
    }
  }

  Future<void> tapText(String text) async {
    await tester.tap(find.text(text).first);
    await settle();
  }

  String t(String key) => Strings.of(_nav.currentContext!).t(key);

  Future<void> save(String name) async {
    await tester.pump(const Duration(milliseconds: 600));
    final dir = Directory('$_outDir/${device.name}/$localeName')..createSync(recursive: true);
    await tester.runAsync(() async {
      final b = _shot.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final img = await b.toImage(pixelRatio: device.scale);
      final rgba = await img.toByteData(format: ui.ImageByteFormat.rawRgba);
      File('${dir.path}/$name.png').writeAsBytesSync(_opaquePng(rgba!.buffer.asUint8List(), img.width, img.height));
    });
  }
}

/// A PNG without an alpha channel: App Store Connect refuses screenshots
/// that carry one, and the engine's own encoder always writes RGBA. The
/// screens are opaque, so dropping alpha loses nothing.
Uint8List _opaquePng(Uint8List rgba, int width, int height) {
  final raw = BytesBuilder(copy: false);
  for (var y = 0; y < height; y++) {
    final row = Uint8List(1 + width * 3); // filter byte 0: none
    for (var x = 0; x < width; x++) {
      final i = (y * width + x) * 4;
      row.setRange(1 + x * 3, 4 + x * 3, rgba, i);
    }
    raw.add(row);
  }
  Uint8List chunk(String type, List<int> data) {
    final body = [...ascii.encode(type), ...data];
    return (BytesBuilder()
          ..add(_u32(data.length))
          ..add(body)
          ..add(_u32(_crc32(body))))
        .takeBytes();
  }

  final header = [..._u32(width), ..._u32(height), 8, 2, 0, 0, 0]; // 8-bit RGB
  return (BytesBuilder()
        ..add(const [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A])
        ..add(chunk('IHDR', header))
        ..add(chunk('IDAT', ZLibEncoder(level: 9).convert(raw.takeBytes())))
        ..add(chunk('IEND', const [])))
      .takeBytes();
}

List<int> _u32(int v) => [(v >> 24) & 0xFF, (v >> 16) & 0xFF, (v >> 8) & 0xFF, v & 0xFF];

final _crcTable = List<int>.generate(256, (n) {
  var c = n;
  for (var k = 0; k < 8; k++) {
    c = (c & 1) != 0 ? 0xEDB88320 ^ (c >> 1) : c >> 1;
  }
  return c;
});

int _crc32(List<int> bytes) {
  var c = 0xFFFFFFFF;
  for (final b in bytes) {
    c = _crcTable[(c ^ b) & 0xFF] ^ (c >> 8);
  }
  return c ^ 0xFFFFFFFF;
}

void main() {
  setUpAll(() async {
    await initializeDateFormatting();
    await _loadFonts();
    // Where the app keeps its files: a scratch directory, not the device's.
    final files = Directory.systemTemp.createTempSync('covey-store-shots');
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger.setMockMethodCallHandler(
      const MethodChannel('plugins.flutter.io/path_provider'),
      (_) async => files.path,
    );
  });

  for (final device in _devices) {
    for (final locale in _locales.keys) {
      testWidgets('store screenshots: ${device.name} $locale', skip: !_on, (tester) async {
        final s = _Stage(tester, device, locale);
        await s.setUp();
        try {
          await _shoot(s);
        } finally {
          await tester.pumpWidget(const SizedBox());
          s.tearDown();
        }
      });
    }
  }
}

Future<void> _shoot(_Stage s) async {
  final phone = s.device.name == 'iphone-6.9';
  await s.pumpApp(HomeScreen(api: s.api, onDisconnect: () {}));
  var n = 0;
  String name(String what) => '${(++n).toString().padLeft(2, '0')}_$what';

  if (phone) {
    // The team space: what waits, the conversations, the colleagues.
    await s.save(name('team'));
    // A colleague's thread: Theo answers as a teammate would.
    await s.tapText('Theo');
    await s.save(name('thread'));
    s.nav.pop();
    await s.settle();
    // A group with a colleague and two agents.
    await s.tapText(s.demo.launchTitle);
    await s.save(name('group'));
    s.nav.pop();
    await s.settle();
  } else {
    // On a wide window the list and the thread stand side by side.
    await s.tapText('Theo');
    await s.save(name('team_thread'));
    await s.tapText(s.demo.launchTitle);
    await s.save(name('group'));
  }

  // The notes: a meeting with its summary, voice notes, a text.
  if (phone) {
    await s.tapText(s.t('mobile.notizen'));
  } else {
    // The sidebar carries icons only.
    await s.tester.tap(find.byIcon(CupertinoIcons.square_pencil).first);
    await s.settle();
  }
  if (phone) {
    await s.save(name('notes'));
  } else {
    await s.tapText(s.demo.de ? 'Wochenplanung' : 'Weekly planning');
    await s.save(name('notes_meeting'));
  }

  // Dictation into a new note, recognised on the device. On a phone only:
  // a wide window opens the note as a page over the whole window, which
  // leaves the picture mostly empty; the open meeting above shows the
  // Dictate button there.
  if (phone) {
    final heard = s.demo.de
        ? 'Morgen früh mit Jonas die letzten drei Punkte der Release Notes durchgehen und danach an Nora geben.'
        : 'Tomorrow morning go through the last three points of the release notes with Jonas, then hand them to Nora.';
    unawaited(
      s.nav.push(
        MaterialPageRoute<void>(
          builder: (_) => NotePage(api: s.api, dictation: _HeardDictation(heard)),
        ),
      ),
    );
    await s.settle();
    await s.tester.tap(find.byTooltip(s.t('mobile.diktieren')));
    await s.settle();
    await s.save(name('dictation'));
    s.nav.pop();
    await s.settle();
  }

  await s.tester.pumpWidget(const SizedBox());

  // Pairing: a QR code on a phone or tablet, a covey:// link on the Mac.
  await s.pumpApp(ConnectScreen(desktop: s.device.mac, onConnected: (_, _) async {}));
  await s.save(name('pairing'));
}
