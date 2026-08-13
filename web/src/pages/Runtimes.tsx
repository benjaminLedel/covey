import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type Principal, type RuntimeInfo, type SetupStep } from "../api";
import RuntimeInstances from "./RuntimeInstances";

const canEdit = (role: string) => role === "org_admin" || role === "security";

function renderText(text: string) {
  return text.split(/(`[^`]+`)/g).map((part, i) =>
    part.startsWith("`") && part.endsWith("`") ? (
      <span key={i} className="mono">
        {part.slice(1, -1)}
      </span>
    ) : (
      <span key={i}>{part}</span>
    ),
  );
}

function Steps({ steps }: { steps: SetupStep[] }) {
  return (
    <ol className="text-sm" style={{ paddingLeft: 18, listStyle: "decimal", lineHeight: 1.7 }}>
      {steps.map((step, i) => (
        <li key={i}>
          {renderText(step.text)}
          {step.items && (
            <ul className="muted" style={{ paddingLeft: 16, listStyle: "disc", marginTop: 2 }}>
              {step.items.map((it, j) => (
                <li key={j}>{renderText(it)}</li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ol>
  );
}

export default function Runtimes({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const [openInfo, setOpenInfo] = useState<string | null>(null);
  const runtimes = useQuery({
    queryKey: ["runtimes"],
    queryFn: () => api<RuntimeInfo[]>("/runtimes"),
  });

  const list = runtimes.data ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("runtimes.title")}</h1>
        <span className="muted">{t("runtimes.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-5" style={{ maxWidth: 640 }}>
        {t("runtimes.desc")}
      </p>

      <RuntimeInstances canEdit={canEdit(me.Role)} />

      <div className="flex items-baseline gap-3 mb-2">
        <h2 className="text-[18px]">{t("runtimes.engines.title")}</h2>
        <span className="muted text-xs">{t("runtimes.engines.subtitle")}</span>
      </div>

      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(260px, 1fr))" }}>
        {list.map((rt) => (
          <div key={rt.name} className="card" style={{ padding: "14px 16px" }}>
            <div className="flex items-center gap-2 mb-1">
              <span className="font-medium">{rt.label}</span>
              <span className="mono text-xs muted">{rt.name}</span>
              {rt.setup?.length > 0 && (
                <button
                  className="btn sm ml-auto"
                  aria-expanded={openInfo === rt.name}
                  title={t("runtimes.showInfo")}
                  onClick={() => setOpenInfo((cur) => (cur === rt.name ? null : rt.name))}
                >
                  ⓘ Info
                </button>
              )}
            </div>
            <p className="muted text-xs mb-2">{rt.description}</p>
            <span
              className="text-[11px]"
              style={{ color: rt.credentials?.length ? "var(--clay)" : "var(--text-secondary)" }}
            >
              {rt.credentials?.length ? t("runtimes.needsCredential") : t("runtimes.noCredential")}
            </span>
            {rt.capabilities && !rt.capabilities.resume && (
              <span className="badge st-blocked ml-2" title={t("runtimes.instances.noResumeHint")}>
                {t("runtimes.instances.noResume")}
              </span>
            )}
            {openInfo === rt.name && (
              <div className="mt-3 pt-3" style={{ borderTop: "0.5px solid var(--border)" }}>
                <div className="text-xs font-medium mb-1">{t("runtimes.setup", { label: rt.label })}</div>
                <Steps steps={rt.setup} />
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
