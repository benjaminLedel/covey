import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../speech_model.dart';
import '../theme.dart';
import 'call.dart';
import 'ears.dart';
import 'voice.dart';

/// The call (#494): the agent's face large in the middle, its name and what
/// it is doing under it, the last few lines said, and two buttons — mute
/// and hang up. It fills the window; closing it ends the call.
class CallScreen extends StatefulWidget {
  const CallScreen({
    super.key,
    required this.api,
    required this.agentId,
    required this.agentName,
    required this.agentSlug,
    this.controller,
  });

  final CoveyApi? api;
  final String agentId;
  final String agentName;
  final String agentSlug;

  /// A test's call; otherwise one on the device's microphone and voices.
  final CallController? controller;

  /// Opens the call over [context].
  static Future<void> open(
    BuildContext context, {
    required CoveyApi api,
    required String agentId,
    required String agentName,
    required String agentSlug,
  }) => Navigator.of(context).push(
    PageRouteBuilder<void>(
      opaque: true,
      fullscreenDialog: true,
      transitionDuration: const Duration(milliseconds: 220),
      reverseTransitionDuration: const Duration(milliseconds: 160),
      pageBuilder: (_, _, _) => CallScreen(api: api, agentId: agentId, agentName: agentName, agentSlug: agentSlug),
      transitionsBuilder: (context, a, _, child) => FadeTransition(opacity: a, child: child),
    ),
  );

  @override
  State<CallScreen> createState() => _CallScreenState();
}

class _CallScreenState extends State<CallScreen> with TickerProviderStateMixin {
  CallController? _call;
  bool _own = false;

  /// The mouth: opened on each word, closing again over a quarter second.
  late final _mouth = AnimationController(vsync: this, duration: const Duration(milliseconds: 240));

