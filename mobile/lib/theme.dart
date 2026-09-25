// THESIS: Two spaces, Team and Notes, and a floating glass capsule between
// them; capture is always one thumb away. Refuses the stock Material app —
// centred bars, grey list tiles, a bottom navigation strip.
// OWN-WORLD: covey's neutral sheet (#f2f2f2, dark #121214; ink #16161a)
// layered by white inset groups and hairlines, Inter carrying every word on
// a steep scale (34 bold titles, 17 body). Ink fills what commits (save,
// send, connect); the clay's text cut starts things (+, new, summarise); the
// clay tile itself is the signet. Colour in a list comes from the agent
// faces and, quietly, from the meeting mark.
// STORY: open the app, see who waits and who works, answer or capture.
// FIRST VIEWPORT: large left-aligned title "Team" with the waiting count
// beneath; waiting questions as full-width cards led by a face; departments
// as inset groups of face, name, role, state in words; the capsule
// Team · Notizen floating over the content, a round + beside it.
// FORM: "Räume" (Arc spaces + iOS 26 tab capsule), 3rd of 7, seed ed24bc94.
// FINISH: unreviewed and undocumented is unfinished; this build ends with the
// finish review, the verdict, DESIGN.md, and every shipping raster carrying
// its provenance
import 'package:flutter/material.dart';

/// The control plane's design language (web/src/styles.css): an exactly
/// neutral ground and its dark counterpart, separated by layering and
/// hairlines rather than hue; Inter alone; the clay as signet and the
/// text-capable accent for action. The values are the web's tokens, light
/// and dark, so both appearances are the ones already measured for contrast.
class CoveyColors extends ThemeExtension<CoveyColors> {
  const CoveyColors({
    required this.surface0,
    required this.surface1,
    required this.surface2,
    required this.textPrimary,
    required this.textSecondary,
    required this.textMuted,
    required this.border,
    required this.hairline,
    required this.textAccent,
    required this.bgAccent,
    required this.textWait,
    required this.bgWait,
    required this.textDanger,
    required this.textSuccess,
    required this.glass,
    required this.shadow,
  });

  final Color surface0, surface1, surface2;
  final Color textPrimary, textSecondary, textMuted;
  final Color border, hairline;
  final Color textAccent, bgAccent, textWait, bgWait, textDanger, textSuccess;

  /// The capsule's fill under its blur — the surface, let through.
  final Color glass;
  final Color shadow;

  static const light = CoveyColors(
    surface0: Color(0xFFF2F2F2),
    surface1: Color(0xFFE6E6E6),
    surface2: Color(0xFFFFFFFF),
    textPrimary: Color(0xFF16161A),
    textSecondary: Color(0xFF48484E),
    textMuted: Color(0xFF5A5A60),
    border: Color(0x29000000),
    hairline: Color(0x1A000000),
    textAccent: Color(0xFF8F3F18),
    bgAccent: Color(0xFFF2E8E4),
    textWait: Color(0xFF37506E),
    bgWait: Color(0xFFECEFF3),
    textDanger: Color(0xFF9D2427),
    textSuccess: Color(0xFF0E6B45),
    glass: Color(0xB8FFFFFF),
    shadow: Color(0x24000000),
  );

  static const dark = CoveyColors(
    surface0: Color(0xFF121214),
    surface1: Color(0xFF0B0B0C),
    surface2: Color(0xFF1F1F22),
    textPrimary: Color(0xFFF4F4F5),
    textSecondary: Color(0xFFB4B4BA),
    textMuted: Color(0xFF9B9BA1),
    border: Color(0x33FFFFFF),
    hairline: Color(0x1FFFFFFF),
    textAccent: Color(0xFFF0A184),
    bgAccent: Color(0xFF2B1A12),
    textWait: Color(0xFF9DBBE0),
    bgWait: Color(0xFF1C2330),
    textDanger: Color(0xFFEF8D84),
    textSuccess: Color(0xFF4EC98A),
    glass: Color(0xB82A2A2E),
    shadow: Color(0x66000000),
  );

  @override
  CoveyColors copyWith() => this;

  @override
  CoveyColors lerp(CoveyColors? other, double t) => t < 0.5 || other == null ? this : other;
}

