import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:intl/date_symbol_data_local.dart';

import 'api.dart';
import 'chrome.dart';
import 'diagnostics.dart';
import 'face.dart';
import 'i18n.dart';
import 'pairing.dart';
import 'profile.dart';
import 'screens/connect.dart';
import 'screens/home.dart';
import 'screens/thread.dart';
import 'speech_model.dart';
import 'splash.dart';
import 'theme.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  // The diagnostic log (#352) catches what goes wrong anywhere: framework
  // errors and uncaught async ones, next to their usual handling.
  unawaited(Diagnostics.instance.init().then((_) => diag('app', 'start')));
  final flutterError = FlutterError.onError;
  FlutterError.onError = (details) {
    diag('error', details.exceptionAsString().split('\n').first);
    flutterError?.call(details);
  };
  PlatformDispatcher.instance.onError = (error, stack) {
    diag('error', '$error');
    return false;
  };
  runApp(CoveyApp(profiles: ProfileStore(), links: AppLinks().uriLinkStream));
}

/// The covey mobile app (spec/27): the chat where the person is.
///
/// Three surfaces and nothing of the console — what waits, the colleagues,
/// the thread. The app starts at the question no consumer app asks, "where
/// is your covey?", because every installation is somebody else's machine.
class CoveyApp extends StatefulWidget {
  const CoveyApp({super.key, required this.profiles, this.links});

  final ProfileStore profiles;

  /// The links the system hands the app (#333). Null in tests, which have no
  /// platform to receive them from.
  final Stream<Uri>? links;

  @override
  State<CoveyApp> createState() => _CoveyAppState();
}

class _CoveyAppState extends State<CoveyApp> {
  Strings? _strings;
  CoveyApi? _api;
  final _loaded = Completer<void>();
  // The start animation stands until it has played and the app has loaded,
  // whichever is later (#332).
  bool _splash = true;
  final _nav = GlobalKey<NavigatorState>();
  StreamSubscription<Uri>? _linkSub;
  // A link that arrived while the splash still stood — the app started BY the
  // link — waits until there is a screen to act from.
  Uri? _pendingLink;

  @override
  void initState() {
    super.initState();
    _start();
    _linkSub = widget.links?.listen(_onLink);
  }

  @override
  void dispose() {
    _linkSub?.cancel();
    super.dispose();
  }

  void _onLink(Uri uri) {
    if (_splash) {
      _pendingLink = uri;
      return;
    }
    _handleLink(uri);
  }

  void _splashDone() {
    setState(() => _splash = false);
    final link = _pendingLink;
    _pendingLink = null;
    if (link != null) WidgetsBinding.instance.addPostFrameCallback((_) => _handleLink(link));
  }