  /// The slow breath of the rings while the agent thinks.
  late final _breath = AnimationController(vsync: this, duration: const Duration(milliseconds: 2400));

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_call == null) {
      final strings = Strings.of(context);
      _own = widget.controller == null;
      _call =
          widget.controller ??
          CallController(
            backend: ApiCallBackend(widget.api!, widget.agentId),
            ears: DeviceEars(widget.api!),
            speaker: MacSpeaker(),
            agentId: widget.agentId,
            appLanguage: strings.language,
            words: strings.t,
          );
      _call!.addListener(_changed);
      _call!.wordTicks.addListener(_word);
      // The model's download, while the call prepares.
      SpeechModel.instance.addListener(_changed);
      if (_own) _call!.start();
    }
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    if (still) {
      _breath.stop();
    } else if (!_breath.isAnimating) {
      _breath.repeat(reverse: true);
    }
  }

  void _changed() {
    if (!mounted) return;
    if (_call!.ended) {
      Navigator.of(context).maybePop();
      return;
    }
    setState(() {});
  }

  void _word() => _mouth.forward(from: 0);

  @override
  void dispose() {
    final call = _call!;
    call.removeListener(_changed);
    call.wordTicks.removeListener(_word);
    SpeechModel.instance.removeListener(_changed);
    // Leaving the view ends the call, whichever way it was left.
    if (_own) {
      call.dispose();
    } else {
      call.hangUp();
    }
    _mouth.dispose();
    _breath.dispose();
    super.dispose();
  }

  String _modeWord(BuildContext context, CallController call) {
    final t = Strings.of(context).t;
    switch (call.mode) {
      case CallMode.preparing:
        final p = SpeechModel.instance.progress;
        return p == null ? t('call.preparing') : t('call.preparingModel', args: {'percent': (p * 100).round()});
      case CallMode.listening:
        return t('call.listening');
      case CallMode.hearing:
        return t('call.hearing');
      case CallMode.thinking:
        return t('call.thinking');
      case CallMode.speaking:
        return t('call.speaking');
      case CallMode.muted:
        return t('call.muted');
      case CallMode.ended:
        return t('call.ended');
      case CallMode.failed:
        final f = call.failure;
        return switch (f?.problem) {
          CallProblem.denied => t('call.denied'),
          CallProblem.modelNotReady => t('call.modelNotReady'),
          CallProblem.off => t('call.off'),
          _ => t('call.failed', args: {'error': f?.detail ?? ''}),
        };
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final call = _call!;
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final dark = Theme.of(context).brightness == Brightness.dark;
    final mode = call.mode;
    final talk = switch (mode) {
      CallMode.listening || CallMode.hearing => FaceTalk.listening,
      CallMode.thinking => FaceTalk.thinking,
      CallMode.speaking => FaceTalk.speaking,
      _ => null,
    };
    final failed = mode == CallMode.failed;

    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.escape): call.hangUp,
        const SingleActivator(LogicalKeyboardKey.keyM): () => call.setMuted(!call.muted),
      },
      child: Focus(
        autofocus: true,
        child: Scaffold(
          backgroundColor: c.surface0,
          body: SafeArea(
            child: LayoutBuilder(
              builder: (context, box) {
                // The face as large as the window allows beside the name, the
                // transcript and the buttons: at most 240 pt.
                final size = math.min(
                  240.0,
                  math.max(96.0, math.min(box.maxWidth * 0.34, (box.maxHeight - 360) / 1.7)),
                );
                return Column(
                  children: [
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 14, 16, 0),
                      child: Row(
                        children: [
                          const Spacer(),
                          // A trial, and where the audio stays: said once,
                          // quietly, at the top.
                          Text(context.t('call.onDevice'), style: context.type.labelSmall),
                          const Spacer(),
                        ],
                      ),
                    ),
                    Expanded(
                      child: Center(
                        // A small window scales the whole middle down rather
                        // than cutting it.
                        child: FittedBox(
                          fit: BoxFit.scaleDown,
                          child: SizedBox(
                            width: math.max(box.maxWidth, 320),
                            child: Column(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                SizedBox.square(
                                  dimension: size * 1.7,
                                  child: AnimatedBuilder(
                                    animation: Listenable.merge([_mouth, _breath]),
                                    builder: (context, _) {
                                      final open = mode == CallMode.speaking
                                          ? (still ? 0.4 : 0.15 + 0.85 * (1 - Curves.easeOut.transform(_mouth.value)))
                                          : 0.0;
                                      final level = still ? 0.0 : call.level;
                                      return CustomPaint(
                                        painter: _Rings(
                                          tone: faceTone(widget.agentSlug, dark: dark),
                                          mode: mode,
                                          level: level,
                                          mouth: mode == CallMode.speaking && !still ? open : 0,
                                          breath: still ? 0.5 : _breath.value,
                                          face: size,
                                        ),
                                        child: Center(
                                          child: Face(
                                            slug: widget.agentSlug,
                                            size: size,
                                            talk: talk,
                                            level: level,
                                            mouth: open,
                                          ),
                                        ),
                                      );
                                    },
                                  ),
                                ),
                                const SizedBox(height: 4),
                                Text(widget.agentName, style: context.type.headlineSmall, textAlign: TextAlign.center),
                                const SizedBox(height: 6),
                                Padding(
                                  padding: const EdgeInsets.symmetric(horizontal: 32),
                                  child: Text(
                                    _modeWord(context, call),
                                    key: const ValueKey('call-state'),
                                    textAlign: TextAlign.center,
                                    style: context.type.bodyMedium?.copyWith(
                                      color: failed ? c.textDanger : c.textSecondary,
                                    ),
                                  ),
                                ),
                                const SizedBox(height: 22),
                                _Transcript(lines: call.lines, agentName: widget.agentName),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 0, 16, 32),
                      child: Row(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          _RoundButton(
                            icon: (call.muted ? AppIcons.micOff : AppIcons.mic).of(context),
                            label: context.t(call.muted ? 'call.unmute' : 'call.mute'),
                            inverted: call.muted,
                            onPressed: failed || mode == CallMode.preparing ? null : () => call.setMuted(!call.muted),
                          ),
                          const SizedBox(width: 28),
                          _RoundButton(
                            icon: AppIcons.hangUp.of(context),
                            label: context.t('call.hangUp'),
                            inverted: true,
                            onPressed: call.hangUp,
                          ),
                        ],
                      ),
                    ),
                  ],
                );
              },
            ),
          ),
        ),
      ),
    );
  }
}

