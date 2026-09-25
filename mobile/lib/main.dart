import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import 'api.dart';
import 'i18n.dart';
import 'profile.dart';
import 'screens/connect.dart';
import 'screens/home.dart';
import 'splash.dart';
import 'theme.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(CoveyApp(profiles: ProfileStore()));
}

/// The covey mobile app (spec/27): the chat where the person is.
///
/// Three surfaces and nothing of the console — what waits, the colleagues,
/// the thread. The app starts at the question no consumer app asks, "where
/// is your covey?", because every installation is somebody else's machine.
class CoveyApp extends StatefulWidget {
  const CoveyApp({super.key, required this.profiles});

  final ProfileStore profiles;

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

  @override
  void initState() {
    super.initState();
    _start();
  }

  Future<void> _start() async {
    // The device language, falling back to English (spec/27).
    final locale = WidgetsBinding.instance.platformDispatcher.locale;
    final strings = await Strings.load(locale);
    var saved = await widget.profiles.read();
    // A debug build can be pointed at an instance from the command line —
    // `flutter run --dart-define=COVEY_INSTANCE=http://localhost:8494
    // --dart-define=COVEY_KEY=covey_…` — so working on the app against
    // `make run` does not start at the connect screen every time. Release
    // builds ignore it: a key compiled into an app is exactly what spec/27
    // forbids.
    const devInstance = String.fromEnvironment('COVEY_INSTANCE');
    const devKey = String.fromEnvironment('COVEY_KEY');
    if (kDebugMode && saved == null && devInstance != '' && devKey != '') {
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
            ? Splash(
                key: const ValueKey('splash'),
                ready: _loaded.future,
                onDone: () => setState(() => _splash = false),
              )
            : _api == null
                ? ConnectScreen(key: const ValueKey('connect'), onConnected: _connected)
                : HomeScreen(key: ValueKey(_api), api: _api!, onDisconnect: _disconnect),
      ),
    );
  }
}
