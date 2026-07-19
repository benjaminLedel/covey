import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, patch, post, type Organization, type Principal, type ProfileField } from "../api";

// Mandanten-Verwaltung (nur platform_admin): Organisationen der Installation
// anlegen, umbenennen, löschen. Jede neue Organisation braucht einen
// initialen Plattform-Admin — sonst wäre sie unerreichbar.
export default function Organizations({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const orgs = useQuery({
    queryKey: ["orgs"],
    queryFn: () => api<Organization[]>("/orgs"),
    retry: false,
  });

  if (orgs.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">Organisationen</h1>
        <p className="muted">Ihre Rolle ({me.Role}) hat auf die Mandanten-Verwaltung keinen Zugriff.</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Organisationen</h1>
        <span className="muted">Mandanten dieser Covey-Installation</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        Die Organisation ist die Einheit von Covey: Agenten, Nutzer, Secrets und Guard-Rails hängen
        an ihr. Löschen entfernt den Mandanten mit allem Inhalt — die eigene Organisation ist davon
        ausgenommen.
      </p>

      <CreateOrg onDone={() => qc.invalidateQueries({ queryKey: ["orgs"] })} />

      {(orgs.data ?? []).map((o) => (
        <OrgRow key={o.id} org={o} isOwn={o.id === me.OrgID} />
      ))}

      <ProfileFieldsSettings />
    </div>
  );
}

// Konfigurierbare Profilfelder der eigenen Organisation: der Admin definiert
// hier beliebige Zusatzfelder (z. B. Standort, Abteilung, Slack-Handle) —
// sie erscheinen sofort in jedem Mitarbeiter-Profil und im Team-Verzeichnis,
// das die Agenten im Prompt sehen. Umbenennen behält den Feld-Schlüssel und
// damit die bereits erfassten Werte; Löschen entfernt auch die Werte.
function ProfileFieldsSettings() {
  const qc = useQueryClient();
  const fields = useQuery({
    queryKey: ["profileFields"],
    queryFn: () => api<ProfileField[]>("/org/profile-fields"),
  });
  const [label, setLabel] = useState("");
  const invalidate = () => qc.invalidateQueries({ queryKey: ["profileFields"] });

  const create = useMutation({
    mutationFn: () => post("/org/profile-fields", { label }),
    onSuccess: () => {
      setLabel("");
      invalidate();
    },
  });

  return (
    <>
      <h2 className="text-sm secondary mt-6 mb-2">Profilfelder Ihrer Organisation</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        Zusätzliche Felder für alle Mitarbeiter-Profile — z. B. Standort, Abteilung oder Slack-Handle.
        Sie erscheinen sofort im Profil-Editor jeder Person und im Team-Verzeichnis, das die Agenten sehen.
        Löschen entfernt auch die bereits erfassten Werte.
      </p>

      <form
        className="card mb-3 flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <div className="flex-1 min-w-52">
          <label>Neues Profilfeld</label>
          <input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="z. B. Standort" required />
        </div>
        <button className="btn primary" disabled={create.isPending}>
          Hinzufügen
        </button>
        {create.isError && <p className="text-xs m-0" style={{ color: "var(--text-danger)" }}>{(create.error as Error).message}</p>}
      </form>

      {(fields.data ?? []).map((f) => (
        <FieldRow key={f.id} field={f} onChanged={invalidate} />
      ))}
      {fields.data?.length === 0 && <p className="muted text-xs">Noch keine zusätzlichen Profilfelder definiert.</p>}
    </>
  );
}

function FieldRow({ field, onChanged }: { field: ProfileField; onChanged: () => void }) {
  const [editing, setEditing] = useState(false);
  const [label, setLabel] = useState(field.label);
  const rename = useMutation({
    mutationFn: () => patch(`/org/profile-fields/${field.id}`, { label }),
    onSuccess: () => {
      setEditing(false);
      onChanged();
    },
  });
  const remove = useMutation({
    mutationFn: () => del(`/org/profile-fields/${field.id}`),
    onSuccess: onChanged,
  });
  const error = rename.error ?? remove.error;

  return (
    <div className="card mb-2" style={{ padding: "9px 15px" }}>
      <div className="flex items-center gap-4 flex-wrap">
        {editing ? (
          <form
            className="flex gap-2 items-center flex-1 min-w-52"
            onSubmit={(e) => {
              e.preventDefault();
              rename.mutate();
            }}
          >
            <input value={label} onChange={(e) => setLabel(e.target.value)} required autoFocus />
            <button className="btn sm primary" disabled={rename.isPending}>
              Speichern
            </button>
            <button type="button" className="btn sm" onClick={() => setEditing(false)}>
              Abbrechen
            </button>
          </form>
        ) : (
          <div className="flex-1 min-w-44">
            <span className="text-sm font-medium">{field.label}</span>
            <span className="muted text-xs mono"> · {field.key}</span>
          </div>
        )}
        {!editing && (
          <button className="btn sm" onClick={() => setEditing(true)}>
            Umbenennen
          </button>
        )}
        <button
          className="btn sm danger"
          onClick={() => {
            if (window.confirm(`Profilfeld „${field.label}" samt aller erfassten Werte löschen?`)) remove.mutate();
          }}
          disabled={remove.isPending}
        >
          Löschen
        </button>
      </div>
      {error && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(error as Error).message}</p>}
    </div>
  );
}

