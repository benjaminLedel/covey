import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../models.dart';
import '../theme.dart';
import 'home.dart';

/// What waits: the open points of the inbox, most urgent first.
///
/// Read-only in this slice. Deciding an approval from a phone is decision 6
/// of spec/27 (does it need a biometric confirmation first?), and a button
/// somebody hits on the way to another must not be the way it gets decided.
/// A point opens the thread of the agent it belongs to.
class WaitingScreen extends StatelessWidget {
  const WaitingScreen({super.key, required this.api, required this.me, required this.onOpen});

  final CoveyApi api;
  final Me me;
  final void Function(String agentId, String name) onOpen;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return LoadingList<InboxPage>(
      load: api.waiting,
      build: (context, page) => [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Text(
            context.t('team.wartetTitel', args: {'name': me.displayName.split(' ').first}),
            style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w600),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
          child: Text(
            page.pending == 0 ? context.t('team.wartetNichts') : context.t('team.wartetLead', count: page.pending),
            style: TextStyle(color: c.textMuted),
          ),
        ),
        for (final e in page.items)
          ListTile(
            title: Text(e.title, maxLines: 2, overflow: TextOverflow.ellipsis),
            subtitle: Text('${context.t('inbox.type.${e.type}')} · ${e.agentName}'),
            trailing: const Icon(Icons.chevron_right),
            onTap: e.agentId.isEmpty ? null : () => onOpen(e.agentId, e.agentName),
          ),
        if (page.items.isNotEmpty)
          Padding(
            padding: const EdgeInsets.all(16),
            child: Text(context.t('mobile.imWebEntscheiden'), style: TextStyle(color: c.textMuted, fontSize: 12.5)),
          ),
      ],
    );
  }
}
