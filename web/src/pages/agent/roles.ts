// Wer darf was am Agenten. Eine Stelle, weil die Antwort in jedem Reiter
// dieselbe sein muss — vorher standen die vier Zeilen oben in einer Datei mit
// allem anderen und wanderten beim Aufteilen sonst in Kopien auseinander.
export const canManage = (role: string) => role === "platform_admin" || role === "agent_owner";
export const canKill = (role: string) => canManage(role) || role === "security";
export const canSecrets = (role: string) => role === "platform_admin" || role === "security";
// Der Arbeitsplatz (Home des Agenten): lesen dürfen ihn seine Verwalter und
// Security — wer einen Agenten untersucht, muss sehen, was bei ihm liegt.
export const canFiles = (role: string) => canManage(role) || role === "security";
