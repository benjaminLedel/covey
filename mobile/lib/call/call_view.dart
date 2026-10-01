import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../push.dart';
import '../speech_model.dart';
import '../theme.dart';
import 'call.dart';
import 'ears.dart';
import 'fillers.dart';
import 'greeting.dart';
import 'recording.dart';
import 'sounds.dart';
import 'understood.dart';
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

  /// A test's call; otherwise one on the device's microphone and the
  /// organisation's voice provider.
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
      if (widget.controller == null) {
        final api = widget.api!;
        final backend = ApiCallBackend(api, widget.agentId, language: strings.language);
        _call = CallController(
          backend: backend,
          ears: DeviceEars(api, language: strings.language),
          // The organisation's voice provider speaks; the Mac only
          // without one.
          speaker: AgentSpeaker(
            output: MacVoiceOutput(),
            agentId: widget.agentId,
            available: () async => (await api.speechModel()).synthesize,
            spoken: backend.spokenVoice,
            provider: (v) => providerVoice(api, v, agentId: widget.agentId),
            // The fillers stay synthesised between calls (#500).
            fillerCache: FillerCache.appSupport(),
          ),
          sounds: CallSounds(
            output: const ChannelEarconOutput(),
            enabled: () => CallSettings.sounds.value,
            volume: () => CallSettings.volume.value,
          ),
          agentId: widget.agentId,
          agentName: widget.agentName,
          appLanguage: strings.language,
          words: strings.t,
          tuning: CallSettings.tuning,
          recording: CallRecording.open,
          // The agent greets when the call connects (#506).
          greeter: CallGreeter(enabled: () => CallSettings.greeting.value),
          // No banners while the call is on (#525).
          hush: PushNotices.instance.hush,
          // And hangs up after its goodbye when the person ends it (#517).
          mayHangUp: () => CallSettings.hangUp.value,
        );
      } else {
        _call = widget.controller;
      }
      _call!.addListener(_changed);
      _call!.wordTicks.addListener(_word);
      _call!.speaker.fallback.addListener(_changed);
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
    // A new turn stands as understood: the correction field starts empty.
    final u = _call!.understood;
    if (!identical(u, _shown)) {
      _shown = u;
      _edit.clear();
    }
    setState(() {});
  }

  UnderstoodTurn? _shown;
  final _edit = TextEditingController();
  final _editFocus = FocusNode();
  final _keys = FocusNode();

  /// The keyboard of the call: Enter sends what stands as understood, Esc
  /// discards it, and any other key starts correcting it. With nothing
  /// understood, Esc hangs up and M mutes.
  KeyEventResult _onKey(FocusNode node, KeyEvent e) {
    if (e is! KeyDownEvent) return KeyEventResult.ignored;
    final call = _call!;
    final u = call.understood;
    final key = e.logicalKey;
    if (u != null && !u.done) {
      if (key == LogicalKeyboardKey.enter || key == LogicalKeyboardKey.numpadEnter) {
        u.send();
        return KeyEventResult.handled;
      }
      if (key == LogicalKeyboardKey.escape) {
        u.discard();
        return KeyEventResult.handled;
      }
      final ch = e.character;
      if (ch != null && ch.isNotEmpty && ch.runes.every((r) => r >= 0x20)) {
        _startEditing(u, ch);
        return KeyEventResult.handled;
      }
      return KeyEventResult.ignored;
    }
    if (key == LogicalKeyboardKey.escape) {
      call.hangUp();
      return KeyEventResult.handled;
    }
    if (key == LogicalKeyboardKey.keyM) {
      call.setMuted(!call.muted);
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  void _startEditing(UnderstoodTurn u, [String typed = '']) {
    u.edit();
    final text = typed.isEmpty ? u.text : '${u.text}$typed';
    _edit.value = TextEditingValue(
      text: text,
      selection: TextSelection.collapsed(offset: text.length),
    );
    _editFocus.requestFocus();
  }

  void _word() => _mouth.forward(from: 0);

  @override
  void dispose() {
    final call = _call!;
    call.removeListener(_changed);
    call.wordTicks.removeListener(_word);
    call.speaker.fallback.removeListener(_changed);
    SpeechModel.instance.removeListener(_changed);
    _edit.dispose();
    _editFocus.dispose();
    _keys.dispose();
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
      case CallMode.understood:
        return t('call.understoodState');
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
      CallMode.listening || CallMode.hearing || CallMode.understood => FaceTalk.listening,
      CallMode.thinking => FaceTalk.thinking,
      CallMode.speaking => FaceTalk.speaking,
      _ => null,
    };
    final failed = mode == CallMode.failed;

    return Focus(
      focusNode: _keys,
      autofocus: true,
      onKeyEvent: _onKey,
      child: Scaffold(
        backgroundColor: c.surface0,
        body: SafeArea(
          child: LayoutBuilder(
            builder: (context, box) {
              // The face as large as the window allows beside the name, the
              // transcript and the buttons: at most 240 pt.
              final size = math.min(240.0, math.max(96.0, math.min(box.maxWidth * 0.34, (box.maxHeight - 360) / 1.7)));
              return Column(
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 14, 16, 0),
                    child: Column(
                      children: [
                        // A trial, and where the audio goes: said once,
                        // quietly, at the top — on this Mac, or, while the
                        // organisation allows it, its server (#516).
                        Text(
                          context.t(call.serverRecognition ? 'call.onServer' : 'call.onDevice'),
                          key: ValueKey(call.serverRecognition ? 'call-on-server' : 'call-on-device'),
                          style: context.type.labelSmall,
                        ),
                        // That the Mac's voice speaks because the voice
                        // provider is not there (#497).
                        if (call.speaker.fallback.value && !failed && mode != CallMode.preparing)
                          Padding(
                            padding: const EdgeInsets.only(top: 4),
                            child: Text(
                              context.t('call.providerUnavailable'),
                              key: const ValueKey('call-provider-unavailable'),
                              style: context.type.labelSmall?.copyWith(color: c.textMuted),
                            ),
                          ),
                        // And that this call keeps its turns (#498).
                        if (call.recordingTurns)
                          Padding(
                            padding: const EdgeInsets.only(top: 4),
                            child: Text(
                              context.t('call.recordingLine'),
                              key: const ValueKey('call-recording'),
                              style: context.type.labelSmall?.copyWith(color: c.textAccent),
                            ),
                          ),
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
                                  animation: Listenable.merge([_mouth, _breath, call.speaker.level]),
                                  builder: (context, _) {
                                    // The voice provider opens the mouth
                                    // with its audio (#497); the Mac's
                                    // voice on each word it reaches.
                                    final own = call.speaker.level.value;
                                    final open = mode == CallMode.speaking
                                        ? (still
                                              ? 0.4
                                              : own != null
                                              ? 0.1 + 0.9 * own
                                              : 0.15 + 0.85 * (1 - Curves.easeOut.transform(_mouth.value)))
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
                              if (call.understood case final u?)
                                _Understood(
                                  turn: u,
                                  controller: _edit,
                                  focus: _editFocus,
                                  onTap: () => _startEditing(u),
                                  onKeys: () => _keys.requestFocus(),
                                ),
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

/// What was understood of the last turn, in the moment before it is sent
/// (#498): the text, a thin line running out until it is sent, and what the
/// keys do. Typing turns it into a field; Enter sends the correction, Esc
/// discards the turn.
class _Understood extends StatelessWidget {
  const _Understood({
    required this.turn,
    required this.controller,
    required this.focus,
    required this.onTap,
    required this.onKeys,
  });

  final UnderstoodTurn turn;
  final TextEditingController controller;
  final FocusNode focus;
  final VoidCallback onTap;

  /// Gives the keyboard back to the call once the turn is done.
  final VoidCallback onKeys;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final hint = turn.editing
        ? context.t('call.understoodEditing')
        : turn.cleaning
        ? context.t('call.understoodCleaning')
        : context.t('call.understoodKeys');
    final sendsAt = turn.sendsAt;
    return ConstrainedBox(
      key: const ValueKey('call-understood'),
      constraints: const BoxConstraints(maxWidth: 560),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 10, 16, 16),
        child: Material(
          color: c.surface2,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
            side: BorderSide(color: c.hairline, width: 0.6),
          ),
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            onTap: turn.editing ? null : onTap,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(context.t('call.understood'), style: context.type.labelSmall),
                      const SizedBox(height: 4),
                      if (turn.editing)
                        Focus(
                          onKeyEvent: (node, e) {
                            if (e is KeyDownEvent && e.logicalKey == LogicalKeyboardKey.escape) {
                              turn.discard();
                              onKeys();
                              return KeyEventResult.handled;
                            }
                            return KeyEventResult.ignored;
                          },
                          child: TextField(
                            key: const ValueKey('call-understood-field'),
                            controller: controller,
                            focusNode: focus,
                            autofocus: true,
                            maxLines: 4,
                            minLines: 1,
                            style: context.type.bodyLarge,
                            decoration: const InputDecoration.collapsed(hintText: ''),
                            textInputAction: TextInputAction.send,
                            onSubmitted: (v) {
                              turn.send(v);
                              onKeys();
                            },
                          ),
                        )
                      else
                        Text(
                          turn.text,
                          key: const ValueKey('call-understood-text'),
                          style: context.type.bodyLarge,
                          maxLines: 4,
                          overflow: TextOverflow.ellipsis,
                        ),
                      const SizedBox(height: 6),
                      Text(hint, style: context.type.bodySmall?.copyWith(color: c.textMuted)),
                    ],
                  ),
                ),
                // The time left before it is sent.
                SizedBox(
                  height: 2,
                  child: sendsAt == null || still
                      ? null
                      : TweenAnimationBuilder<double>(
                          key: ValueKey(sendsAt),
                          tween: Tween(begin: 1, end: 0),
                          duration: turn.window,
                          builder: (context, v, _) => Align(
                            alignment: Alignment.centerLeft,
                            child: FractionallySizedBox(
                              widthFactor: v,
                              child: ColoredBox(color: c.textAccent.withValues(alpha: 0.6)),
                            ),
                          ),
                        ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
