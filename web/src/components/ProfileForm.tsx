import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, patch, type Human, type ProfileField } from "../api";
import IdentityFields from "./IdentityFields";

// Gemeinsamer Editor für das Mitarbeiter-Profil (Funktion, Plattform-
// Kennungen, Telefon, Zuständigkeiten). Wird an drei Stellen verwendet:
// Benutzerverwaltung (Admin, /users/{id}), eigene Profil-Seite (/auth/me)
// und Organigramm (je nach Rolle einer der beiden Endpunkte, sonst readOnly).
export default function ProfileForm({
  human,
  endpoint,
  readOnly,
  onSaved,
}: {
  human: Human;
  endpoint: string;
  readOnly?: boolean;
  onSaved?: () => void;
}) {
  const qc = useQueryClient();
  // Org-weit konfigurierte Zusatzfelder (Organisationen-Seite) — bestimmen,
  // welche Eingaben über die Basisfelder hinaus angeboten werden.
  const fields = useQuery({
    queryKey: ["profileFields"],
    queryFn: () => api<ProfileField[]>("/org/profile-fields"),
  });
  const fieldDefs = fields.data ?? [];
  const [p, setP] = useState({
    job_title: human.job_title,
    identities: human.identities ?? {},
    phone: human.phone,
    responsibilities: human.responsibilities,
    custom: human.custom ?? {},
  });
  // Beim Wechsel auf eine andere Person (Organigramm) neu initialisieren.
  useEffect(() => {
    setP({
      job_title: human.job_title,
      identities: human.identities ?? {},
      phone: human.phone,
      responsibilities: human.responsibilities,
      custom: human.custom ?? {},
    });
  }, [human.id]); // eslint-disable-line react-hooks/exhaustive-deps

  const save = useMutation({
    mutationFn: () => patch(endpoint, p),
    onSuccess: () => {
      for (const key of ["users", "orgchart", "myProfile", "human"]) qc.invalidateQueries({ queryKey: [key] });
      onSaved?.();
    },
  });

  if (readOnly) {
    const identities = Object.entries(human.identities ?? {});
    const rows: [string, string][] = [
      ["Funktion", human.job_title],
      ["Telefon", human.phone],
      ["Zuständig für", human.responsibilities],
      ...identities.map(([system, id]) => [`${system}-Kennung`, id] as [string, string]),
      ...fieldDefs.map((f) => [f.label, human.custom?.[f.key] ?? ""] as [string, string]),
    ];
    const filled = rows.filter(([, v]) => v);
    if (filled.length === 0) return <p className="muted text-xs m-0">Kein Profil hinterlegt.</p>;
    return (
      <div className="text-xs">
        {filled.map(([label, value]) => (
          <div key={label} className="flex gap-2 py-0.5">
            <span className="muted" style={{ minWidth: 110 }}>{label}</span>
            <span>{value}</span>
          </div>
        ))}
      </div>
    );
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex gap-3 items-end flex-wrap">
        <div className="min-w-40">
          <label>Funktion</label>
          <input value={p.job_title} onChange={(e) => setP({ ...p, job_title: e.target.value })} placeholder="z. B. QA Engineer" />
        </div>
        <div className="min-w-40">
          <label>Telefon</label>
          <input value={p.phone} onChange={(e) => setP({ ...p, phone: e.target.value })} />
        </div>
        <IdentityFields value={p.identities} onChange={(identities) => setP({ ...p, identities })} />
        {fieldDefs.map((f) => (
          <div className="min-w-40" key={f.id}>
            <label>{f.label}</label>
            <input
              value={p.custom[f.key] ?? ""}
              onChange={(e) => setP({ ...p, custom: { ...p.custom, [f.key]: e.target.value } })}
            />
          </div>
        ))}
      </div>
      <div className="flex gap-3 items-end flex-wrap mt-3">
        <div className="flex-1 min-w-52">
          <label>Zuständigkeiten (sehen die Agenten)</label>
          <input
            value={p.responsibilities}
            onChange={(e) => setP({ ...p, responsibilities: e.target.value })}
            placeholder="z. B. testet Bugfixes im Projekt educa"
          />
        </div>
        <button className="btn sm primary" disabled={save.isPending}>
          Speichern
        </button>
      </div>
      <p className="muted text-xs mt-2 mb-0">
        Profil und Zuständigkeiten gehen als Team-Verzeichnis in den Prompt der Agenten — so weiß z. B. der
        GitLab-Bot, wem er ein Issue zum Testen zuweist.
      </p>
      {save.isError && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(save.error as Error).message}</p>}
    </form>
  );
}
