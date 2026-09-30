import 'dart:async';

import '../api.dart';

/// Which recogniser a turn's text came from (#516).
enum Recogniser {
  /// The on-device model (Parakeet or SenseVoice).
  device,

  /// The organisation's voice provider, through the instance.
  server,
}

/// A turn's text, where it came from, and how long it took.
class TurnText {
  const TurnText(this.text, this.by, {required this.elapsed, this.serverProblem});

  final String text;
  final Recogniser by;

  /// From the start of recognition to this text.
  final Duration elapsed;

  /// Why the server's text was not taken, when it was asked: `timeout`,
  /// `empty`, or the error. Null when it was taken or not asked.
  final String? serverProblem;
}

/// The bound on the server's recognition: after it the device's text is
/// taken. The device's recogniser runs alongside, so the fallback is ready
/// by then.
const serverRecognitionBound = Duration(seconds: 4);

/// Recognises one turn (#516): with [server], it and [device] start at once;
/// the server's text is taken when it arrives within [bound] and is not
/// empty, otherwise the device's. Without [server] only the device is asked.
/// Throws only what the device throws when it is the one used.
Future<TurnText> recogniseTurn({
  required Future<String> Function() device,
  Future<String> Function()? server,
  Duration bound = serverRecognitionBound,
}) async {
  final watch = Stopwatch()..start();
  if (server == null) {
    final text = (await device()).trim();
    return TurnText(text, Recogniser.device, elapsed: watch.elapsed);
  }
  // Asked first, so its request is on its way while the device decodes.
  final fromServer = Future.sync(server);
  final fromDevice = Future.sync(device);
  // Whichever is not used must not end as an unhandled error.
  unawaited(fromDevice.then((_) {}, onError: (_) {}));
  unawaited(fromServer.then((_) {}, onError: (_) {}));
  String problem;
  final overdue = Completer<String?>();
  final timer = Timer(bound, () => overdue.complete(null));
  try {
    final got = await Future.any<String?>([fromServer, overdue.future]);
    if (got == null) {
      problem = 'timeout';
    } else if (got.trim().isEmpty) {
      problem = 'empty';
    } else {
      return TurnText(got.trim(), Recogniser.server, elapsed: watch.elapsed);
    }
  } on ApiException catch (e) {
    problem = e.status == 0 ? e.message : 'HTTP ${e.status}: ${e.message}';
  } catch (e) {
    problem = '$e';
  } finally {
    timer.cancel();
  }
  final text = (await fromDevice).trim();
  return TurnText(text, Recogniser.device, elapsed: watch.elapsed, serverProblem: problem);
}
