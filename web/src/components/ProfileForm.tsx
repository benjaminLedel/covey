import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, patch, type Human, type ProfileField } from "../api";
import IdentityFields from "./IdentityFields";

// Die Profilfelder, die Menschen und Agenten teilen — das Formular bedient
// beide (PATCH /users/{id}, /auth/me bzw. /agents/{id}/profile).
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

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex gap-3 items-end flex-wrap">
        <div className="min-w-40">
          <label>{t("profile.jobTitle")}</label>
          <input value={p.job_title} onChange={(e) => setP({ ...p, job_title: e.target.value })} placeholder={t("profile.jobTitlePlaceholder")} />
        </div>
        <div className="min-w-40">
          <label>{t("profile.phone")}</label>
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
          <label>{t("profile.responsibilities")}</label>
          <input
            value={p.responsibilities}
            onChange={(e) => setP({ ...p, responsibilities: e.target.value })}
            placeholder={t("profile.responsibilitiesPlaceholder")}
          />
        </div>
        <button className="btn sm primary" disabled={save.isPending}>
          {t("profile.save")}
        </button>
      </div>
      <p className="muted text-xs mt-2 mb-0">
        {t("profile.responsibilitiesHint")}
      </p>
      {save.isError && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(save.error as Error).message}</p>}
    </form>
  );
}
