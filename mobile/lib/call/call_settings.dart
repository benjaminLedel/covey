import 'dart:async';

import 'package:flutter/material.dart';

import '../chrome.dart';
import '../i18n.dart';
import '../theme.dart';
import '../ui.dart';
import 'call.dart';
import 'recording.dart';

/// A call's settings on this Mac (#498): the pause that ends a turn, how
/// long the person must speak to interrupt, how long a turn stands as
/// understood, and the diagnostics recording. How an agent sounds is not
/// set here: it is its covey voice's spoken voice at the organisation's
/// voice provider, set on the instance.
class CallSettingsScreen extends StatefulWidget {
  const CallSettingsScreen({super.key});

  @override
  State<CallSettingsScreen> createState() => _CallSettingsScreenState();
}

class _CallSettingsScreenState extends State<CallSettingsScreen> {
  (int, int)? _recorded;

  static const _pauses = [500, 700, 1000, 1500];
  static const _bargeIns = [200, 400, 800];
  static const _windows = [0, 1000, 1500, 3000];

  @override
  void initState() {
    super.initState();
    unawaited(_measure());
  }

  Future<void> _measure() async {
    if (CallSettings.record.value) {
      try {
        await (await CallRecording.open()).prune();
      } catch (_) {}
    }
    final r = await CallRecording.size();
    if (mounted) setState(() => _recorded = r);
  }

  String _seconds(BuildContext context, int ms) =>
      ms == 0 ? context.t('call.windowOff') : context.t('call.seconds', args: {'n': (ms / 1000).toStringAsFixed(1)});

  Future<void> _pick(String title, List<int> choices, int current, Future<void> Function(Duration) set) async {
    await showModalBottomSheet<void>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
                child: Text(title, style: context.type.titleMedium),
              ),
              InsetGroup(
                dividerIndent: 14,
                children: [
                  for (final ms in choices)
                    GroupRow(
                      title: _seconds(context, ms),
                      trailing: ms == current ? Icon(Icons.check_rounded, color: context.colors.textAccent) : null,
                      onTap: () async {
                        Navigator.pop(context);
                        await set(Duration(milliseconds: ms));
                        if (mounted) setState(() {});
                      },
                    ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final small = context.type.bodySmall?.copyWith(color: c.textMuted);
    final recorded = _recorded;
    return Scaffold(
      appBar: ChromeAppBar(title: Text(context.t('call.settingsTitle'))),
      body: ListView(
        padding: const EdgeInsets.only(bottom: 40),
        children: [
          SectionTitle(context.t('call.turns')),
          InsetGroup(
            dividerIndent: 14,
            children: [
              GroupRow(
                title: context.t('call.pause'),
                subtitle: _seconds(context, CallSettings.pause.value.inMilliseconds),
                tabularSubtitle: true,
                onTap: () => _pick(
                  context.t('call.pause'),
                  _pauses,
                  CallSettings.pause.value.inMilliseconds,
                  CallSettings.setPause,
                ),
              ),
              GroupRow(
                title: context.t('call.bargeIn'),
                subtitle: _seconds(context, CallSettings.bargeIn.value.inMilliseconds),
                tabularSubtitle: true,
                onTap: () => _pick(
                  context.t('call.bargeIn'),
                  _bargeIns,
                  CallSettings.bargeIn.value.inMilliseconds,
                  CallSettings.setBargeIn,
                ),
              ),
              GroupRow(
                title: context.t('call.window'),
                subtitle: _seconds(context, CallSettings.window.value.inMilliseconds),
                tabularSubtitle: true,
                onTap: () => _pick(
                  context.t('call.window'),
                  _windows,
                  CallSettings.window.value.inMilliseconds,
                  CallSettings.setWindow,
                ),
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(context.t('call.turnsHint'), style: small),
          ),

          SectionTitle(context.t('call.diagnostics')),
          InsetGroup(
            dividerIndent: 14,
            children: [
              GroupRow(
                title: context.t('call.record'),
                trailing: ValueListenableBuilder<bool>(
                  valueListenable: CallSettings.record,
                  builder: (context, on, _) => Switch.adaptive(
                    value: on,
                    onChanged: (v) async {
                      await CallSettings.setRecord(v);
                      await _measure();
                    },
                  ),
                ),
              ),
              GroupRow(
                title: context.t('call.recordDelete'),
                subtitle: recorded == null
                    ? null
                    : context.t(
                        'call.recordSize',
                        args: {'turns': recorded.$2, 'mb': (recorded.$1 / 1000000).toStringAsFixed(1)},
                      ),
                tabularSubtitle: true,
                onTap: (recorded?.$1 ?? 0) > 0
                    ? () async {
                        await CallRecording.deleteAll();
                        await _measure();
                      }
                    : null,
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: Text(context.t('call.recordHint'), style: small),
          ),
        ],
      ),
    );
  }
}
