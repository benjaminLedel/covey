import { Link } from "react-router-dom";
import type { Human } from "../api";

// Gemeinsame Bausteine rund um Personen: Initialen-Avatar und der
// einheitliche Link auf die Profil-Seite (/people/{id}). Überall verwenden,
// wo ein Mensch angezeigt wird — Organigramm, Benutzerliste, Profil-Seite.

export const initials = (name: string) =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .map((p) => p[0])
    .slice(0, 2)
    .join("")
    .toUpperCase() || "?";

export function Avatar({ name, size = 32, human }: { name: string; size?: number; human?: boolean }) {
  return (
    <div className={`avatar${human ? " hum" : ""}`} style={{ width: size, height: size, fontSize: Math.round(size * 0.38) }}>
      {initials(name)}
    </div>
  );
}

export function PersonLink({ human }: { human: Pick<Human, "id" | "display_name"> }) {
  return (
    <Link to={`/people/${human.id}`} style={{ color: "inherit" }} title="Profil-Seite öffnen">
      {human.display_name}
    </Link>
  );
}
