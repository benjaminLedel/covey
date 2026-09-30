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
import 'prefs.dart';
import 'profile.dart';
import 'push.dart';
import 'screens/connect.dart';
import 'screens/home.dart';
import 'screens/thread.dart';
import 'speech_model.dart';
import 'splash.dart';
import 'theme.dart';
import 'updater.dart';

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

/// Set when the person disconnected, cleared when they connect (#405).
const _disconnected = 'profile.disconnected';

class _CoveyAppState extends State<CoveyApp> {
  Strings? _strings;
  CoveyApi? _api;
  final _loaded = Completer<void>();
  // The start animation stands until it has played and the app has loaded,
  // whichever is later (#332).
  bool _splash = true;
  final _nav = GlobalKey<NavigatorState>();
  StreamSubscription<Uri>? _linkSub;
  StreamSubscription<String>? _pushSub;
  // A link that arrived while the splash still stood — the app started BY the
  // link — waits until there is a screen to act from.
  Uri? _pendingLink;

  @override
  void initState() {
    super.initState();
    _start();
    _linkSub = widget.links?.listen(_onLink);
    // A tapped notification opens its thread the way a link to it does (#379).
    _pushSub = PushNotices.instance.opens.listen((target) => _onLink(PushNotices.linkFor(target)));
  }

