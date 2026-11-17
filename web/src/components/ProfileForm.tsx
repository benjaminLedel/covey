import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, patch, type Human, type ProfileField } from "../api";
import IdentityFields from "./IdentityFields";

// The profile fields that humans and agents share — the form serves both
// (`PATCH /users/{id}`, `/auth/me` and `/agents/{id}/profile`).
export type ProfileData = Pick<Human, "id" | "job_title" | "identities" | "phone" | "responsibilities" | "custom">;

export default function ProfileForm({
  human,
  endpoint,
  readOnly,
  onSaved,
}: {
  human: ProfileData;
  endpoint: string;
  readOnly?: boolean;
  onSaved?: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
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
      for (const key of ["users", "orgchart", "myProfile", "human", "agent", "agents"]) qc.invalidateQueries({ queryKey: [key] });
      onSaved?.();
    },
  });

  if (readOnly) {
    const identities = Object.entries(human.identities ?? {});
    const rows: [string, string][] = [
      [t("profile.jobTitle"), human.job_title],
      [t("profile.phone"), human.phone],
      [t("profile.responsibilities"), human.responsibilities],
      ...identities.map(([system, id]) => [t("profile.identityLabel", { system }), id] as [string, string]),
      ...fieldDefs.map((f) => [f.label, human.custom?.[f.key] ?? ""] as [string, string]),
    ];
    const filled = rows.filter(([, v]) => v);
    if (filled.length === 0) return <p className="muted text-xs m-0">{t("profile.noProfile")}</p>;
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

  const initial = JSON.stringify({
    job_title: human.job_title,
    identities: human.identities ?? {},
    phone: human.phone,
    responsibilities: human.responsibilities,
    custom: human.custom ?? {},
  });
  const dirty = JSON.stringify(p) !== initial;

  return (
    <form
      className="profile-form"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="field-grid">
        <div>
          <label htmlFor="pf-job">{t("profile.jobTitle")}</label>
          <input id="pf-job" value={p.job_title} onChange={(e) => setP({ ...p, job_title: e.target.value })} placeholder={t("profile.jobTitlePlaceholder")} />
        </div>
        <div>
          <label htmlFor="pf-phone">{t("profile.phone")}</label>
          <input id="pf-phone" type="tel" value={p.phone} onChange={(e) => setP({ ...p, phone: e.target.value })} />
        </div>
        {fieldDefs.map((f) => (
          <div key={f.id}>
            <label htmlFor={`pf-${f.key}`}>{f.label}</label>
            <input
              id={`pf-${f.key}`}
              value={p.custom[f.key] ?? ""}
              onChange={(e) => setP({ ...p, custom: { ...p.custom, [f.key]: e.target.value } })}
            />
          </div>
        ))}
        <div className="field-wide">
          <label htmlFor="pf-resp">{t("profile.responsibilities")}</label>
          <textarea
            id="pf-resp"
            rows={2}
            value={p.responsibilities}
            onChange={(e) => setP({ ...p, responsibilities: e.target.value })}
            placeholder={t("profile.responsibilitiesPlaceholder")}
          />
          <p className="muted text-xs mt-1 mb-0">{t("profile.responsibilitiesHint")}</p>
        </div>
      </div>

      <IdentityFields value={p.identities} onChange={(identities) => setP({ ...p, identities })} />

      <div className="edit-panel-foot">
        {save.isError && <span className="danger-text text-xs mr-auto">{(save.error as Error).message}</span>}
        {save.isSuccess && !dirty && <span className="muted text-xs mr-auto">{t("profile.saved")}</span>}
        <button className="btn primary" disabled={!dirty || save.isPending}>
          {t("profile.save")}
        </button>
      </div>
    </form>
  );
}
