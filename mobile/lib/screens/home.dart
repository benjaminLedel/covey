import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../models.dart';
import '../theme.dart';
import 'colleagues.dart';
import 'notes.dart';
import 'thread.dart';
import 'waiting.dart';

/// The lists the app opens on: what waits — the home, the screen a
/// notification will one day open — the colleagues, and the person's own
/// notes (#336). Without the team surface only the notes remain: the app is a
/// notetaker then, not a notice about a setting.
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

  /// What stands beside the list on a wide window (#334) — a thread or a
  /// note. On a phone it is pushed instead, and this stays null.
  Widget? _detail;

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

  void _show(Widget detail) {
    if (MediaQuery.sizeOf(context).width >= wide) {
      setState(() => _detail = detail);
      return;
    }
    Navigator.of(context).push(MaterialPageRoute(builder: (_) => detail));
  }

  void _openThread(String agentId, String name) => _show(
    ThreadScreen(key: ValueKey('thread:$agentId'), api: widget.api, agentId: agentId, agentName: name, me: _me!),
  );

  void _openNote(Note note, bool canSummarize, VoidCallback changed) => _show(
    NoteScreen(
      key: ValueKey('note:${note.id}'),
      api: widget.api,
      note: note,
      canSummarize: canSummarize,
      onChanged: () {
        changed();
        // A note deleted beside the list leaves an empty pane, not a ghost.
        if (_detail?.key == ValueKey('note:${note.id}')) setState(() => _detail = null);
      },
    ),
  );

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
    final tabs = [
      if (me.teamSurface) ...[
        (
          icon: Icons.inbox_outlined,
          label: context.t('team.wartet'),
          body: WaitingScreen(api: widget.api, me: me, onOpen: _openThread) as Widget,
        ),
        (
          icon: Icons.people_outline,
          label: context.t('team.kollegen'),
          body: ColleaguesScreen(api: widget.api, onOpen: _openThread) as Widget,
        ),
      ],
      (
        icon: Icons.edit_note,
        label: context.t('mobile.notizen'),
        body: NotesScreen(api: widget.api, onOpen: _openNote) as Widget,
      ),
    ];
    final tab = _tab.clamp(0, tabs.length - 1);
    final lists = IndexedStack(index: tab, children: [for (final t in tabs) t.body]);
    final detail = _detail;
    return Scaffold(
      appBar: AppBar(
        title: Text(tabs[tab].label),
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
              // Why there are no colleagues: one quiet line where somebody
              // looks for it, instead of a banner over every screen.
              if (!me.teamSurface)
                PopupMenuItem(
                  enabled: false,
                  child: Text(
                    context.t('mobile.nurNotizen'),
                    style: TextStyle(color: context.colors.textMuted, fontSize: 12.5),
                  ),
                ),
              PopupMenuItem(value: 'disconnect', child: Text(context.t('mobile.trennen'))),
            ],
          ),
        ],
      ),
      body: !isWide
          ? lists
          // A wide window: the rail, the list, and what is open beside it —
          // the same surfaces, not a second console (#334).
          : Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (tabs.length > 1) ...[
                  NavigationRail(
                    selectedIndex: tab,
                    onDestinationSelected: (i) => setState(() => _tab = i),
                    labelType: NavigationRailLabelType.all,
                    backgroundColor: context.colors.surface2,
                    destinations: [
                      for (final d in tabs) NavigationRailDestination(icon: Icon(d.icon), label: Text(d.label)),
                    ],
                  ),
                  const VerticalDivider(width: 1),
                ],
                SizedBox(width: 380, child: lists),
                const VerticalDivider(width: 1),
                Expanded(
                  child:
                      detail ??
                      Center(
                        child: Text(context.t('mobile.waehlen'), style: TextStyle(color: context.colors.textMuted)),
                      ),
                ),
              ],
            ),
      // One tab needs no bar.
      bottomNavigationBar: isWide || tabs.length < 2
          ? null
          : NavigationBar(
              selectedIndex: tab,
              onDestinationSelected: (i) => setState(() => _tab = i),
              destinations: [for (final d in tabs) NavigationDestination(icon: Icon(d.icon), label: d.label)],
            ),
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