  @override
  void dispose() {
    _linkSub?.cancel();
    _pushSub?.cancel();
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

  /// What a link can do (#333): pair, or open a thread or a conversation
  /// (#440). Nothing else — a link is something anybody can send.
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

    final conversationId = conversationLink(uri);
    var agentId = threadLinkAgent(uri);
    final api = _api;
    // A thread link only means something for the instance the app is
    // connected to; a link to another host is left alone.
    if ((agentId == null && conversationId == null) ||
        api == null ||
        (uri.scheme != 'covey' && uri.host != api.base.host)) {
      return;
    }
    try {
      final me = await api.me();
      // Without the team surface there is no thread to open (#336).
      if (!me.teamSurface || !me.canChat) return;
      if (conversationId != null) {
        // Only a member reads it; anybody else gets a 404 and nothing opens.
        final c = await api.conversation(conversationId);
        agentId = c.directAgent?.id;
        if (agentId == null) {
          _nav.currentState?.push(
            MaterialPageRoute(
              builder: (_) => ThreadScreen.conversation(api: api, conversation: c, me: me),
            ),
          );
          return;
        }
      }
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
    await WindowZoom.load();
    // The speech model and language picked in settings (#351).
    unawaited(SpeechModel.instance.loadPrefs());
    // A new version of the Mac app, from the releases (#421).
    unawaited(Updater.start());
    // Somebody who disconnected stays disconnected (#405): a key the
    // keychain would not let go of is not a way back in, and neither is an
    // instance given on the command line.
    final disconnected = await Prefs.instance.read(_disconnected) == '1';
    if (disconnected) await _forget();
    var saved = disconnected ? null : await _read();
    // A developer build (debug, or profile — which starts on a phone without
    // a debugger) can be pointed at an instance from the command line —
    // `flutter run --dart-define=COVEY_INSTANCE=http://localhost:8494
    // --dart-define=COVEY_KEY=covey_…` — so working on the app against
    // `make run` does not start at the connect screen every time. Release
    // builds ignore it: a key compiled into an app is exactly what spec/27
    // forbids.
    const devInstance = String.fromEnvironment('COVEY_INSTANCE');
    const devKey = String.fromEnvironment('COVEY_KEY');
    if (!kReleaseMode && !disconnected && saved == null && devInstance != '' && devKey != '') {
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
    await _reloadProfiles();
    setState(() {
      _strings = strings;
      _api = api;
    });
    _loaded.complete();
  }

  /// The connections saved on this device (#417), for the settings' list.
  List<Profile> _profiles = const [];
  Profile? _active;

  Future<({String instance, String key})?> _read() async {
    try {
      return await widget.profiles.read();
    } catch (e) {
      diag('profile', 'the saved connections could not be read: $e');
      return null;
    }
  }

  Future<void> _reloadProfiles() async {
    try {
      final all = await widget.profiles.all();
      final active = await widget.profiles.active();
      if (mounted) {
        setState(() {
          _profiles = all;
          _active = active;
        });
      }
    } catch (e) {
      diag('profile', 'the saved connections could not be listed: $e');
    }
  }

  /// Switches to another saved connection (#417): a key belongs to one
  /// organisation, so switching is choosing another key, not moving this one.
  Future<void> _switch(Profile p) async {
    final CoveyApi api;
    try {
      api = CoveyApi(parseInstance(p.instance), p.key);
    } on FormatException {
      return;
    }
    _nav.currentState?.popUntil((r) => r.isFirst);
    setState(() => _api = api);
    try {
      await widget.profiles.activate(p);
    } catch (e) {
      diag('profile', 'the active connection could not be saved: $e');
    }
    await _reloadProfiles();
  }

  /// Pairs another organisation (#417): the pairing screen over the app,
  /// and what it connects is added beside the connections there are.
  void _addOrganisation() {
    _nav.currentState?.push(
      MaterialPageRoute<void>(
        builder: (context) => ConnectScreen(
          onConnected: (instance, key) async {
            _nav.currentState?.popUntil((r) => r.isFirst);
            await _connected(instance, key);
          },
        ),
      ),
    );
  }

  /// Connects, and keeps the connection for the next start. A keychain that
  /// refuses the save does not stop the connection (#407): the app is then
  /// connected for this session, and the log says why it will ask again.
  ///
  /// Saved beside the other connections, under the organisation the key's
  /// seat is in (#417) — a second organisation is added, a new pairing to the
  /// same one replaces its key.
  Future<void> _connected(Uri instance, String key) async {
    final api = CoveyApi(instance, key);
    setState(() => _api = api);
    await Prefs.instance.write(_disconnected, null);
    var orgId = '', label = '';
    try {
      orgId = (await api.me()).orgId;
      label = (await api.memberships()).where((m) => m.orgId == orgId).firstOrNull?.orgName ?? '';
    } catch (e) {
      diag('profile', 'the organisation of the new connection is not known: $e');
    }
    try {
      await widget.profiles.write(instance.toString(), key, orgId: orgId, label: label);
    } catch (e) {
      diag('profile', 'the connection could not be saved: $e');
    }
    await _reloadProfiles();
  }

  /// The screen changes first (#405): the person asked to leave, whatever
  /// the keychain says. On the Mac a development build can be refused the
  /// delete of an item an earlier, differently signed build wrote — the
  /// button then did nothing at all.
  ///
  /// Only the active connection goes (#417); with others saved, the app
  /// switches to the next, and the connect screen comes when none is left.
  Future<void> _disconnect() async {
    final others = _profiles.length > 1;
    if (!others) setState(() => _api = null);
    try {
      await widget.profiles.clear();
    } catch (e) {
      diag('profile', 'the saved connection could not be deleted: $e');
    }
    await _reloadProfiles();
    final next = _active;
    if (others && next != null) {
      await _switch(next);
      return;
    }
    setState(() => _api = null);
    await Prefs.instance.write(_disconnected, '1');
  }

  /// Deletes what is left after the last disconnect; a refusal is logged,
  /// and the next start tries again.
  Future<void> _forget() async {
    try {
      for (var i = 0; i < 20 && (await widget.profiles.all()).isNotEmpty; i++) {
        await widget.profiles.clear();
      }
    } catch (e) {
      diag('profile', 'the saved connection could not be deleted: $e');
    }
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
      // On the Mac the whole window, dialogs and menus included, is drawn
      // at the desktop's size (#375).
      builder: (context, child) => WindowZoom(
        child: strings == null ? child! : StringsScope(strings: strings, child: child!),
      ),
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
            : HomeScreen(
                key: ValueKey(_api),
                api: _api!,
                onDisconnect: _disconnect,
                profiles: _profiles,
                active: _active,
                onSwitch: _switch,
                onAddOrganisation: _addOrganisation,
              ),
      ),
    );
  }
}
