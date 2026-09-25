import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../i18n.dart';
import '../pairing.dart';

/// The camera, pointed at the web's pairing QR code (#330). It returns the
/// first code that is a covey pairing code and ignores everything else it
/// sees — a camera in an office reads a lot of QR codes.
class ScanScreen extends StatefulWidget {
  const ScanScreen({super.key});

  @override
  State<ScanScreen> createState() => _ScanScreenState();
}

class _ScanScreenState extends State<ScanScreen> {
  final _camera = MobileScannerController(formats: const [BarcodeFormat.qrCode]);
  String? _hint;
  bool _done = false;

  @override
  void dispose() {
    _camera.dispose();
    super.dispose();
  }

  void _detect(BarcodeCapture capture) {
    if (_done) return;
    for (final b in capture.barcodes) {
      final raw = b.rawValue;
      if (raw == null) continue;
      try {
        final code = PairingCode.parse(raw);
        if (code == null) {
          setState(() => _hint = context.t('mobile.keinCoveyCode'));
          continue;
        }
        _done = true;
        Navigator.of(context).pop(code);
        return;
      } on FormatException {
        setState(() => _hint = context.t('mobile.nurHttps'));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        title: Text(context.t('mobile.scanTitel')),
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
      ),
      body: Stack(
        children: [
          MobileScanner(
            controller: _camera,
            onDetect: _detect,
            // The plugin's own message is English and names the plugin; this
            // one says what to do instead.
            errorBuilder: (context, _) => Center(
              child: Padding(
                padding: const EdgeInsets.all(32),
                child: Text(context.t('mobile.keineKamera'),
                    textAlign: TextAlign.center, style: const TextStyle(color: Colors.white, height: 1.4)),
              ),
            ),
          ),
          Positioned(
            left: 24,
            right: 24,
            bottom: 32,
            child: SafeArea(
              child: Container(
                padding: const EdgeInsets.all(14),
                decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.7), borderRadius: BorderRadius.circular(12)),
                child: Text(
                  _hint ?? context.t('mobile.scanHinweis'),
                  style: const TextStyle(color: Colors.white, height: 1.4),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
