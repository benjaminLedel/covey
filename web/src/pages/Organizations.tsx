import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, patch, post, type Organization, type Principal, type ProfileField } from "../api";
import PlatformHeader from "./platform/Header";

// Die Mandantenliste — Startseite des Plattform-Panels (Platform.tsx).
//
// Die Profilfelder standen hier einmal darunter. Sie sind aber Stammdaten EINER
// Organisation und keine Sache der Instanz; sie stehen jetzt im
// Administrations-Panel unter "Profil".
export default function Organizations({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const orgs = useQuery({
    queryKey: ["orgs"],
    queryFn: () => api<Organization[]>("/platform/orgs"),
    retry: false,
  });

  if (orgs.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">{t("orgs.title")}</h1>
        <p className="muted">{t("orgs.noAccess", { role: me.Role })}</p>
      </div>
    );
  }

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>{t("orgs.desc")}</p>

      <CreateOrg onDone={() => qc.invalidateQueries({ queryKey: ["orgs"] })} />

      {(orgs.data ?? []).map((o) => (
        <OrgRow key={o.id} org={o} isOwn={o.id === me.OrgID} />
      ))}
    </div>
  );
}

/** Die Profilfelder einer Organisation — eingebunden vom Administrations-Panel. */
export function ProfileFieldsSettings() {
  const { t } = useTranslation();
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
      <h2 className="text-sm secondary mt-6 mb-2">{t("orgs.profileFields")}</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("orgs.profileFieldsDesc")}
      </p>

      <form
        className="card mb-3 flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <div className="flex-1 min-w-52">
          <label>{t("orgs.newField")}</label>
          <input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t("orgs.newFieldPlaceholder")} required />
        </div>
        <button className="btn primary" disabled={create.isPending}>
          {t("orgs.addField")}
        </button>
        {create.isError && <p className="text-xs m-0" style={{ color: "var(--text-danger)" }}>{(create.error as Error).message}</p>}
      </form>

      {(fields.data ?? []).map((f) => (
        <FieldRow key={f.id} field={f} onChanged={invalidate} />
      ))}
      {fields.data?.length === 0 && <p className="muted text-xs">{t("orgs.noFields")}</p>}
    </>
  );
}

function FieldRow({ field, onChanged }: { field: ProfileField; onChanged: () => void }) {
  const { t } = useTranslation();
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
              {t("orgs.save")}
            </button>
            <button type="button" className="btn sm" onClick={() => setEditing(false)}>
              {t("orgs.cancel")}
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
            {t("orgs.rename")}
          </button>
        )}
        <button
          className="btn sm danger"
          onClick={() => {
            if (window.confirm(t("orgs.deleteFieldConfirm", { label: field.label }))) remove.mutate();
          }}
          disabled={remove.isPending}
        >
          {t("orgs.delete")}
        </button>
      </div>
      {error && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(error as Error).message}</p>}
    </div>
  );
}

function CreateOrg({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [adminEmail, setAdminEmail] = useState("");
  const [adminName, setAdminName] = useState("");
  const [adminPassword, setAdminPassword] = useState("");
  const mut = useMutation({
    mutationFn: () =>
      post("/platform/orgs", { name, admin_email: adminEmail, admin_name: adminName, admin_password: adminPassword }),
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
          <label>{t("orgs.orgName")}</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="flex-1 min-w-48">
          <label>{t("orgs.adminEmail")}</label>
          <input type="email" value={adminEmail} onChange={(e) => setAdminEmail(e.target.value)} required />
        </div>
        <div className="min-w-40">
          <label>{t("orgs.adminName")}</label>
          <input value={adminName} onChange={(e) => setAdminName(e.target.value)} required />
        </div>
        <div className="min-w-44">
          <label>{t("orgs.adminPassword")}</label>
          <input type="password" value={adminPassword} onChange={(e) => setAdminPassword(e.target.value)} minLength={8} required />
        </div>
        <button className="btn primary" disabled={mut.isPending}>
          {t("orgs.createOrg")}
        </button>
      </div>
      {mut.isError && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(mut.error as Error).message}</p>}
    </form>
  );
}

function OrgRow({ org, isOwn }: { org: Organization; isOwn: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(org.name);
  const invalidate = () => qc.invalidateQueries({ queryKey: ["orgs"] });

  const rename = useMutation({
    mutationFn: () => patch(`/platform/orgs/${org.id}`, { name }),
    onSuccess: () => {
      setEditing(false);
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: () => del(`/platform/orgs/${org.id}`),
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
              {t("orgs.save")}
            </button>
            <button type="button" className="btn sm" onClick={() => setEditing(false)}>
              {t("orgs.cancel")}
            </button>
          </form>
        ) : (
          <div className="flex-1 min-w-44">
            <div className="text-sm font-medium">
              {org.name}
              {isOwn && <span className="muted text-xs"> {t("orgs.ownOrg")}</span>}
            </div>
            <div className="muted text-xs">
              {t("orgs.users", { count: org.human_count })} · {t("orgs.agents", { count: org.agent_count })}
            </div>
          </div>
        )}
        {org.fleet_killed && <span className="badge state st-killed">{t("orgs.fleetStopped")}</span>}
        {!editing && (
          <button className="btn sm" onClick={() => setEditing(true)}>
            {t("orgs.rename")}
          </button>
        )}
        <button
          className="btn sm danger"
          onClick={() => {
            if (window.confirm(t("orgs.deleteConfirm", { name: org.name }))) remove.mutate();
          }}
          disabled={isOwn || remove.isPending}
          title={isOwn ? t("orgs.cantDeleteOwn") : undefined}
        >
          {t("orgs.delete")}
        </button>
      </div>
      {error && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(error as Error).message}</p>}
    </div>
  );
}
