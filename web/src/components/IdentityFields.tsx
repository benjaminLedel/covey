import { useQuery } from "@tanstack/react-query";
import { api, type TargetPlugin } from "../api";

// Editor für die Plattform-Kennungen eines Mitarbeiters (identities:
// system → kennung). Die Plattform-Liste kommt aus den Zielsystem-Plugins
// der Organisation — keine hartkodierte Liste; ein neues Plugin bekommt
// automatisch ein Eingabefeld. Kennungen zu inzwischen entfernten Systemen
// bleiben editierbar, damit sie sich leeren (= löschen) lassen.
export default function IdentityFields({
  value,
  onChange,
}: {
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
}) {
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });

  const platforms = new Map<string, string>();
  for (const t of targets.data ?? []) {
    if (t.enabled) platforms.set(t.name, t.label || t.name);
  }
  for (const system of Object.keys(value)) {
    if (!platforms.has(system)) platforms.set(system, system);
  }

  if (platforms.size === 0) {
    return (
      <p className="muted text-xs m-0">
        Keine Zielsysteme aktiviert — Plattform-Kennungen lassen sich pflegen, sobald unter „Zielsysteme" ein System aktiv ist.
      </p>
    );
  }

  return (
    <>
      {[...platforms].map(([system, label]) => (
        <div className="min-w-40" key={system}>
          <label>{label}-Kennung</label>
          <input
            value={value[system] ?? ""}
            onChange={(e) => onChange({ ...value, [system]: e.target.value })}
            placeholder="Username / Login im System"
          />
        </div>
      ))}
    </>
  );
}
