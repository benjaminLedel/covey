import 'dart:async';

import 'package:flutter/foundation.dart';

/// How a turn shown as understood ended (#498).
enum UnderstoodOutcome {
  /// The window ran out; what was shown was sent.
  sent,

  /// The person sent it before the window ran out (Enter).
  sentNow,

  /// The person corrected it and sent the correction.
  edited,

  /// The person discarded it (Esc).
  discarded,
}

/// A recognised turn in the moment before it is sent (#498): the call
/// shows it as understood, cleans it up meanwhile, and sends it once
/// [window] has passed after the clean-up — unless the person sends it at
/// once, discards it, or starts typing, which stops the clock until they
/// send or discard the correction.
///
/// What is sent is always what was shown: sending before the clean-up came
/// back sends the text as recognised.
class UnderstoodTurn extends ChangeNotifier {
  UnderstoodTurn(this.raw, {required this.window, bool cleaning = false}) : text = raw, _cleaning = cleaning {
    if (!cleaning) _arm();
  }

  /// What the recogniser heard.
  final String raw;

  /// What is shown, and sent unless it is corrected.
  String text;

  /// How long the understood text stands before it is sent. Zero sends it
  /// as soon as it is final.
  final Duration window;

  bool _cleaning;

  /// The clean-up is still out; the window starts when it is back.
  bool get cleaning => _cleaning;

  bool _editing = false;

  /// The person is correcting the text; nothing is sent until they say so.
  bool get editing => _editing;

  /// What the clean-up made of it, when it came back with something.
  String? cleaned;

  final _done = Completer<(String?, UnderstoodOutcome)>();
  Timer? _timer;
  DateTime? _shownAt;

  /// When the window ends, while it runs.
  DateTime? get sendsAt => _timer == null || _shownAt == null ? null : _shownAt!.add(window);

  /// The text to send (null: discarded) and how it came to be.
  Future<(String?, UnderstoodOutcome)> get result => _done.future;

  bool get done => _done.isCompleted;

  /// The clean-up came back; null or empty when it failed or was skipped.
  void cleanedUp(String? t) {
    if (done || !_cleaning) return;
    _cleaning = false;
    final c = t?.trim() ?? '';
    if (c.isNotEmpty) cleaned = c;
    if (!_editing) {
      if (c.isNotEmpty) text = c;
      _arm();
    }
    notifyListeners();
  }

  void _arm() {
    _timer?.cancel();
    _shownAt = DateTime.now();
    if (window <= Duration.zero) {
      _finish(text, UnderstoodOutcome.sent);
      return;
    }
    _timer = Timer(window, () => _finish(text, UnderstoodOutcome.sent));
  }

  /// Sends now: what is shown, or [corrected] when the person corrected it.
  void send([String? corrected]) {
    if (done) return;
    final c = corrected?.trim();
    if (c != null && c.isEmpty) return discard();
    _finish(c ?? text, c != null && c != text ? UnderstoodOutcome.edited : UnderstoodOutcome.sentNow);
  }

  void discard() => _finish(null, UnderstoodOutcome.discarded);

  /// The person starts correcting: the clock stops.
  void edit() {
    if (done || _editing) return;
    _editing = true;
    _timer?.cancel();
    _timer = null;
    notifyListeners();
  }

  void _finish(String? t, UnderstoodOutcome how) {
    if (done) return;
    _timer?.cancel();
    _timer = null;
    _done.complete((t, how));
    notifyListeners();
  }

  @override
  void dispose() {
    _timer?.cancel();
    if (!done) _done.complete((null, UnderstoodOutcome.discarded));
    super.dispose();
  }
}
