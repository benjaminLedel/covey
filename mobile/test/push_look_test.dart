import 'package:covey_mobile/models.dart';
import 'package:covey_mobile/push.dart';
import 'package:flutter_test/flutter_test.dart';

// What the Mac's look announces (#418). "The last look" is kept across
// starts, so an answer that came while the app restarted is not swallowed.

ThreadState _t(String agent, int unread, DateTime at) => ThreadState(agentId: agent, unread: unread, lastAt: at);

void main() {
  final t0 = DateTime.utc(2026, 9, 26, 20, 0);
  final t1 = t0.add(const Duration(minutes: 1));

  test('the very first look takes stock and announces nothing', () {
    expect(PushNotices.toAnnounce(null, [_t('dora', 2, t0)]), isEmpty);
  });

  test('what came after the last look is announced — also after a restart', () {
    // Kept from before the restart: Dora's newest entry was at t0.
    final seen = {'dora': t0, 'egon': t0};
    final got = PushNotices.toAnnounce(seen, [_t('dora', 1, t1), _t('egon', 1, t0)]);
    expect(got.map((t) => t.agentId), ['dora'], reason: 'Egon has nothing newer; Dora answered meanwhile');
  });

  test('read is not announced, and a conversation new since the last look is', () {
    final seen = {'dora': t0};
    expect(PushNotices.toAnnounce(seen, [_t('dora', 0, t1)]), isEmpty, reason: 'read already');
    expect(PushNotices.toAnnounce(seen, [_t('neu', 1, t1)]).single.agentId, 'neu');
  });
}
