import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../activity.dart';
import '../anywhere.dart';
import '../api.dart';
import '../chrome.dart';
import '../face.dart';
import '../i18n.dart';
import '../icons.dart';
import '../photo.dart';
import '../push.dart';
import '../mark.dart';
import '../models.dart';
import '../theme.dart';
import '../ui.dart';
import 'notes.dart';
import 'review.dart';
import 'suggestions.dart';
import 'settings.dart';
import 'team_space.dart';
import 'thread.dart';

/// Two spaces — Team and Notes — and a floating capsule between them, with
/// the + for capturing beside it (the iOS 26 tab bar and its accessory).
/// Without the team surface only Notes remains: the app is a notetaker then,
/// not a notice about a setting (#336). On a wide window the capsule becomes
/// a sidebar, the way Arc keeps its spaces, and what is opened stands beside
/// the list (#334).
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key, required this.api, required this.onDisconnect});

  final CoveyApi api;
  final VoidCallback onDisconnect;

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  /// How the notes are shown (#373); on a wide window the table and the
  /// board take the width.
  NotesView _notesView = NotesView.list;

  int _space = 0;
  Me? _me;
  Object? _meError;
  final _notes = GlobalKey<NotesScreenState>();

  /// What stands beside the list on a wide window — a thread or a note. On a
  /// phone it is pushed instead, and this stays null.
  Widget? _detail;

  /// From this width the spaces become a sidebar and the detail stands beside
  /// the list: a desktop window, a tablet in landscape.
  static const wide = 920.0;

  @override
  void initState() {
    super.initState();
    _loadMe();
    // Dictate anywhere (#355) follows the instance the app is signed in to.
    if (DictateAnywhere.supported) unawaited(DictateAnywhere.instance.attach(widget.api));
    // The activity log (#363) too; its menu-bar item can ask for the review.
    if (ActivityRecorder.supported) {
      ActivityRecorder.instance.onReviewRequested = _reviewFromMenu;
      unawaited(ActivityRecorder.instance.attach(widget.api));
    }
  }

  /// The day's review, asked for in the menu bar: the window comes forward
  /// and opens the note.
  Future<void> _reviewFromMenu() async {
    final messenger = ScaffoldMessenger.of(context);
    final lang = Strings.of(context).language;
    final title = context.t(
      'mobile.aktivRueckblickTitel',
      args: {'date': DateFormat.yMMMMd(lang).format(DateTime.now())},
    );
    await MacChrome.activate();
    try {
      final note = await ActivityRecorder.instance.review(lang: lang, title: title);
      if (!mounted) return;
      _openNote(note, false, () => _notes.currentState?.reload());
      _notes.currentState?.reload();
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    // The panel speaks the app's language.
    ActivityRecorder.instance.strings = {
      'title': context.t('mobile.aktivMenue'),
      'paused': context.t('mobile.aktivPausiert'),
      'pause': context.t('mobile.aktivPause'),
      'resume': context.t('mobile.aktivFortsetzen'),
      'review': context.t('mobile.aktivRueckblick'),
      'off': context.t('mobile.aktivAus'),
    };
    DictateAnywhere.instance
      ..listening = context.t('mobile.hoertZu')
      ..cleaning = context.t('mobile.raeumtAuf')
      ..loading = context.t('mobile.sprachmodellLaedt', args: {'pct': '…'});
  }

  @override
  void dispose() {
    if (DictateAnywhere.supported) unawaited(DictateAnywhere.instance.detach());
    if (ActivityRecorder.supported) unawaited(ActivityRecorder.instance.detach());
    super.dispose();
  }

  Future<void> _loadMe() async {
    try {
      final me = await widget.api.me();
      if (mounted) setState(() => _me = me);
      // Notifications need a conversation to be about (#379).
      if (mounted && me.teamSurface) unawaited(PushNotices.instance.start(widget.api, Strings.of(context)));
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

  void _openThread(String agentId, String name, String slug, FaceState state) => _show(
    ThreadScreen(
      key: ValueKey('thread:$agentId'),
      api: widget.api,
      agentId: agentId,
      agentName: name,
      agentSlug: slug,
      faceState: state,
      me: _me!,
    ),
  );

  void _openNote(Note note, bool canSummarize, VoidCallback changed) => _show(
    NotePage(
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

  /// The +: a sheet with the two ways to capture. What is captured lands in
  /// Notes, and the app goes there to show it.
  Future<void> _capture(int notesIndex) async {
    final kind = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.only(bottom: 16),
          child: InsetGroup(
            children: [
              GroupRow(
                leading: const KindMark(kind: 'text'),
                title: context.t('mobile.notizNeu'),
                subtitle: context.t('mobile.notizHinweis'),
                onTap: () => Navigator.pop(context, 'note'),
              ),
              GroupRow(
                leading: const KindMark(kind: 'meeting'),
                title: context.t('mobile.meetingNeu'),
                subtitle: context.t('mobile.meetingHinweis'),
                onTap: () => Navigator.pop(context, 'meeting'),
              ),
              // The day's review (#368), from what the Mac recorded — also on
              // the phone, since the activity lives on the instance.
              GroupRow(
                leading: Icon(AppIcons.summary.of(context), color: context.colors.textAccent),
                title: context.t('mobile.rueckblick'),
                subtitle: context.t('mobile.rueckblickZeile'),
                onTap: () => Navigator.pop(context, 'review'),
              ),
              // What an agent could take over, from two weeks of activity
              // (#370).
              GroupRow(
                leading: Icon(AppIcons.team.of(context), color: context.colors.textAccent),
                title: context.t('mobile.vorschlaege'),
                subtitle: context.t('mobile.vorschlaegeZeile'),
                onTap: () => Navigator.pop(context, 'suggestions'),
              ),
            ],
          ),
        ),
      ),
    );
    if (kind == null || !mounted) return;
    setState(() => _space = notesIndex);
    void reload() => _notes.currentState?.reload();
    if (kind == 'suggestions') {
      await Navigator.of(context).push<void>(
        MaterialPageRoute(
          builder: (_) => SuggestionsScreen(api: widget.api, canHire: _me?.canWrite ?? false),
        ),
      );
      return;
    }
    if (kind == 'review') {
      await Navigator.of(context).push<void>(
        MaterialPageRoute(
          builder: (routeContext) => ReviewScreen(
            api: widget.api,
            onOpen: (note) {
              Navigator.of(routeContext).pop();
              reload();
              _openNote(note, _notes.currentState?.canSummarize ?? false, reload);
            },
          ),
        ),
      );
      return;
    }
    if (kind == 'note') {
      // A new note is the same page as an open one, empty; it comes into
      // being with its first words (#343).
      _show(NotePage(key: UniqueKey(), api: widget.api, onChanged: reload));
      return;
    }
    final saved = await Navigator.of(
      context,
    ).push<Note>(MaterialPageRoute(builder: (_) => MeetingScreen(api: widget.api)));
    if (saved == null || !mounted) return;
    setState(() => _space = notesIndex);
    await _notes.currentState?.reload();
    if (!mounted) return;
    _openNote(saved, _notes.currentState?.canSummarize ?? false, () => _notes.currentState?.reload());
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
    final account = _AccountButton(
      me: me,
      api: widget.api,
      onDisconnect: widget.onDisconnect,
      onChanged: (m) => setState(() => _me = m),
    );
    final actions = isWide ? const <Widget>[] : [account];
    final spaces = <({IconData icon, String label, Widget body})>[
      if (me.teamSurface)
        (
          icon: AppIcons.team.of(context),
          label: context.t('team.workspace'),
          body: TeamSpace(
            api: widget.api,
            me: me,
            onOpen: _openThread,
            actions: actions,
            bottomClearance: isWide ? 24 : capsuleClearance,
            compact: isWide,
          ),
        ),
      (
        icon: AppIcons.notes.of(context),
        label: context.t('mobile.notizen'),
        body: NotesScreen(
          key: _notes,
          api: widget.api,
          onOpen: _openNote,
          actions: actions,
          bottomClearance: isWide ? 24 : capsuleClearance,
          compact: isWide,
          onViewChanged: (v) => setState(() => _notesView = v),
        ),
      ),
    ];
    final space = _space.clamp(0, spaces.length - 1);
    final notesIndex = spaces.length - 1;
    final body = IndexedStack(index: space, children: [for (final s in spaces) s.body]);
    final capsule = [for (final s in spaces) (icon: s.icon, label: s.label)];

    if (isWide) {
      return Scaffold(
        body: Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _Sidebar(
              // The team carries the unread count (#393).
              spaces: [
                for (var i = 0; i < spaces.length; i++)
                  (icon: spaces[i].icon, label: spaces[i].label, badge: me.teamSurface && i == 0 ? unreadTotal : null),
              ],
              selected: space,
              onSelect: (i) => setState(() => _space = i),
              onAdd: () => _capture(notesIndex),
              account: account,
            ),
            const VerticalDivider(width: 0.6),
            // The panes right of the sidebar are clear of the traffic lights.
            // The table and the board of the notes take the width (#373); an
            // open note stands beside them.
            if (space == notesIndex && _notesView != NotesView.list) ...[
              Expanded(child: LightsInset(left: 0, child: body)),
              if (_detail != null) ...[
                const VerticalDivider(width: 0.6),
                SizedBox(width: 560, child: LightsInset(left: 0, child: _detail!)),
              ],
            ] else ...[
              // The list gives way before the thread does (#376).
              SizedBox(
                width: ((MediaQuery.sizeOf(context).width - 232) * 0.4).clamp(280, 400),
                child: LightsInset(left: 0, child: body),
              ),
              const VerticalDivider(width: 0.6),
              Expanded(
                child: LightsInset(
                  left: 0,
                  child:
                      _detail ??
                      Center(
                        child: Text(
                          context.t('mobile.waehlen'),
                          style: context.type.bodyLarge?.copyWith(color: context.colors.textMuted),
                        ),
                      ),
                ),
              ),
            ],
          ],
        ),
      );
    }

    return Scaffold(
      body: Stack(
        children: [
          body,
          Positioned(
            left: 16,
            right: 16,
            bottom: 0,
            child: SafeArea(
              minimum: const EdgeInsets.only(bottom: 12),
              child: SpaceCapsule(
                spaces: capsule,
                selected: space,
                onSelect: (i) => setState(() => _space = i),
                onAdd: () => _capture(notesIndex),
                addLabel: context.t('mobile.neu'),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// The person, as initials in a circle at the top right: the way into the
/// settings (#349) — who is signed in where, speech recognition, the way out.
class _AccountButton extends StatelessWidget {
  const _AccountButton({required this.me, required this.api, required this.onDisconnect, required this.onChanged});

  final Me me;
  final CoveyApi api;
  final VoidCallback onDisconnect;

  /// The seat after a change made in the settings — a new photo (#377).
  final ValueChanged<Me> onChanged;

  void _open(BuildContext context) => Navigator.of(context).push(
    MaterialPageRoute<void>(
      builder: (_) => SettingsScreen(api: api, me: me, onDisconnect: onDisconnect, onChanged: onChanged),
    ),
  );

  @override
  Widget build(BuildContext context) {
    // A 36 pt circle in a 44 pt target (ios.md): the photo, or the monogram.
    return Semantics(
      button: true,
      label: context.t('nav.userMenu'),
      child: InkResponse(
        onTap: () => _open(context),
        radius: 24,
        child: SizedBox.square(
          dimension: 44,
          child: Center(
            child: PersonPhoto(api: api, humanId: me.id, photoId: me.photoId, name: me.displayName),
          ),
        ),
      ),
    );
  }
}

/// The spaces on a wide window: the mark, the spaces as rows, the + as a
/// full-width action, the person at the foot.
class _Sidebar extends StatelessWidget {
  const _Sidebar({
    required this.spaces,
    required this.selected,
    required this.onSelect,
    required this.onAdd,
    required this.account,
  });

  final List<({IconData icon, String label, ValueListenable<int>? badge})> spaces;
  final int selected;
  final ValueChanged<int> onSelect;
  final VoidCallback onAdd;
  final Widget account;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    // As narrow as the window allows (#393): on macOS the traffic lights sit
    // in the rail's top band, so it is at least as wide as they are.
    final width = MacChrome.active ? math.max(72.0, MacChrome.lightsRight) : 72.0;
    return Container(
      width: width,
      color: c.surface1,
      child: SafeArea(
        right: false,
        child: Padding(
          padding: EdgeInsets.fromLTRB(0, MacChrome.active ? 0 : 14, 0, 14),
          child: Column(
            children: [
              // The traffic lights' band (#356): it moves the window, as the
              // title bar did.
              if (MacChrome.active)
                WindowDrag(
                  child: SizedBox(height: MacChrome.height, width: width),
                ),
              const Padding(padding: EdgeInsets.only(bottom: 18), child: CoveyMark(size: 30)),
              for (var i = 0; i < spaces.length; i++)
                Padding(
                  padding: const EdgeInsets.only(bottom: 6),
                  child: _RailItem(
                    icon: spaces[i].icon,
                    label: spaces[i].label,
                    selected: i == selected,
                    badge: spaces[i].badge,
                    onTap: () => onSelect(i),
                  ),
                ),
              const SizedBox(height: 6),
              _RailItem(
                icon: AppIcons.add.of(context),
                label: context.t('mobile.neu'),
                selected: false,
                accent: true,
                onTap: onAdd,
              ),
              const Spacer(),
              account,
            ],
          ),
        ),
      ),
    );
  }
}

/// One place in the rail: an icon in a rounded square, its name as tooltip
/// and for screen readers, and a count where there is one.
class _RailItem extends StatelessWidget {
  const _RailItem({
    required this.icon,
    required this.label,
    required this.selected,
    required this.onTap,
    this.badge,
    this.accent = false,
  });

  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onTap;
  final ValueListenable<int>? badge;
  final bool accent;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final square = Material(
      color: selected ? c.surface2 : (accent ? c.bgAccent : Colors.transparent),
      elevation: selected ? 0.5 : 0,
      shadowColor: Colors.black26,
      borderRadius: BorderRadius.circular(12),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: onTap,
        child: SizedBox.square(
          dimension: 44,
          child: Icon(icon, size: 22, color: accent ? c.textAccent : (selected ? c.textPrimary : c.textMuted)),
        ),
      ),
    );
    final b = badge;
    return Tooltip(
      message: label,
      waitDuration: const Duration(milliseconds: 400),
      child: Semantics(
        button: true,
        selected: selected,
        label: label,
        excludeSemantics: true,
        child: b == null
            ? square
            : ValueListenableBuilder<int>(
                valueListenable: b,
                builder: (context, n, child) => Stack(
                  clipBehavior: Clip.none,
                  children: [
                    child!,
                    if (n > 0)
                      Positioned(
                        top: -3,
                        right: -3,
                        child: Container(
                          constraints: const BoxConstraints(minWidth: 18),
                          height: 18,
                          padding: const EdgeInsets.symmetric(horizontal: 5),
                          alignment: Alignment.center,
                          decoration: BoxDecoration(
                            color: c.textPrimary,
                            borderRadius: BorderRadius.circular(9),
                            border: Border.all(color: c.surface1, width: 2),
                          ),
                          child: Text(
                            n > 99 ? '99+' : '$n',
                            style: context.type.labelSmall?.copyWith(color: c.surface2, fontSize: 10, height: 1),
                          ),
                        ),
                      ),
                  ],
                ),
                child: square,
              ),
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
            style: context.type.bodyLarge,
          ),
          const SizedBox(height: 16),
          if (!revoked) OutlinedButton(onPressed: onRetry, child: Text(context.t('mobile.erneut'))),
          TextButton(onPressed: onDisconnect, child: Text(context.t('mobile.trennen'))),
        ],
      ),
    );
  }
}
