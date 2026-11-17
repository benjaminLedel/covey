import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type TargetPlugin } from "../api";
import { TargetIcon } from "./TargetIcon";

export default function IdentityFields({
  value,
  onChange,
}: {
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
}) {
  const { t } = useTranslation();
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });

  type Platform = { label: string; kind?: string; category?: string };
  const platforms = new Map<string, Platform>();
  for (const tgt of targets.data ?? []) {
    if (tgt.enabled) {
      platforms.set(tgt.name, { label: tgt.label || tgt.name, kind: tgt.kind, category: tgt.category });
    }
  }
  for (const system of Object.keys(value)) {
    if (!platforms.has(system)) platforms.set(system, { label: system });
  }

  if (platforms.size === 0) {
    return <p className="muted text-xs m-0">{t("profile.noTargets")}</p>;
  }

  /* One line per system: icon and name on the left, the login on the right
     (#743). A wrapping row of labelled boxes looked like a form to fill in
     completely; most people need one or two of them. */
  return (
    <div className="identity-block">
      <h3 className="ps-subtitle">{t("profile.identities")}</h3>
      <p className="ps-hint mb-2">{t("profile.identitiesHint")}</p>
      <div className="identity-list">
        {[...platforms].map(([system, p]) => (
          <label className="identity-row" key={system}>
            <span className="identity-name">
              <TargetIcon name={system} kind={p.kind} category={p.category} size={16} />
              {p.label}
            </span>
            <input
              value={value[system] ?? ""}
              onChange={(e) => onChange({ ...value, [system]: e.target.value })}
              placeholder={t("profile.identityPlaceholder")}
              aria-label={t("profile.identityLabel", { system: p.label })}
            />
          </label>
        ))}
      </div>
    </div>
  );
}