/// Rings around the face in its own tone: they widen with the person's
/// voice while the call listens, pulse on the agent's words while it speaks,
/// and breathe slowly while it thinks.
class _Rings extends CustomPainter {
  _Rings({
    required this.tone,
    required this.mode,
    required this.level,
    required this.mouth,
    required this.breath,
    required this.face,
  });

  final Color tone;
  final CallMode mode;
  final double level, mouth, breath, face;

  @override
  void paint(Canvas canvas, Size size) {
    final center = size.center(Offset.zero);
    final r = face * 0.5;
    final (double spread, double alpha) = switch (mode) {
      CallMode.listening || CallMode.hearing => (0.06 + 0.34 * level, 0.10 + 0.14 * level),
      CallMode.speaking => (0.10 + 0.22 * mouth, 0.16),
      CallMode.thinking => (0.08 + 0.10 * breath, 0.10),
      _ => (0.0, 0.0),
    };
    if (alpha == 0) return;
    for (var i = 0; i < 3; i++) {
      final k = 1 + spread * (i + 1) / 1.6;
      canvas.drawCircle(center, r * k, Paint()..color = tone.withValues(alpha: alpha * (1 - i / 3)));
    }
  }

  @override
  bool shouldRepaint(_Rings old) =>
      old.tone != tone ||
      old.mode != mode ||
      old.level != level ||
      old.mouth != mouth ||
      old.breath != breath ||
      old.face != face;
}

/// The last lines said, newest at the bottom, the older ones fading.
class _Transcript extends StatelessWidget {
  const _Transcript({required this.lines, required this.agentName});

  final List<CallLine> lines;
  final String agentName;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final shown = lines.length > 3 ? lines.sublist(lines.length - 3) : lines;
    return ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 560, minHeight: 96),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (var i = 0; i < shown.length; i++)
              Opacity(
                opacity: const [0.35, 0.6, 1.0][3 - shown.length + i],
                child: Padding(
                  padding: const EdgeInsets.only(bottom: 6),
                  child: Text.rich(
                    TextSpan(
                      children: [
                        TextSpan(
                          text: '${shown[i].mine ? context.t('call.you') : agentName}  ',
                          style: context.type.labelSmall?.copyWith(color: c.textSecondary, fontWeight: FontWeight.w600),
                        ),
                        TextSpan(text: shown[i].text),
                      ],
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    textAlign: TextAlign.center,
                    style: context.type.bodySmall?.copyWith(color: c.textSecondary),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _RoundButton extends StatelessWidget {
  const _RoundButton({required this.icon, required this.label, required this.onPressed, this.inverted = false});

  final IconData icon;
  final String label;
  final VoidCallback? onPressed;
  final bool inverted;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final bg = inverted ? c.textPrimary : c.surface2;
    final fg = inverted ? c.surface2 : c.textPrimary;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Tooltip(
          message: label,
          child: Semantics(
            button: true,
            label: label,
            child: Material(
              color: onPressed == null ? bg.withValues(alpha: 0.5) : bg,
              shape: CircleBorder(side: inverted ? BorderSide.none : BorderSide(color: c.border)),
              clipBehavior: Clip.antiAlias,
              child: InkWell(
                onTap: onPressed,
                child: SizedBox.square(dimension: 64, child: Icon(icon, color: fg, size: 26)),
              ),
            ),
          ),
        ),
        const SizedBox(height: 8),
        ExcludeSemantics(child: Text(label, style: context.type.labelSmall)),
      ],
    );
  }
}
