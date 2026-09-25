import 'package:flutter/material.dart';

import 'dictation.dart';
import 'i18n.dart';
import 'theme.dart';
import 'ui.dart';

/// The microphone's level over the last seconds, running from right to left
/// the way Voice Memos draws it: the proof that sound arrives, before any
/// word has been recognised. A flat line says "silent".
class Waveform extends StatelessWidget {
  const Waveform({super.key, required this.levels, this.height = 32, this.active = true});

  /// Oldest first, 0–1.
  final List<double> levels;
  final double height;
  final bool active;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Semantics(
      excludeSemantics: true,
      child: SizedBox(
        height: height,
        width: double.infinity,
        child: CustomPaint(
          painter: _WavePainter(levels: List.of(levels), color: active ? c.textAccent : c.textMuted, quiet: c.hairline),
        ),
      ),
    );
  }
}

class _WavePainter extends CustomPainter {
  _WavePainter({required this.levels, required this.color, required this.quiet});

  final List<double> levels;
  final Color color;
  final Color quiet;

  static const bar = 3.0;
  static const gap = 2.0;

  @override
  void paint(Canvas canvas, Size size) {
    final slots = (size.width / (bar + gap)).floor();
    final mid = size.height / 2;
    final paint = Paint()..strokeCap = StrokeCap.round;
    // The newest level at the right edge; slots without a level yet are the
    // quiet baseline, so the line is there from the first moment.
    for (var i = 0; i < slots; i++) {
      final from = levels.length - slots + i;
      final v = from >= 0 ? levels[from] : 0.0;
      final x = i * (bar + gap) + bar / 2;
      final h = (size.height * (0.08 + 0.92 * v)).clamp(bar, size.height);
      // Older bars fade: the eye reads the right edge as "now".
      final age = i / slots;
      paint
        ..color = v < 0.04 ? quiet : color.withValues(alpha: 0.35 + 0.65 * age)
        ..strokeWidth = bar;
      canvas.drawLine(Offset(x, mid - h / 2), Offset(x, mid + h / 2), paint);
    }
  }

  @override
  bool shouldRepaint(_WavePainter old) => true;
}

/// What dictation hears, while it hears it (#348): the words as whisper
/// refines them, or the model's download while it is fetched, and under
/// them the running waveform. It floats over the page the words are going
/// into, so the page does not jump while whisper is still correcting itself.
class DictationPreview extends StatelessWidget {
  const DictationPreview({super.key, required this.dictation, this.onStop, this.maxLines = 3});

  final Dictation dictation;
  final VoidCallback? onStop;
  final int maxLines;

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: dictation,
      builder: (context, _) {
        final c = context.colors;
        final d = dictation;
        final text = d.text;
        final String line;
        final TextStyle? style;
        if (d.preparing) {
          final p = d.progress;
          line = context.t('mobile.sprachmodellLaedt', args: {'pct': p == null ? '…' : (p * 100).floor()});
          style = context.type.bodyMedium;
        } else if (text.isEmpty) {
          line = context.t('mobile.hoertZu');
          style = context.type.bodyMedium?.copyWith(color: c.textMuted);
        } else {
          line = tail(text, 160);
          style = context.type.bodyLarge;
        }
        return Glass(
          radius: 22,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 10, 6, 12),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Semantics(
                        liveRegion: true,
                        child: Text(line, maxLines: maxLines, overflow: TextOverflow.fade, style: style),
                      ),
                    ),
                    if (onStop != null)
                      IconButton(
                        onPressed: onStop,
                        tooltip: context.t('mobile.anhalten'),
                        icon: Icon(Icons.stop_circle_rounded, color: c.textAccent, size: 30),
                      ),
                  ],
                ),
                const SizedBox(height: 8),
                Padding(
                  padding: const EdgeInsets.only(right: 10),
                  child: d.preparing
                      ? ClipRRect(
                          borderRadius: BorderRadius.circular(2),
                          child: LinearProgressIndicator(
                            value: d.progress,
                            minHeight: 3,
                            color: c.textAccent,
                            backgroundColor: c.hairline,
                          ),
                        )
                      : Waveform(levels: d.levels, height: 28, active: d.running),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

/// The last [chars] characters of [text], cut at a word: the preview shows
/// what is being said now, not how the dictation began.
String tail(String text, int chars) {
  if (text.length <= chars) return text;
  final cut = text.substring(text.length - chars);
  final space = cut.indexOf(' ');
  return '…${space >= 0 ? cut.substring(space) : cut}';
}
