import 'dart:convert';

import 'package:covey_mobile/api.dart';
import 'package:covey_mobile/i18n.dart';
import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/screens/decision_cards.dart';
import 'package:covey_mobile/screens/team_space.dart';
import 'package:covey_mobile/screens/thread.dart';
import 'package:covey_mobile/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:intl/date_symbol_data_local.dart';

// The decisions the app takes since #622: what waits stands as a card with
// its buttons, the buttons follow the role and the server's flags, and the
// server's 403 is final.

Future<Widget> _app(Widget home) async {
  await initializeDateFormatting();
  final strings = await Strings.load(const Locale('de'));
  return MaterialApp(
    theme: coveyTheme(Brightness.light),
    builder: (context, child) => StringsScope(strings: strings, child: child!),
    home: home,
  );
}

http.Response _json(Object? body, [int status = 200]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 6; i++) {
    await tester.pump(const Duration(milliseconds: 60));
  }
}

const _bea = '11111111-2222-3333-4444-555555555555';

Me _me(String role) => Me(email: 'ada@example.org', displayName: 'Ada', role: role, teamSurface: true);

final _prose = List.filled(24, 'Wort').join(' ');

Map<String, Object?> _approval({String id = 'ap-1', Object? params}) => {
  'type': 'approval',
  'id': id,
  'agent_id': _bea,
  'agent_slug': 'bea',
  'agent_name': 'Bea',
  'title': 'zammad.reply',
  'status': 'pending',
  'pending': true,
  'created_at': '2026-10-20T08:00:00Z',
  'approval': {
    'id': id,
    'action': 'zammad.reply',
    'params': params ?? {'ticket': 7},
    'status': 'pending',
  },
};

Map<String, Object?> _item({String kind = 'proposal', bool needsSecurity = false, List<String> conflicts = const []}) =>
    {
      'type': kind,
      'id': 'it-1',
      'agent_id': _bea,
      'agent_slug': 'bea',
      'agent_name': 'Bea',
      'title': 'Seltener nachsehen',
      'status': 'pending',
      'pending': true,
      'created_at': '2026-10-20T08:00:00Z',
      'item': {
        'id': 'it-1',
        'kind': kind,
        'rationale': 'Die Queue ist nachts leer.',
        'needs_security': needsSecurity,
        'conflicts': conflicts,
      },
    };

/// An instance with one agent's thread and what waits about it; [decide]
/// answers the decide routes and records what they were sent.
class _Instance {
  _Instance(this.waiting, {this.decide});

  List<Map<String, Object?>> waiting;
  final http.Response Function(String path, Map<String, dynamic> body)? decide;
  final posts = <(String, Map<String, dynamic>)>[];
  List<Map<String, Object?>> thread = [];

  CoveyApi api() => CoveyApi(
    Uri.parse('https://c.example'),
    'k',
    client: MockClient((req) async {
      final p = req.url.path;
      if (req.method == 'POST') {
        final body = jsonDecode(req.body) as Map<String, dynamic>;
        posts.add((p, body));
        final r = decide?.call(p, body);
        if (r != null) return r;
        waiting = [];
        return _json({});
      }
      if (p.endsWith('/inbox')) return _json({'items': waiting, 'pending': waiting.length, 'total': waiting.length});
      if (p.endsWith('/thread')) return _json({'entries': thread, 'pending': false});
      return _json({});
    }),
  );
}

Future<void> _thread(WidgetTester tester, _Instance inst, String role) async {
  await tester.pumpWidget(
    await tester.runAsync(() => _app(ThreadScreen(api: inst.api(), agentId: _bea, agentName: 'Bea', me: _me(role))))
        as Widget,
  );
  await _settle(tester);
}

