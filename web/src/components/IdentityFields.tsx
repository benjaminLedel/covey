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
    return (
      <p className="muted text-xs m-0">
        {t("profile.noTargets")}
      </p>
    );
  }

  return (
    <>
      {[...platforms].map(([system, p]) => (
        <div className="min-w-40" key={system}>
          <label className="flex items-center gap-1.5">
            <TargetIcon name={system} kind={p.kind} category={p.category} size={14} />
            {t("profile.identityLabel", { system: p.label })}
          </label>
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
