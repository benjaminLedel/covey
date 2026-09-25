import 'package:flutter/material.dart';

import '../api.dart';
import '../i18n.dart';
import '../models.dart';
import '../theme.dart';
import 'home.dart';

/// The colleagues, grouped by department the way the web's team shell holds
/// them. Applicants are left out: a draft is not somebody one writes to.
class ColleaguesScreen extends StatelessWidget {
  const ColleaguesScreen({super.key, required this.api, required this.onOpen});

  final CoveyApi api;
  final void Function(String agentId, String name) onOpen;

  Future<({List<Agent> agents, List<Department> departments})> _load() async {
    final r = await Future.wait([api.agents(), api.departments()]);
    return (agents: r[0] as List<Agent>, departments: r[1] as List<Department>);
  }

  @override
  Widget build(BuildContext context) {
    return LoadingList(
      load: _load,
      build: (context, data) {
        final colleagues = data.agents.where((a) => !a.isApplicant).toList()
          ..sort((a, b) => a.displayName.toLowerCase().compareTo(b.displayName.toLowerCase()));
        if (colleagues.isEmpty) {
          return [Padding(padding: const EdgeInsets.all(16), child: Text(context.t('chat.noAgents')))];
        }
        final known = {for (final d in data.departments) d.id};
        final groups = [
          for (final d in data.departments)
            (name: d.name, members: colleagues.where((a) => a.departmentId == d.id).toList()),
          (
            name: context.t('team.ohneAbteilung'),
            members: colleagues.where((a) => a.departmentId == null || !known.contains(a.departmentId)).toList(),
          ),
        ].where((g) => g.members.isNotEmpty);
        return [
          for (final g in groups) ...[
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
              child: Text(
                g.name.toUpperCase(),
                style: TextStyle(
                  fontSize: 11.5,
                  letterSpacing: 0.6,
                  fontWeight: FontWeight.w600,
                  color: context.colors.textMuted,
                ),
              ),
            ),
            for (final a in g.members)
              ListTile(
                title: Text(a.displayName),
                subtitle: Text(a.jobTitle.isEmpty ? a.slug : a.jobTitle, maxLines: 1, overflow: TextOverflow.ellipsis),
                // The state in words, not in a colour alone (spec/27).
                trailing: Text(
                  context.t('status.${a.killed ? 'killed' : a.status}'),
                  style: TextStyle(color: context.colors.textMuted, fontSize: 12.5),
                ),
                onTap: () => onOpen(a.id, a.displayName),
              ),
          ],
        ];
      },
    );
  }
}
