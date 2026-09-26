import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// The window's own chrome on macOS (#356): no grey title bar above the app,
/// but one unified bar — the traffic lights sit in the app's top bar, as in
/// Finder, Mail or Teams (MainFlutterWindow.swift). The Flutter view covers
/// the whole window, so the app leaves the lights their corner, and the
/// empty parts of its bars move the window and zoom it on a double click,
/// which the title bar did before.
abstract final class MacChrome {
  static bool get active => Platform.isMacOS;

  static const _channel = MethodChannel('covey/window');

  /// The bar's height and where the traffic lights end, as the window
  /// reports them; the values of a unified toolbar until then.
  static double height = 52;
  static double lightsRight = 80;

  static Future<void> load() async {
    if (!active) return;
    try {
      final m = await _channel.invokeMapMethod<String, double>('metrics');
      height = m?['height'] ?? height;
      lightsRight = m?['lightsRight'] ?? lightsRight;
    } on PlatformException {
      // The defaults.
    } on MissingPluginException {
      // A test, or another embedding.
    }
  }

  static void drag() => _channel.invokeMethod<void>('drag').ignore();
  static void zoom() => _channel.invokeMethod<void>('zoom').ignore();
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
      leadingWidth: inset + (canPop ? 44 : 0),
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
