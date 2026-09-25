import 'dart:ui' show FontFeature, ImageFilter;

import 'package:flutter/material.dart';

import 'icons.dart';
import 'mark.dart';
import 'theme.dart';

/// Room the floating capsule needs at the bottom of every space.
const capsuleClearance = 112.0;

/// A space: a large title with the iOS metrics — a 44 pt bar, the title
/// right under it, its subtitle a few points below — that hands over to a
/// small title in the bar once it has scrolled away. The Material large app
/// bar was tried first; its taller rows left the title floating ~30 pt low
/// and far from its own subtitle. [compact] drops the bar row where there is
/// nothing to put in it (the list pane of a wide window), so the title lines
/// up with the detail beside it.
class SpaceScroll extends StatefulWidget {
  const SpaceScroll({
    super.key,
    required this.title,
    required this.slivers,
    this.subtitle,
    this.actions = const [],
    this.onRefresh,
    this.bottomClearance = capsuleClearance,
    this.compact = false,
    this.search,
  });

  final String title;

  /// The search field under the title, where iOS puts it; it scrolls away
  /// with the title.
  final Widget? search;
  final String? subtitle;
  final List<Widget> actions;
  final List<Widget> slivers;
  final Future<void> Function()? onRefresh;
  final double bottomClearance;
  final bool compact;

  @override
  State<SpaceScroll> createState() => _SpaceScrollState();
}

class _SpaceScrollState extends State<SpaceScroll> {
  final _scroll = ScrollController();
  bool _scrolled = false;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(() {
      // The large title is gone once ~40 pt have scrolled under the bar.
      final scrolled = _scroll.offset > 40;
      if (scrolled != _scrolled) setState(() => _scrolled = scrolled);
    });
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final top = MediaQuery.paddingOf(context).top;
    final scroll = CustomScrollView(
      controller: _scroll,
      physics: const AlwaysScrollableScrollPhysics(parent: BouncingScrollPhysics()),
      slivers: [
        if (!widget.compact)
          SliverAppBar(
            pinned: true,
            toolbarHeight: 44,
            backgroundColor: _scrolled ? c.surface0 : c.surface0.withValues(alpha: 0),
            centerTitle: true,
            // The signet, top left, as the counterweight to the person on
            // the right: whichever space is open, this is covey.
            leadingWidth: 60,
            leading: const Padding(
              padding: EdgeInsets.only(left: 16),
              child: Align(alignment: Alignment.centerLeft, child: CoveyMark(size: 30)),
            ),
            automaticallyImplyLeading: false,
            title: AnimatedOpacity(
              opacity: _scrolled ? 1 : 0,
              duration: const Duration(milliseconds: 180),
              child: Text(widget.title, style: context.type.titleMedium),
            ),
            actions: [...widget.actions, const SizedBox(width: 16)],
            bottom: PreferredSize(
              preferredSize: const Size.fromHeight(0.6),
              child: AnimatedOpacity(
                opacity: _scrolled ? 1 : 0,
                duration: const Duration(milliseconds: 180),
                child: Divider(height: 0.6, color: c.hairline),
              ),
            ),
          ),
        SliverToBoxAdapter(
          child: Padding(
            padding: EdgeInsets.fromLTRB(16, widget.compact ? top + 18 : 2, 16, 0),
            child: Text(widget.title, style: context.type.headlineMedium),
          ),
        ),
        if (widget.subtitle != null)
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(16, 6, 16, 0),
              child: Text(widget.subtitle!, style: context.type.bodyLarge?.copyWith(color: c.textMuted)),
            ),
          ),
        if (widget.search != null)
          SliverToBoxAdapter(
            child: Padding(padding: const EdgeInsets.fromLTRB(16, 14, 16, 0), child: widget.search),
          ),
        ...widget.slivers,
        SliverToBoxAdapter(child: SizedBox(height: widget.bottomClearance)),
      ],
    );
    return widget.onRefresh == null
        ? scroll
        : RefreshIndicator(onRefresh: widget.onRefresh!, edgeOffset: top + 44, child: scroll);
  }
}

/// A section title inside a space: more room above than below, so it
/// belongs to what follows.
class SectionTitle extends StatelessWidget {
  const SectionTitle(this.text, {super.key, this.trailing});

  final String text;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 26, 16, 10),
    child: Row(
      children: [
        Expanded(child: Text(text, style: context.type.titleLarge)),
        ?trailing,
      ],
    ),
  );
}

/// An inset group: white rows on the neutral sheet, one rounded body,
/// hairlines between the rows that start where the text starts.
class InsetGroup extends StatelessWidget {
  const InsetGroup({super.key, required this.children, this.dividerIndent = 64});

