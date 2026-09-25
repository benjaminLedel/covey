import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

/// One icon, two drawings: the Cupertino set (SF Symbols' shapes) on Apple
/// platforms, Material elsewhere. A web icon set on an iPhone is the first
/// thing that makes a native app read as ported (ios.md).
class AppIcon {
  const AppIcon(this.material, this.apple);

  final IconData material;
  final IconData apple;

  IconData of(BuildContext context) => isApple(context) ? apple : material;
}

bool isApple(BuildContext context) {
  final p = Theme.of(context).platform;
  return p == TargetPlatform.iOS || p == TargetPlatform.macOS;
}

abstract final class AppIcons {
  static const team = AppIcon(Icons.people_alt_outlined, CupertinoIcons.person_2);
  static const notes = AppIcon(Icons.edit_note_rounded, CupertinoIcons.square_pencil);
  static const add = AppIcon(Icons.add_rounded, CupertinoIcons.add);
  static const chevron = AppIcon(Icons.chevron_right_rounded, CupertinoIcons.chevron_right);
  static const summary = AppIcon(Icons.auto_awesome_outlined, CupertinoIcons.sparkles);
  static const send = AppIcon(Icons.arrow_upward_rounded, CupertinoIcons.arrow_up);
  static const reply = AppIcon(Icons.reply_rounded, CupertinoIcons.reply);
  static const close = AppIcon(Icons.close_rounded, CupertinoIcons.xmark);
  static const delete = AppIcon(Icons.delete_outline_rounded, CupertinoIcons.trash);
  static const mic = AppIcon(Icons.mic_none_rounded, CupertinoIcons.mic);
  static const stop = AppIcon(Icons.stop_rounded, CupertinoIcons.stop_fill);
  static const record = AppIcon(Icons.circle, CupertinoIcons.circle_fill);
  static const scan = AppIcon(Icons.qr_code_scanner, CupertinoIcons.qrcode_viewfinder);
  static const checkOpen = AppIcon(Icons.check_box_outline_blank_rounded, CupertinoIcons.square);
  static const checkDone = AppIcon(Icons.check_box_rounded, CupertinoIcons.checkmark_square_fill);
  static const kindText = AppIcon(Icons.notes_rounded, CupertinoIcons.doc_text);
  static const kindVoice = AppIcon(Icons.graphic_eq_rounded, CupertinoIcons.waveform);
  static const search = AppIcon(Icons.search_rounded, CupertinoIcons.search);
  static const clear = AppIcon(Icons.cancel_rounded, CupertinoIcons.clear_circled_solid);
  static const kindMeeting = AppIcon(Icons.groups_2_outlined, CupertinoIcons.person_3);
}