/// The type scale: steep on purpose. A title is a title at 34 bold, the body
/// reads at 17, secondary lines drop to 15 and captions to 13 — four steps
/// the eye can tell apart without reading. Inter tightens as it grows.
TextTheme _type(CoveyColors c) {
  TextStyle s(double size, FontWeight w, {double spacing = 0, double height = 1.3, Color? color}) => TextStyle(
    fontFamily: 'Inter',
    fontSize: size,
    fontWeight: w,
    letterSpacing: spacing,
    height: height,
    color: color ?? c.textPrimary,
  );
  return TextTheme(
    // The large title of a space.
    headlineMedium: s(34, FontWeight.w700, spacing: -0.9, height: 1.12),
    // The title of a detail screen, and a note's heading.
    headlineSmall: s(26, FontWeight.w700, spacing: -0.6, height: 1.18),
    // Section titles inside a space.
    titleLarge: s(20, FontWeight.w600, spacing: -0.3, height: 1.25),
    // A row's name, a card's lead line, the collapsed bar title.
    titleMedium: s(17, FontWeight.w600, spacing: -0.2),
    titleSmall: s(15, FontWeight.w600, spacing: -0.1),
    bodyLarge: s(17, FontWeight.w400, spacing: -0.2, height: 1.42),
    bodyMedium: s(15, FontWeight.w400, spacing: -0.1, height: 1.4, color: c.textSecondary),
    bodySmall: s(13, FontWeight.w400, height: 1.35, color: c.textMuted),
    labelLarge: s(15, FontWeight.w600, spacing: -0.1),
    labelMedium: s(13, FontWeight.w600),
    labelSmall: s(12, FontWeight.w500, color: c.textMuted),
  );
}

ThemeData coveyTheme(Brightness b) {
  final c = b == Brightness.light ? CoveyColors.light : CoveyColors.dark;
  final type = _type(c);
  final scheme = ColorScheme.fromSeed(seedColor: const Color(0xFFCC7A5B), brightness: b).copyWith(
    primary: c.textPrimary,
    onPrimary: c.surface2,
    secondary: c.textAccent,
    surface: c.surface0,
    onSurface: c.textPrimary,
    onSurfaceVariant: c.textMuted,
    surfaceContainerHighest: c.surface2,
    error: c.textDanger,
    outline: c.border,
    outlineVariant: c.hairline,
  );
  final round = RoundedRectangleBorder(borderRadius: BorderRadius.circular(14));
  return ThemeData(
    colorScheme: scheme,
    fontFamily: 'Inter',
    textTheme: type,
    scaffoldBackgroundColor: c.surface0,
    splashFactory: InkSparkle.splashFactory,
    extensions: [c],
    appBarTheme: AppBarTheme(
      backgroundColor: c.surface0,
      foregroundColor: c.textPrimary,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: false,
      // No titleTextStyle here: it would also shrink the expanded large
      // title of a space to bar size. Bars take titleLarge, large titles
      // headlineMedium, both from the text theme.
    ),
    dividerTheme: DividerThemeData(color: c.hairline, space: 1, thickness: 0.6),
    listTileTheme: ListTileThemeData(
      titleTextStyle: type.titleMedium,
      subtitleTextStyle: type.bodyMedium,
      minVerticalPadding: 10,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        textStyle: type.labelLarge,
        minimumSize: const Size(44, 50),
        shape: round,
        backgroundColor: c.textPrimary,
        foregroundColor: c.surface2,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        textStyle: type.labelLarge,
        minimumSize: const Size(44, 50),
        shape: round,
        foregroundColor: c.textPrimary,
        side: BorderSide(color: c.border),
        backgroundColor: c.surface2,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        textStyle: type.labelLarge,
        foregroundColor: c.textAccent,
        minimumSize: const Size(44, 44),
      ),
    ),
    snackBarTheme: SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      backgroundColor: c.textPrimary,
      contentTextStyle: type.bodyMedium?.copyWith(color: c.surface2),
      shape: round,
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: c.surface2,
      titleTextStyle: type.titleLarge,
      contentTextStyle: type.bodyMedium,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(24)),
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: c.surface2,
      showDragHandle: true,
      dragHandleColor: c.border,
      shape: const RoundedRectangleBorder(borderRadius: BorderRadius.vertical(top: Radius.circular(28))),
    ),
    popupMenuTheme: PopupMenuThemeData(
      color: c.surface2,
      textStyle: type.bodyLarge,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
    ),
    textSelectionTheme: TextSelectionThemeData(
      cursorColor: c.textAccent,
      selectionColor: c.textAccent.withValues(alpha: 0.25),
      selectionHandleColor: c.textAccent,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: c.surface2,
      hintStyle: type.bodyLarge?.copyWith(color: c.textMuted),
      labelStyle: type.bodyMedium,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: c.hairline),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: c.hairline),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide(color: c.textAccent, width: 1.4),
      ),
    ),
  );
}

extension CoveyTheme on BuildContext {
  CoveyColors get colors => Theme.of(this).extension<CoveyColors>()!;
  TextTheme get type => Theme.of(this).textTheme;
}
