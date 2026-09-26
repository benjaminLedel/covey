import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'prefs.dart';

/// The window's own chrome on macOS (#356): no grey title bar above the app,
/// but one unified bar — the traffic lights sit in the app's top bar, as in
/// Finder, Mail or Teams (MainFlutterWindow.swift). The Flutter view covers
/// the whole window, so the app leaves the lights their corner, and the
/// empty parts of its bars move the window and zoom it on a double click,
/// which the title bar did before.
abstract final class MacChrome {
  static bool get active => Platform.isMacOS;

  static const _channel = MethodChannel('covey/window');

  /// The bar's height and where the traffic lights end, in the app's
  /// zoomed points ([WindowZoom]): the window reports them in its own, and
  /// the lights stay where they are whatever the zoom.
  static double get height => _height / WindowZoom.scale.value;
  static double get lightsRight => _lightsRight / WindowZoom.scale.value;

  // The values of a unified toolbar until the window reports its own.
  static double _height = 52;
  static double _lightsRight = 80;

  static Future<void> load() async {
    if (!active) return;
    try {
      final m = await _channel.invokeMapMethod<String, double>('metrics');
      _height = m?['height'] ?? _height;
      _lightsRight = m?['lightsRight'] ?? _lightsRight;
    } on PlatformException {
      // The defaults.
    } on MissingPluginException {
      // A test, or another embedding.
    }
  }

  static void drag() => _channel.invokeMethod<void>('drag').ignore();
  static void zoom() => _channel.invokeMethod<void>('zoom').ignore();

  /// Brings the app and its window to the front — the window may have been
  /// closed while the app kept running.
  static Future<void> activate() async {
    if (!active) return;
    try {
      await _channel.invokeMethod<void>('activate');
    } on PlatformException {
      // Stays where it is.
    }
  }
}

/// The whole window drawn at a zoom factor on macOS (#375). The app's sizes
/// are a phone's — 17 point text, 46 point controls, made for a thumb — and
/// on a desktop read at arm's length with a pointer they look swollen. One
/// factor shrinks type, spacing, controls, dialogs and menus together, so no
/// screen needs a desktop twin; ⌘+ ⌘− ⌘0 change it as in a browser, and the
/// choice is kept.
class WindowZoom extends StatefulWidget {
  const WindowZoom({super.key, required this.child});

  final Widget child;

  static const standard = 0.85;

  /// What text gets on top of the zoom.
  static const text = 0.92;
  static const _steps = [0.7, 0.75, 0.8, 0.85, 0.9, 1.0, 1.1, 1.25];
  static const _pref = 'mac.zoom';

  /// The factor in force; 1 away from macOS.
  static final scale = ValueNotifier<double>(MacChrome.active ? standard : 1);

  static Future<void> load() async {
    if (!MacChrome.active) return;
    final saved = double.tryParse(await Prefs.instance.read(_pref) ?? '');
    if (saved != null && saved >= _steps.first && saved <= _steps.last) scale.value = saved;
  }

  static void _step(int by) {
    final i = _steps.indexWhere((s) => s >= scale.value - 0.001);
    _set(_steps[((i < 0 ? _steps.length - 1 : i) + by).clamp(0, _steps.length - 1)]);
  }

  static void _set(double value) {
    if (value == scale.value) return;
    scale.value = value;
    Prefs.instance.write(_pref, '$value').ignore();
  }

  @override
  State<WindowZoom> createState() => _WindowZoomState();
}

class _WindowZoomState extends State<WindowZoom> {
  @override
  void initState() {
    super.initState();
    WindowZoom.scale.addListener(_changed);
  }

  @override
  void dispose() {
    WindowZoom.scale.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    setState(() {});
    // What read the bar's height from [MacChrome] reads it again: a zoom
    // is rare enough to rebuild everything for it.
    void rebuild(Element e) {
      e.markNeedsBuild();
      e.visitChildren(rebuild);
    }

    (context as Element).visitChildren(rebuild);
  }