void main() {
  testWidgets('an approval stands above the input and is approved there', (tester) async {
    final inst = _Instance([_approval()]);
    await _thread(tester, inst, 'agent_owner');
    expect(find.text('Wartet auf Sie'), findsOneWidget);
    expect(find.text('zammad.reply'), findsOneWidget);
    expect(find.textContaining('"ticket": 7'), findsOneWidget, reason: 'exactly what gets approved');
    await tester.tap(find.text('Freigeben'));
    await _settle(tester);
    expect(inst.posts.single.$1, endsWith('/approvals/ap-1/decide'));
    expect(inst.posts.single.$2, {'approve': true});
    expect(find.text('zammad.reply'), findsNothing, reason: 'decided, the card goes');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a role that may not decide sees the approval without buttons', (tester) async {
    final inst = _Instance([_approval()]);
    await _thread(tester, inst, 'auditor');
    expect(find.text('zammad.reply'), findsOneWidget);
    expect(find.text('Freigeben'), findsNothing);
    expect(find.textContaining('Ihre Rolle kann das nicht entscheiden'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('controlling sees approvals, not the open points of a review', (tester) async {
    final inst = _Instance([_item()]);
    await _thread(tester, inst, 'controlling');
    expect(find.text('Seltener nachsehen'), findsNothing);
    expect(find.text('Wartet auf Sie'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets("the server's 403 is final", (tester) async {
    final inst = _Instance([_approval()], decide: (_, _) => _json({'error': 'forbidden'}, 403));
    await _thread(tester, inst, 'org_admin');
    await tester.tap(find.text('Ablehnen'));
    await _settle(tester);
    expect(inst.posts.single.$2, {'approve': false});
    expect(find.textContaining('Ihre Rolle kann das nicht entscheiden'), findsOneWidget);
    expect(find.text('Freigeben'), findsNothing, reason: 'nothing more is offered');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a single passage of prose is corrected and approved as corrected', (tester) async {
    final inst = _Instance([
      _approval(params: {'ticket': 7, 'body': _prose}),
    ]);
    await _thread(tester, inst, 'org_admin');
    await tester.ensureVisible(find.text('Text korrigieren'));
    await tester.tap(find.text('Text korrigieren'));
    await _settle(tester);
    await tester.enterText(find.byKey(const ValueKey('approval-text')), 'Kurz und freundlich.');
    await tester.pump();
    await tester.ensureVisible(find.text('Korrigiert freigeben'));
    await tester.tap(find.text('Korrigiert freigeben'));
    await _settle(tester);
    expect(inst.posts.single.$2, {'approve': true, 'text': 'Kurz und freundlich.'});
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('an open point is rejected with its note; access waits for security', (tester) async {
    final inst = _Instance([_item(needsSecurity: true)]);
    await _thread(tester, inst, 'agent_owner');
    expect(find.text('Die Queue ist nachts leer.'), findsOneWidget);
    final accept = tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Annehmen'));
    expect(accept.onPressed, isNull, reason: 'ACCESS/EGRESS is org_admin or security');
    expect(find.textContaining('Nur org_admin oder security'), findsOneWidget);
    await tester.enterText(find.widgetWithText(TextField, 'Notiz zur Entscheidung (optional)'), 'lieber nicht');
    await tester.tap(find.widgetWithText(OutlinedButton, 'Ablehnen'));
    await _settle(tester);
    expect(inst.posts.single.$1, endsWith('/improvements/it-1/decide'));
    expect(inst.posts.single.$2, {'accept': false, 'note': 'lieber nicht'});
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a configuration proposal is a card: who may not decide sees who does', (tester) async {
    final inst = _Instance([]);
    inst.thread = [
      {
        'kind': 'config_proposal',
        'id': 'm-1',
        'task_title': '',
        'task_state': '',
        'author': 'agent',
        'text': 'Vorschlag',
        'at': '2026-10-20T08:00:00Z',
        'proposal': {
          'id': 'pr-1',
          'status': 'pending',
          'title': 'Nur werktags',
          'rationale': 'Wie gewünscht.',
          'diff': [],
          'can_decide': false,
          'can_accept': false,
          'approvers': ['Grace', 'Linus'],
        },
      },
    ];
    await _thread(tester, inst, 'auditor');
    expect(find.text('Nur werktags'), findsOneWidget);
    expect(find.text('Wartet auf Freigabe durch Grace, Linus.'), findsOneWidget);
    expect(find.text('Annehmen'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a proposal whose configuration moved reads as rewrite or discard (409)', (tester) async {
    final inst = _Instance([], decide: (_, _) => _json({'error': 'the configuration has changed'}, 409));
    final card = ProposalCard(id: 'pr-1', status: 'pending', title: 'Nur werktags', canDecide: true, canAccept: true);
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              Scaffold(
                body: ProposalCardView(api: inst.api(), card: card),
              ),
            ),
          )
          as Widget,
    );
    await _settle(tester);
    await tester.tap(find.text('Annehmen'));
    await _settle(tester);
    expect(inst.posts.single.$1, endsWith('/improvements/pr-1/decide'));
    expect(inst.posts.single.$2['accept'], isTrue);
    expect(find.textContaining('neu geschrieben oder verworfen'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('a workflow decision is taken in the team space, one button per option', (tester) async {
    final posts = <(String, Map<String, dynamic>)>[];
    final api = CoveyApi(
      Uri.parse('https://c.example'),
      'k',
      client: MockClient((req) async {
        final p = req.url.path;
        if (req.method == 'POST') {
          posts.add((p, jsonDecode(req.body) as Map<String, dynamic>));
          return _json({});
        }
        if (p.endsWith('/workflows/mine')) {
          return _json([
            {
              'workflow': 'refunds',
              'workflow_id': 'w-1',
              'name': 'Erstattungen',
              'run': 'run-1',
              'case': 'T-42',
              'step': 'Freigabe',
              'question': 'Erstatten?',
              'options': ['ja', 'nein'],
              'since': '2026-10-20T08:00:00Z',
            },
          ]);
        }
        if (p.endsWith('/inbox')) return _json({'items': [], 'pending': 0});
        if (p.endsWith('/agents') || p.endsWith('/departments')) return _json([]);
        if (p.endsWith('/me/threads')) return _json({'threads': []});
        if (p.endsWith('/conversations')) return _json({'conversations': []});
        return _json({});
      }),
    );
    await tester.pumpWidget(
      await tester.runAsync(
            () => _app(
              Scaffold(
                body: TeamSpace(api: api, me: _me('auditor'), onOpen: (_, _, _, _) {}),
              ),
            ),
          )
          as Widget,
    );
    await _settle(tester);
    expect(find.text('Wartet auf Sie'), findsOneWidget);
    expect(find.text('Erstatten?'), findsOneWidget);
    await tester.tap(find.text('nein'));
    await _settle(tester);
    expect(posts.single.$1, endsWith('/workflows/refunds/runs/run-1/decide'));
    expect(posts.single.$2, {'option': 'nein'});
    expect(find.text('Entschieden: nein.'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  test('the prose a reviewer may correct is exactly one long string', () {
    expect(loneProse({'a': _prose}), _prose);
    expect(loneProse({'a': _prose, 'b': _prose}), '');
    expect(loneProse({'a': 'kurz'}), '');
    expect(
      loneProse([
        {'x': _prose},
      ]),
      _prose,
    );
  });
}