function CreateOrg({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState("");
  const [adminEmail, setAdminEmail] = useState("");
  const [adminName, setAdminName] = useState("");
  const [adminPassword, setAdminPassword] = useState("");
  const mut = useMutation({
    mutationFn: () =>
      post("/orgs", { name, admin_email: adminEmail, admin_name: adminName, admin_password: adminPassword }),
    onSuccess: () => {
      setName("");
      setAdminEmail("");
      setAdminName("");
      setAdminPassword("");
      onDone();
    },
  });
  return (
    <form
      className="card mb-4"
      onSubmit={(e) => {
        e.preventDefault();
        mut.mutate();
      }}
    >
      <div className="flex gap-3 items-end flex-wrap">
        <div className="flex-1 min-w-44">
          <label>Name der Organisation</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="flex-1 min-w-48">
          <label>Admin E-Mail</label>
          <input type="email" value={adminEmail} onChange={(e) => setAdminEmail(e.target.value)} required />
        </div>
        <div className="min-w-40">
          <label>Admin Name</label>
          <input value={adminName} onChange={(e) => setAdminName(e.target.value)} required />
        </div>
        <div className="min-w-44">
          <label>Admin Passwort (min. 8)</label>
          <input type="password" value={adminPassword} onChange={(e) => setAdminPassword(e.target.value)} minLength={8} required />
        </div>
        <button className="btn primary" disabled={mut.isPending}>
          Anlegen
        </button>
      </div>
      {mut.isError && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(mut.error as Error).message}</p>}
    </form>
  );
}

function OrgRow({ org, isOwn }: { org: Organization; isOwn: boolean }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(org.name);
  const invalidate = () => qc.invalidateQueries({ queryKey: ["orgs"] });

  const rename = useMutation({
    mutationFn: () => patch(`/orgs/${org.id}`, { name }),
    onSuccess: () => {
      setEditing(false);
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: () => del(`/orgs/${org.id}`),
    onSuccess: invalidate,
  });
  const error = rename.error ?? remove.error;

  return (
    <div className="card mb-2" style={{ padding: "11px 15px" }}>
      <div className="flex items-center gap-4 flex-wrap">
        {editing ? (
          <form
            className="flex gap-2 items-center flex-1 min-w-52"
            onSubmit={(e) => {
              e.preventDefault();
              rename.mutate();
            }}
          >
            <input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
            <button className="btn sm primary" disabled={rename.isPending}>
              Speichern
            </button>
            <button type="button" className="btn sm" onClick={() => setEditing(false)}>
              Abbrechen
            </button>
          </form>
        ) : (
          <div className="flex-1 min-w-44">
            <div className="text-sm font-medium">
              {org.name}
              {isOwn && <span className="muted text-xs"> — Ihre Organisation</span>}
            </div>
            <div className="muted text-xs">
              {org.human_count} Nutzer · {org.agent_count} Agenten
            </div>
          </div>
        )}
        {org.fleet_killed && <span className="badge st-killed">Flotte gestoppt</span>}
        {!editing && (
          <button className="btn sm" onClick={() => setEditing(true)}>
            Umbenennen
          </button>
        )}
        <button
          className="btn sm danger"
          onClick={() => {
            if (window.confirm(`Organisation „${org.name}" mit allen Agenten und Nutzern löschen?`)) remove.mutate();
          }}
          disabled={isOwn || remove.isPending}
          title={isOwn ? "Die eigene Organisation kann nicht gelöscht werden" : undefined}
        >
          Löschen
        </button>
      </div>
      {error && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(error as Error).message}</p>}
    </div>
  );
}
