// Who may do what to the agent. One place, because the answer in every tab
// must be the same — before, the four lines stood at the top of a file with
// everything else and otherwise drifted apart into copies when splitting.
export const canManage = (role: string) => role === "org_admin" || role === "agent_owner";
export const canKill = (role: string) => canManage(role) || role === "security";
export const canSecrets = (role: string) => role === "org_admin" || role === "security";
// The workstation (the agent's home): its managers and security may read
// it — anyone investigating an agent must see what lies in it.
export const canFiles = (role: string) => canManage(role) || role === "security";
// The work record (spec/21): it follows the recordings, not the cost figures.
// A sum says what was spent; a record says how someone worked — controlling
// therefore does not see it, the auditor does, read-only.
export const canRecord = (role: string) =>
  canManage(role) || role === "security" || role === "auditor";
