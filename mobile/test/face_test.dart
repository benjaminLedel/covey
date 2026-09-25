import 'package:covey_mobile/face.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  // The same agent must look the same in the app as in the browser (#337).
  // These are the values of hash() in web/src/components/Gesicht.tsx, taken
  // from node with that function verbatim.
  test('the face hash is the web hash, bit for bit', () {
    const web = {
      '': 2166136261,
      'bea': 1303265843,
      'support-1': 1657807466,
      'people-department': 1353237635,
      'tester-1': 1438694960,
      'Ünïcødé-agent': 595984648,
    };
    web.forEach((slug, want) => expect(faceHash(slug), want, reason: slug));
  });

  test('the state follows the web: stopped, asleep, otherwise working', () {
    expect(faceStateOf(killed: true, status: 'sleeping'), FaceState.killed);
    expect(faceStateOf(killed: false, status: 'sleeping'), FaceState.sleeping);
    expect(faceStateOf(killed: false, status: 'triggered'), FaceState.working);
  });

  testWidgets('faces paint in every state, and stand still with reduced motion', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(
        home: Row(
          children: [
            Face(slug: 'bea'),
            Face(slug: 'bob', state: FaceState.sleeping),
            Face(slug: 'cid', state: FaceState.killed),
          ],
        ),
      ),
    );
    await tester.pump(const Duration(seconds: 2));
    expect(find.byType(Face), findsNWidgets(3));
    await tester.pumpWidget(const SizedBox());

    await tester.pumpWidget(
      const MediaQuery(
        data: MediaQueryData(disableAnimations: true),
        child: MaterialApp(home: Face(slug: 'bea')),
      ),
    );
    // No clock running: nothing scheduled, so the tree settles at once.
    await tester.pumpAndSettle();
    expect(tester.hasRunningAnimations, isFalse);
  });
}
