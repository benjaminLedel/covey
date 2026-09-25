import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../models.dart';
import '../theme.dart';
import 'colleagues.dart';
import 'thread.dart';
import 'waiting.dart';

/// The two lists the app opens on: what waits — the home, the screen a
/// notification will one day open — and the colleagues. The thread is pushed
/// on top of either.
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key, required this.api, required this.onDisconnect});

  final CoveyApi api;
  final VoidCallback onDisconnect;

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  int _tab = 0;
  Me? _me;
  Object? _meError;

  /// The thread open beside the list on a wide window (#334). On a phone the
  /// thread is pushed instead, and this stays null.
  ({String id, String name})? _open;

  /// From this width the list and the thread stand side by side: a desktop
  /// window, a tablet in landscape.
  static const wide = 840.0;

  @override
  void initState() {
    super.initState();
    _loadMe();
  }

  Future<void> _loadMe() async {
    try {
      final me = await widget.api.me();
      if (mounted) setState(() => _me = me);
    } catch (e) {
      if (mounted) setState(() => _meError = e);
    }
  }

  void _openThread(String agentId, String name) {
    if (MediaQuery.sizeOf(context).width >= wide) {
      setState(() => _open = (id: agentId, name: name));
      return;
    }
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ThreadScreen(api: widget.api, agentId: agentId, agentName: name, me: _me!),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final me = _me;
    if (me == null) {
      return Scaffold(
        body: Center(
          child: _meError == null
              ? Text(context.t('common.loading'))
              : _Failure(
                  error: _meError!,
                  onRetry: () {
                    setState(() => _meError = null);
                    _loadMe();
                  },
                  onDisconnect: widget.onDisconnect,
                ),
        ),
      );
    }
    final isWide = MediaQuery.sizeOf(context).width >= wide;
    final lists = IndexedStack(
      index: _tab,
      children: [
        WaitingScreen(api: widget.api, me: me, onOpen: _openThread),
        ColleaguesScreen(api: widget.api, onOpen: _openThread),
      ],
    );
    final destinations = [
      (icon: Icons.inbox_outlined, label: context.t('team.wartet')),
      (icon: Icons.people_outline, label: context.t('team.kollegen')),
    ];
    final open = _open;
    return Scaffold(
      appBar: AppBar(
        title: Text(_tab == 0 ? context.t('team.wartet') : context.t('team.kollegen')),
        actions: [
          PopupMenuButton<String>(
            tooltip: context.t('nav.userMenu'),
            onSelected: (v) => v == 'disconnect' ? widget.onDisconnect() : null,
            itemBuilder: (context) => [
              PopupMenuItem(
                enabled: false,
                child: Text(
                  '${me.displayName}\n${widget.api.base.host}',
                  style: TextStyle(color: context.colors.textMuted),
                ),
              ),
              PopupMenuItem(value: 'disconnect', child: Text(context.t('mobile.trennen'))),
            ],
          ),
        ],
      ),
      body: Column(
        children: [
          if (!me.teamSurface) _Notice(text: context.t('mobile.teamAus')),
          Expanded(
            child: !isWide
                ? lists
                // A wide window: the rail, the list, and the thread beside it —
                // the same three surfaces, not a second console (#334).
                : Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      NavigationRail(
                        selectedIndex: _tab,
                        onDestinationSelected: (i) => setState(() => _tab = i),
                        labelType: NavigationRailLabelType.all,
                        backgroundColor: context.colors.surface2,
                        destinations: [
                          for (final d in destinations)
                            NavigationRailDestination(icon: Icon(d.icon), label: Text(d.label)),
                        ],
                      ),
                      const VerticalDivider(width: 1),
                      SizedBox(width: 360, child: lists),
                      const VerticalDivider(width: 1),
                      Expanded(
                        child: open == null
                            ? Center(
                                child: Text(
                                  context.t('mobile.waehlen'),
                                  style: TextStyle(color: context.colors.textMuted),
                                ),
                              )
                            : ThreadScreen(
                                key: ValueKey(open.id),
                                api: widget.api,
                                agentId: open.id,
                                agentName: open.name,
                                me: me,
                              ),
                      ),
                    ],
                  ),
          ),
        ],
      ),
      bottomNavigationBar: isWide
          ? null
          : NavigationBar(
              selectedIndex: _tab,
              onDestinationSelected: (i) => setState(() => _tab = i),
              destinations: [for (final d in destinations) NavigationDestination(icon: Icon(d.icon), label: d.label)],
            ),
    );
  }
}

class _Notice extends StatelessWidget {
  const _Notice({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Container(
      width: double.infinity,
      color: c.bgWait,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Text(text, style: TextStyle(color: c.textWait, fontSize: 13, height: 1.35)),
    );
  }
}

class _Failure extends StatelessWidget {
  const _Failure({required this.error, required this.onRetry, required this.onDisconnect});

  final Object error;
  final VoidCallback onRetry;
  final VoidCallback onDisconnect;

  @override
  Widget build(BuildContext context) {
    // A key that no longer works — revoked in the web interface — is the one
    // failure that retrying cannot fix.
    final revoked = error is ApiException && (error as ApiException).status == 401;
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            revoked ? context.t('mobile.schluesselAbgelehnt') : context.t('mobile.fehler', args: {'error': '$error'}),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 16),
          if (!revoked) OutlinedButton(onPressed: onRetry, child: Text(context.t('mobile.erneut'))),
          TextButton(onPressed: onDisconnect, child: Text(context.t('mobile.trennen'))),
        ],
      ),
    );
  }
}

/// A list that loads, can be pulled to refresh, and says what went wrong.
/// Shared by the two tabs.
class LoadingList<T> extends StatefulWidget {
  const LoadingList({super.key, required this.load, required this.build});

  final Future<T> Function() load;
  final List<Widget> Function(BuildContext context, T data) build;

  @override
  State<LoadingList<T>> createState() => _LoadingListState<T>();
}

class _LoadingListState<T> extends State<LoadingList<T>> {
  T? _data;
  Object? _error;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    try {
      final d = await widget.load();
      if (mounted) {
        setState(() {
          _data = d;
          _error = null;
        });
      }
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final data = _data;
    final children = <Widget>[
      if (_error != null)
        Padding(
          padding: const EdgeInsets.all(16),
          child: Text(
            context.t('mobile.fehler', args: {'error': '$_error'}),
            style: TextStyle(color: context.colors.textDanger),
          ),
        ),
      if (data == null && _error == null)
        Padding(padding: const EdgeInsets.all(16), child: Text(context.t('common.loading'))),
      if (data != null) ...widget.build(context, data),
    ];
    return RefreshIndicator(
      onRefresh: _refresh,
      child: ListView(physics: const AlwaysScrollableScrollPhysics(), children: children),
    );
  }
}
