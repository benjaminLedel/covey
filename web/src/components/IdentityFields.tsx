import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type TargetPlugin } from "../api";

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

  const platforms = new Map<string, string>();
  for (const tgt of targets.data ?? []) {
    if (tgt.enabled) platforms.set(tgt.name, tgt.label || tgt.name);
  }
  for (const system of Object.keys(value)) {
    if (!platforms.has(system)) platforms.set(system, system);
  }

  if (platforms.size === 0) {
    return (
      <p className="muted text-xs m-0">
        {t("profile.noTargets")}
      </p>
    );
  }

  return (
    <>
      {[...platforms].map(([system, label]) => (
        <div className="min-w-40" key={system}>
          <label>{t("profile.identityLabel", { system: label })}</label>
          <input
            value={value[system] ?? ""}
            onChange={(e) => onChange({ ...value, [system]: e.target.value })}
            placeholder={t("profile.identityPlaceholder")}
          />
        </div>
      ))}
    </>
  );
}
