// Wer darf was am Agenten. Eine Stelle, weil die Antwort in jedem Reiter
// dieselbe sein muss — vorher standen die vier Zeilen oben in einer Datei mit
// allem anderen und wanderten beim Aufteilen sonst in Kopien auseinander.
export const canManage = (role: string) => role === "platform_admin" || role === "agent_owner";
export const canKill = (role: string) => canManage(role) || role === "security";
export const canSecrets = (role: string) => role === "platform_admin" || role === "security";
// Der Arbeitsplatz (Home des Agenten): lesen dürfen ihn seine Verwalter und
// Security — wer einen Agenten untersucht, muss sehen, was bei ihm liegt.
export const canFiles = (role: string) => canManage(role) || role === "security";
// Die Arbeitsakte (spec/21): sie folgt den Recordings, nicht den Kostenzahlen.
// Eine Summe sagt, was ausgegeben wurde; eine Akte sagt, wie jemand gearbeitet
// hat — Controlling sieht sie deshalb nicht, der Auditor lesend schon.
export const canRecord = (role: string) =>
  canManage(role) || role === "security" || role === "auditor";
