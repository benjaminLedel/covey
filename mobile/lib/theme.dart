import 'package:flutter/material.dart';

/// The control plane's design language (web/src/styles.css): near-monochrome,
/// one accent that carries links and nothing else, and the state of a thing
/// said in words rather than in colour alone. The values are the web's
/// tokens, light and dark, so both appearances are the ones already measured
/// for contrast there.
class CoveyColors extends ThemeExtension<CoveyColors> {
  const CoveyColors({
    required this.surface0,
    required this.surface2,
    required this.textPrimary,
    required this.textMuted,
    required this.border,
    required this.textAccent,
    required this.bgAccent,
    required this.textWait,
    required this.bgWait,
    required this.textDanger,
  });

  final Color surface0, surface2, textPrimary, textMuted, border, textAccent, bgAccent, textWait, bgWait, textDanger;

  static const light = CoveyColors(
    surface0: Color(0xFFF2F2F2),
    surface2: Color(0xFFFFFFFF),
    textPrimary: Color(0xFF16161A),
    textMuted: Color(0xFF5A5A60),
    border: Color(0x29000000),
    textAccent: Color(0xFF8F3F18),
    bgAccent: Color(0xFFF2E8E4),
    textWait: Color(0xFF37506E),
    bgWait: Color(0xFFECEFF3),
    textDanger: Color(0xFF9D2427),
  );

  static const dark = CoveyColors(
    surface0: Color(0xFF121214),
    surface2: Color(0xFF1F1F22),
    textPrimary: Color(0xFFF4F4F5),
    textMuted: Color(0xFF9B9BA1),
    border: Color(0x33FFFFFF),
    textAccent: Color(0xFFF0A184),
    bgAccent: Color(0xFF2B1A12),
    textWait: Color(0xFF9DBBE0),
    bgWait: Color(0xFF1C2330),
    textDanger: Color(0xFFEF8D84),
  );

  @override
  CoveyColors copyWith() => this;

  @override
  CoveyColors lerp(CoveyColors? other, double t) => t < 0.5 || other == null ? this : other;
}

ThemeData coveyTheme(Brightness b) {
  final c = b == Brightness.light ? CoveyColors.light : CoveyColors.dark;
  final scheme = ColorScheme.fromSeed(
    seedColor: c.textAccent,
    brightness: b,
  ).copyWith(
    primary: c.textPrimary,
    onPrimary: c.surface2,
    surface: c.surface0,
    onSurface: c.textPrimary,
    error: c.textDanger,
    outlineVariant: c.border,
  );
  return ThemeData(
    colorScheme: scheme,
    scaffoldBackgroundColor: c.surface0,
    extensions: [c],
    appBarTheme: AppBarTheme(backgroundColor: c.surface0, foregroundColor: c.textPrimary, elevation: 0, scrolledUnderElevation: 0),
    dividerTheme: DividerThemeData(color: c.border, space: 1),
    navigationBarTheme: NavigationBarThemeData(backgroundColor: c.surface2, indicatorColor: c.bgAccent),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: c.surface2,
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: c.border)),
      enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(10), borderSide: BorderSide(color: c.border)),
    ),
  );
}

extension CoveyTheme on BuildContext {
  CoveyColors get colors => Theme.of(this).extension<CoveyColors>()!;
}