  final List<Widget> children;
  final double dividerIndent;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(18),
        child: ColoredBox(
          color: c.surface2,
          child: Column(
            children: [
              for (var i = 0; i < children.length; i++) ...[
                if (i > 0) Divider(height: 0.6, indent: dividerIndent),
                children[i],
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// One row of an inset group.
class GroupRow extends StatelessWidget {
  const GroupRow({
    super.key,
    this.leading,
    required this.title,
    this.subtitle,
    this.trailing,
    this.onTap,
    this.tabularSubtitle = false,
  });

  final Widget? leading;
  final String title;
  final String? subtitle;
  final Widget? trailing;
  final VoidCallback? onTap;

  /// Times and durations in the subtitle line up in tabular figures.
  final bool tabularSubtitle;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: ConstrainedBox(
        constraints: const BoxConstraints(minHeight: 62),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          child: Row(
            children: [
              if (leading != null) ...[SizedBox(width: 36, child: Center(child: leading)), const SizedBox(width: 14)],
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(title, maxLines: 1, overflow: TextOverflow.ellipsis, style: context.type.titleMedium),
                    if (subtitle != null && subtitle!.isNotEmpty) ...[
                      const SizedBox(height: 2),
                      Text(
                        subtitle!,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: tabularSubtitle
                            ? context.type.bodyMedium?.copyWith(fontFeatures: const [FontFeature.tabularFigures()])
                            : context.type.bodyMedium,
                      ),
                    ],
                  ],
                ),
              ),
              if (trailing != null) ...[const SizedBox(width: 10), trailing!],
            ],
          ),
        ),
      ),
    );
  }
}

/// Frosted glass, only on controls that float over content moving under them
/// — the capsule, the +, the composer, the dictation button. The effect says
/// "above", not "pretty".
class Glass extends StatelessWidget {
  const Glass({super.key, required this.child, this.radius = 999});

  final Widget child;
  final double radius;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return DecoratedBox(
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(radius),
        boxShadow: [BoxShadow(color: c.shadow, blurRadius: 28, offset: const Offset(0, 10))],
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(radius),
        child: BackdropFilter(
          filter: ImageFilter.blur(sigmaX: 22, sigmaY: 22),
          child: DecoratedBox(
            decoration: BoxDecoration(
              color: c.glass,
              borderRadius: BorderRadius.circular(radius),
              border: Border.all(color: c.hairline, width: 0.8),
            ),
            child: child,
          ),
        ),
      ),
    );
  }
}

/// The floating capsule between the spaces, and the round + beside it — the
/// iOS 26 tab bar with its accessory. With a single space there is nothing
/// to switch between, and only the + remains.
class SpaceCapsule extends StatelessWidget {
  const SpaceCapsule({
    super.key,
    required this.spaces,
    required this.selected,
    required this.onSelect,
    required this.onAdd,
    required this.addLabel,
  });

  final List<({IconData icon, String label})> spaces;
  final int selected;
  final ValueChanged<int> onSelect;
  final VoidCallback onAdd;
  final String addLabel;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        if (spaces.length > 1) ...[
          Glass(
            child: Padding(
              padding: const EdgeInsets.all(5),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  for (var i = 0; i < spaces.length; i++)
                    _CapsuleItem(
                      icon: spaces[i].icon,
                      label: spaces[i].label,
                      selected: i == selected,
                      onTap: () => onSelect(i),
                    ),
                ],
              ),
            ),
          ),
          const SizedBox(width: 10),
        ],
        Semantics(
          button: true,
          label: addLabel,
          child: Glass(
            child: SizedBox.square(
              dimension: 58,
              child: IconButton(
                onPressed: onAdd,
                tooltip: addLabel,
                icon: Icon(AppIcons.add.of(context), size: 30, color: c.textAccent),
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _CapsuleItem extends StatelessWidget {
  const _CapsuleItem({required this.icon, required this.label, required this.selected, required this.onTap});

  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final fg = selected ? c.textPrimary : c.textMuted;
    return Semantics(
      selected: selected,
      button: true,
      child: GestureDetector(
        onTap: onTap,
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 260),
          curve: Curves.easeOutCubic,
          height: 48,
          padding: const EdgeInsets.symmetric(horizontal: 18),
          decoration: BoxDecoration(
            color: selected ? c.textPrimary.withValues(alpha: 0.08) : Colors.transparent,
            borderRadius: BorderRadius.circular(999),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 21, color: fg),
              const SizedBox(width: 8),
              Text(label, style: context.type.labelLarge?.copyWith(color: fg)),
            ],
          ),
        ),
      ),
    );
  }
}

/// A quiet empty state: a sentence, not an illustration.
class EmptyNote extends StatelessWidget {
  const EmptyNote(this.text, {super.key});

  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
    child: Text(text, style: context.type.bodyLarge?.copyWith(color: context.colors.textMuted)),
  );
}

/// A search field: a quiet filled capsule, the magnifier, a clear button once
/// there is something to clear.
class SearchField extends StatefulWidget {
  const SearchField({super.key, required this.hint, required this.onChanged});

  final String hint;
  final ValueChanged<String> onChanged;

  @override
  State<SearchField> createState() => _SearchFieldState();
}

class _SearchFieldState extends State<SearchField> {
  final _text = TextEditingController();

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return TextField(
      controller: _text,
      onChanged: (v) {
        setState(() {});
        widget.onChanged(v);
      },
      textInputAction: TextInputAction.search,
      style: context.type.bodyLarge,
      decoration: InputDecoration(
        isDense: true,
        hintText: widget.hint,
        filled: true,
        fillColor: c.textPrimary.withValues(alpha: 0.06),
        prefixIcon: Icon(AppIcons.search.of(context), size: 20, color: c.textMuted),
        suffixIcon: _text.text.isEmpty
            ? null
            : IconButton(
                icon: Icon(AppIcons.clear.of(context), size: 18, color: c.textMuted),
                onPressed: () {
                  _text.clear();
                  setState(() {});
                  widget.onChanged('');
                },
              ),
        contentPadding: const EdgeInsets.symmetric(vertical: 11),
        border: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide.none),
        enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide.none),
        focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide.none),
      ),
    );
  }
}