  /// What a link can do (#333): pair, or open a thread. Nothing else — a link
  /// is something anybody can send.
  Future<void> _handleLink(Uri uri) async {
    final ctx = _nav.currentContext;
    if (ctx == null) return;
    final t = Strings.of(ctx).t;
    final messenger = ScaffoldMessenger.maybeOf(ctx);

    final PairingCode? pairing;
    try {
      pairing = PairingCode.parse(uri.toString());
    } on FormatException {
      messenger?.showSnackBar(SnackBar(content: Text(t('mobile.nurHttps'))));
      return;
    }
    if (pairing != null) {
      // Confirmed before it is used, naming the host: a pairing link from
      // outside could otherwise connect the app to a stranger's instance, and
      // everything typed afterwards would go there.
      final current = _api?.base.host;
      final ok = await showDialog<bool>(
        context: ctx,
        builder: (context) => AlertDialog(
          title: Text(t('mobile.koppelnFrage', args: {'host': pairing!.instance.host})),
          content: Text(
            [
              t('mobile.koppelnFrageText'),
              if (current != null && current != pairing.instance.host)
                t('mobile.koppelnErsetzt', args: {'host': current}),
            ].join('\n\n'),
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(context, false), child: Text(t('team.abbrechen'))),
            FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(t('mobile.koppeln'))),
          ],
        ),
      );
      if (ok != true) return;
      try {
        final key = await redeemPairing(pairing);
        await _connected(pairing.instance, key);
      } on ApiException catch (e) {
        messenger?.showSnackBar(
          SnackBar(content: Text(e.status == 401 ? t('mobile.koppelnFehler') : t('mobile.nichtErreichbar'))),
        );
      }
      return;
    }

    final agentId = threadLinkAgent(uri);
    final api = _api;
    // A thread link only means something for the instance the app is
    // connected to; a link to another host is left alone.
    if (agentId == null || api == null || (uri.scheme != 'covey' && uri.host != api.base.host)) return;
    try {
      final me = await api.me();
      // Without the team surface there is no thread to open (#336).
      if (!me.teamSurface || !me.canWrite) return;
      final agent = (await api.agents()).where((a) => a.id == agentId).firstOrNull;
      if (agent == null) return;
      _nav.currentState?.push(
        MaterialPageRoute(
          builder: (_) => ThreadScreen(
            api: api,
            agentId: agent.id,
            agentName: agent.displayName,
            agentSlug: agent.slug,
            faceState: faceStateOf(killed: agent.killed, status: agent.status),
            me: me,
          ),
        ),
      );
    } on ApiException {
      return;
    }
  }

  Future<void> _start() async {
    // The device language, falling back to English (spec/27).
    final locale = WidgetsBinding.instance.platformDispatcher.locale;
    final strings = await Strings.load(locale);
    // The speech model follows the app's language where the person chose
    // none (#366).
    SpeechModel.instance.appLanguage = strings.language;
    // Dates in the notes are written in the person's language (#336).
    await initializeDateFormatting();
    // The window's bar metrics on macOS (#356), before the first frame.
    await MacChrome.load();
    // The speech model and language picked in settings (#351).
    unawaited(SpeechModel.instance.loadPrefs());
    var saved = await widget.profiles.read();
    // A developer build (debug, or profile — which starts on a phone without
    // a debugger) can be pointed at an instance from the command line —
    // `flutter run --dart-define=COVEY_INSTANCE=http://localhost:8494
    // --dart-define=COVEY_KEY=covey_…` — so working on the app against
    // `make run` does not start at the connect screen every time. Release
    // builds ignore it: a key compiled into an app is exactly what spec/27
    // forbids.
    const devInstance = String.fromEnvironment('COVEY_INSTANCE');
    const devKey = String.fromEnvironment('COVEY_KEY');
    if (!kReleaseMode && saved == null && devInstance != '' && devKey != '') {
      saved = (instance: devInstance, key: devKey);
    }
    CoveyApi? api;
    if (saved != null) {
      try {
        api = CoveyApi(parseInstance(saved.instance), saved.key);
      } on FormatException {
        await widget.profiles.clear();
      }
    }
    setState(() {
      _strings = strings;
      _api = api;
    });
    _loaded.complete();
  }

  Future<void> _connected(Uri instance, String key) async {
    await widget.profiles.write(instance.toString(), key);
    setState(() => _api = CoveyApi(instance, key));
  }

  Future<void> _disconnect() async {
    await widget.profiles.clear();
    setState(() => _api = null);
  }

  @override
  Widget build(BuildContext context) {
    final strings = _strings;
    return MaterialApp(
      title: 'covey',
      debugShowCheckedModeBanner: false,
      navigatorKey: _nav,
      theme: coveyTheme(Brightness.light),
      darkTheme: coveyTheme(Brightness.dark),
      // The splash needs no words; everything after it does.
      builder: (context, child) => strings == null ? child! : StringsScope(strings: strings, child: child!),
      home: AnimatedSwitcher(
        duration: const Duration(milliseconds: 450),
        switchInCurve: Curves.easeOutCubic,
        transitionBuilder: (child, animation) => FadeTransition(
          opacity: animation,
          child: ScaleTransition(scale: Tween(begin: 0.98, end: 1.0).animate(animation), child: child),
        ),
        child: _splash
            ? Splash(key: const ValueKey('splash'), ready: _loaded.future, onDone: _splashDone)
            : _api == null
            ? ConnectScreen(key: const ValueKey('connect'), onConnected: _connected)
            : HomeScreen(key: ValueKey(_api), api: _api!, onDisconnect: _disconnect),
      ),
    );
  }
}