  @override
  Widget build(BuildContext context) {
    if (!MacChrome.active) return widget.child;
    final s = WindowZoom.scale.value;
    final mq = MediaQuery.of(context);
    final size = mq.size / s;
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.equal, meta: true): () => WindowZoom._step(1),
        const SingleActivator(LogicalKeyboardKey.add, meta: true): () => WindowZoom._step(1),
        const SingleActivator(LogicalKeyboardKey.numpadAdd, meta: true): () => WindowZoom._step(1),
        const SingleActivator(LogicalKeyboardKey.minus, meta: true): () => WindowZoom._step(-1),
        const SingleActivator(LogicalKeyboardKey.numpadSubtract, meta: true): () => WindowZoom._step(-1),
        const SingleActivator(LogicalKeyboardKey.digit0, meta: true): () => WindowZoom._set(WindowZoom.standard),
      },
      child: FittedBox(
        fit: BoxFit.fill,
        alignment: Alignment.topLeft,
        child: SizedBox.fromSize(
          size: size,
          child: MediaQuery(
            data: mq.copyWith(
              size: size,
              padding: mq.padding / s,
              viewPadding: mq.viewPadding / s,
              viewInsets: mq.viewInsets / s,
              // Text a step further down than the rest (#376): the zoom
              // alone left it reading large next to other desktop apps,
              // while the controls were the right size.
              textScaler: _TimesScaler(mq.textScaler, WindowZoom.text),
            ),
            child: widget.child,
          ),
        ),
      ),
    );
  }
}

class _TimesScaler extends TextScaler {
  const _TimesScaler(this.base, this.factor);

  final TextScaler base;
  final double factor;

  @override
  double scale(double fontSize) => base.scale(fontSize) * factor;

  @override
  // ignore: deprecated_member_use
  double get textScaleFactor => base.textScaleFactor * factor;

  @override
  bool operator ==(Object other) => other is _TimesScaler && other.base == base && other.factor == factor;

  @override
  int get hashCode => Object.hash(base, factor);
}

/// How much room the traffic lights take at the left edge of what is below:
/// the lights' width at the window's left edge, nothing in a pane further
/// right (the list and detail panes of a wide window say so with 0).
class LightsInset extends InheritedWidget {
  const LightsInset({super.key, required this.left, required super.child});

  final double left;

  static double of(BuildContext context) {
    if (!MacChrome.active) return 0;
    return context.dependOnInheritedWidgetOfExactType<LightsInset>()?.left ?? MacChrome.lightsRight;
  }

  @override
  bool updateShouldNotify(LightsInset old) => old.left != left;
}

/// An empty part of a bar that moves the window when dragged and zooms it
/// on a double click. Controls inside it keep their taps.
class WindowDrag extends StatelessWidget {
  const WindowDrag({super.key, this.child = const SizedBox.expand()});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    if (!MacChrome.active) return child;
    return GestureDetector(
      behavior: HitTestBehavior.translucent,
      onPanStart: (_) => MacChrome.drag(),
      onDoubleTap: MacChrome.zoom,
      child: child,
    );
  }
}

/// The app bar of a pushed page. On macOS it is the window's title bar:
/// as tall as the unified bar, the back control right of the traffic lights,
/// and draggable where it is empty. Elsewhere it is a plain [AppBar].
class ChromeAppBar extends StatelessWidget implements PreferredSizeWidget {
  const ChromeAppBar({
    super.key,
    this.title,
    this.actions,
    this.titleSpacing,
    this.backgroundColor,
    this.foregroundColor,
  });

  final Widget? title;
  final List<Widget>? actions;
  final double? titleSpacing;
  final Color? backgroundColor;
  final Color? foregroundColor;

  @override
  Size get preferredSize => Size.fromHeight(MacChrome.active ? MacChrome.height : kToolbarHeight);

  @override
  Widget build(BuildContext context) {
    if (!MacChrome.active) {
      return AppBar(
        title: title,
        actions: actions,
        titleSpacing: titleSpacing,
        backgroundColor: backgroundColor,
        foregroundColor: foregroundColor,
      );
    }
    final inset = LightsInset.of(context);
    final canPop = ModalRoute.of(context)?.canPop ?? false;
    return AppBar(
      toolbarHeight: MacChrome.height,
      automaticallyImplyLeading: false,
      leadingWidth: inset + (canPop ? 48 : 0),
      leading: canPop
          ? Row(
              children: [
                SizedBox(width: inset),
                const BackButton(),
              ],
            )
          : (inset > 0 ? SizedBox(width: inset) : null),
      title: title,
      titleSpacing: titleSpacing ?? (canPop ? 4 : 16),
      actions: actions,
      backgroundColor: backgroundColor,
      foregroundColor: foregroundColor,
      flexibleSpace: const WindowDrag(),
    );
  }
}
