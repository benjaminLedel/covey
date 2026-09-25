import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../theme.dart';

/// Where is your covey?
///
/// The address and — the interim spec/27 names, not the design — an API key.
/// Three questions in order, each with its own answer when it fails: does
/// anything answer there, does it take the key, and which build it runs.
class ConnectScreen extends StatefulWidget {
  const ConnectScreen({super.key, required this.onConnected, this.makeApi = CoveyApi.new});

  final Future<void> Function(Uri instance, String key) onConnected;

  /// Swapped in tests, so the screen can be driven without a network.
  final CoveyApi Function(Uri base, String key) makeApi;

  @override
  State<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends State<ConnectScreen> {
  final _address = TextEditingController();
  final _key = TextEditingController();
  String? _error;
  bool _busy = false;

  @override
  void dispose() {
    _address.dispose();
    _key.dispose();
    super.dispose();
  }

  Future<void> _connect() async {
    final t = Strings.of(context).t;
    final Uri base;
    try {
      base = parseInstance(_address.text);
    } on FormatException {
      setState(() => _error = t('mobile.nurHttps'));
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
        child: ListView(
          padding: const EdgeInsets.fromLTRB(24, 48, 24, 24),
          children: [
            Text('covey', style: TextStyle(fontSize: 22, fontWeight: FontWeight.w600, color: c.textAccent)),
            const SizedBox(height: 32),
            Text(context.t('mobile.verbindenTitel'),
                style: const TextStyle(fontSize: 26, fontWeight: FontWeight.w600, height: 1.2)),
            const SizedBox(height: 12),
            Text(context.t('mobile.verbindenLead'), style: TextStyle(color: c.textMuted, height: 1.4)),
            const SizedBox(height: 28),
            TextField(
              controller: _address,
              keyboardType: TextInputType.url,
              autocorrect: false,
              textInputAction: TextInputAction.next,
              decoration: InputDecoration(labelText: context.t('mobile.adresse'), hintText: 'covey.example.org'),
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
            Text(context.t('mobile.schluesselHinweis'), style: TextStyle(color: c.textMuted, fontSize: 12.5, height: 1.4)),
            if (_error != null) ...[
              const SizedBox(height: 16),
              Text(_error!, style: TextStyle(color: c.textDanger)),
            ],
            const SizedBox(height: 24),
            FilledButton(
              onPressed: _busy ? null : _connect,
              style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(48)),
              child: Text(_busy ? context.t('common.loading') : context.t('mobile.verbinden')),
            ),
          ],
        ),
      ),
    );
  }
}
