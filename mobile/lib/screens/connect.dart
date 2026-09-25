import 'dart:io';

import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../icons.dart';
import '../mark.dart';
import '../pairing.dart';
import '../theme.dart';
import 'scan.dart';

/// Where is your covey?
///
/// Two ways in. The first is the QR code the web shows under Profile &
/// Settings (#330): the app scans it and exchanges the code for a key of its
/// own, and nobody types anything. The second is the address and an API key,
/// for when there is no second screen at hand. The address starts at
/// app.covey.work, where most people's covey is (#331).
class ConnectScreen extends StatefulWidget {
  // Not const: without an explicit [desktop] the platform is asked.
  ConnectScreen({
    super.key,
    required this.onConnected,
    this.makeApi = CoveyApi.new,
    this.scan,
    this.redeem = redeemPairing,
    bool? desktop,
  }) : desktop = desktop ?? isDesktop;

  /// Whether this is the desktop app; the platform decides, a test may say.
  final bool desktop;

  final Future<void> Function(Uri instance, String key) onConnected;

  /// Swapped in tests, so the screen can be driven without a network or a
  /// camera.
  final CoveyApi Function(Uri base, String key) makeApi;
  final Future<PairingCode?> Function(BuildContext context)? scan;
  final Future<String> Function(PairingCode code) redeem;

  static const defaultInstance = 'app.covey.work';

  /// A desktop has no camera worth pointing at its own screen (#334): it pairs
  /// through "Open in the app" on the web's pairing card instead.
  static bool get isDesktop => Platform.isMacOS || Platform.isWindows || Platform.isLinux;

  @override
  State<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends State<ConnectScreen> {
  final _address = TextEditingController(text: ConnectScreen.defaultInstance);
  final _key = TextEditingController();
  String? _error;
  // Which way in the error belongs to: it stands at the button that caused it,
  // or a person at the bottom of the form never sees it.
  bool _errorFromForm = false;
  bool _busy = false;
  late bool _manual = widget.desktop;

  @override
  void dispose() {
    _address.dispose();
    _key.dispose();
    super.dispose();
  }

  Future<PairingCode?> _openScanner(BuildContext context) =>
      Navigator.of(context).push<PairingCode>(MaterialPageRoute(builder: (_) => const ScanScreen()));

  Future<void> _pair() async {
    final t = Strings.of(context).t;
    final code = await (widget.scan ?? _openScanner)(context);
    if (code == null || !mounted) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    String? error;
    String? key;
    try {
      key = await widget.redeem(code);
    } on ApiException catch (e) {
      error = e.status == 401 ? t('mobile.koppelnFehler') : (e.status == 0 ? t('mobile.nichtErreichbar') : e.message);
    }
    if (!mounted) return;
    if (error != null) {
      setState(() {
        _busy = false;
        _error = error;
        _errorFromForm = false;
      });
      return;
    }
    await widget.onConnected(code.instance, key!);
  }

  Future<void> _connect() async {
    final t = Strings.of(context).t;
    final Uri base;
    try {
      base = parseInstance(_address.text);
    } on FormatException {
      setState(() {
        _error = t('mobile.nurHttps');
        _errorFromForm = true;
      });
      return;
    }
    final key = _key.text.trim();
    setState(() {
      _busy = true;
      _error = null;
    });
    final api = widget.makeApi(base, key);
    String? error;
    if (!await api.reachable()) {
      error = t('mobile.nichtErreichbar');
    } else {
      try {
        await api.me();
        await api.version();
      } on ApiException catch (e) {
        error = e.status == 401 || e.status == 403 ? t('mobile.schluesselAbgelehnt') : t('mobile.keinCovey');
      } on FormatException {
        error = t('mobile.keinCovey');
      }
    }
    if (!mounted) return;
    if (error != null) {
      setState(() {
        _busy = false;
        _error = error;
        _errorFromForm = true;
      });
      return;
    }
    await widget.onConnected(base, key);
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Scaffold(
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 480),
            child: ListView(
              padding: const EdgeInsets.fromLTRB(24, 40, 24, 24),
              children: [
                Row(
                  children: [
                    const CoveyMark(size: 44),
                    const SizedBox(width: 12),
                    Text('covey', style: context.type.titleLarge),
                  ],
                ),
                const SizedBox(height: 36),
                Text(context.t('mobile.verbindenTitel'), style: context.type.headlineMedium),
                const SizedBox(height: 12),
                Text(
                  widget.desktop ? context.t('mobile.desktopHinweis') : context.t('mobile.scanHinweis'),
                  style: context.type.bodyLarge?.copyWith(color: c.textMuted),
                ),
                if (!widget.desktop) ...[
                  const SizedBox(height: 24),
                  FilledButton.icon(
                    onPressed: _busy ? null : _pair,
                    icon: Icon(AppIcons.scan.of(context)),
                    style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(56), shape: const StadiumBorder()),
                    label: Text(_busy && !_manual ? context.t('common.loading') : context.t('mobile.scannen')),
                  ),
                ],
                if (_error != null && !_errorFromForm) ...[
                  const SizedBox(height: 16),
                  Text(_error!, style: context.type.bodyMedium?.copyWith(color: c.textDanger)),
                ],
                const SizedBox(height: 20),
                if (!_manual)
                  TextButton(onPressed: () => setState(() => _manual = true), child: Text(context.t('mobile.manuell')))
                else ...[
                  Divider(color: c.border),
                  const SizedBox(height: 16),
                  Text(context.t('mobile.verbindenLead'), style: context.type.bodyMedium),
                  const SizedBox(height: 16),
                  TextField(
                    controller: _address,
                    keyboardType: TextInputType.url,
                    autocorrect: false,
                    textInputAction: TextInputAction.next,
                    decoration: InputDecoration(labelText: context.t('mobile.adresse')),
                  ),
                  const SizedBox(height: 14),
                  TextField(
                    controller: _key,
                    obscureText: true,
                    autocorrect: false,
                    enableSuggestions: false,
                    decoration: InputDecoration(labelText: context.t('mobile.schluessel'), hintText: 'covey_…'),
                    onSubmitted: (_) => _busy ? null : _connect(),
                  ),
                  const SizedBox(height: 8),
                  Text(context.t('mobile.schluesselHinweis'), style: context.type.bodySmall),
                  if (_error != null && _errorFromForm) ...[
                    const SizedBox(height: 16),
                    Text(_error!, style: context.type.bodyMedium?.copyWith(color: c.textDanger)),
                  ],
                  const SizedBox(height: 20),
                  OutlinedButton(
                    onPressed: _busy ? null : _connect,
                    style: OutlinedButton.styleFrom(
                      minimumSize: const Size.fromHeight(52),
                      shape: const StadiumBorder(),
                    ),
                    child: Text(_busy ? context.t('common.loading') : context.t('mobile.verbinden')),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
