/* The organisation's workflows in the preview (#560). Example data only:
   one of them opens, the others show what else a workflow could carry. */
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import "./workflows.css";
import { BlockIcon } from "./canvas";
import { example, type Mode } from "./model";

const MODE_PILL: Record<Mode, string> = { off: "mute", watch: "info", steer: "ok" };

export default function Workflows() {
  const { t, i18n } = useTranslation();
  const ex = useMemo(() => example(i18n.language), [i18n.language]);
  const navigate = useNavigate();
  const [only, setOnly] = useState<string | null>(null);

  return (
    <div className="wf-list-page">
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("workflows.title")}</h1>
        <span className="wf-pill mute">{t("workflows.orgWide")}</span>
        <span className="wf-pill clay">{t("workflows.preview")}</span>
      </div>
      <p className="muted mb-4" style={{ maxWidth: 680 }}>
        {t("workflows.lead")}
      </p>

      <div className="wf-table-wrap">
        <table className="wf-table">
          <thead>
            <tr>
              <th>{t("workflows.col.name")}</th>
              <th>{t("workflows.col.scope")}</th>
              <th>{t("workflows.col.stations")}</th>
              <th>{t("workflows.col.mode")}</th>
              <th className="num">{t("workflows.col.cases")}</th>
              <th className="num">{t("workflows.col.stuck")}</th>
            </tr>
          </thead>
          <tbody>
            {ex.workflows.map((w, i) => (
              <tr
                key={w.id}
                tabIndex={0}
                onClick={() => (i === 0 ? navigate(`/workflows/${w.id}`) : setOnly(w.id))}
                onKeyDown={(e) => e.key === "Enter" && (i === 0 ? navigate(`/workflows/${w.id}`) : setOnly(w.id))}
              >
                <td>
                  <div className="wf-name">{w.name}</div>
                  <div className="wf-desc">{only === w.id ? t("workflows.onlyFirst") : w.desc}</div>
                </td>
                <td>{w.scope}</td>
                <td>
                  <div className="wf-chain">
                    {w.chain.map((c, j) => (
                      <span key={j}>
                        {j > 0 && <span className="wf-arrow">→</span>}
                        <span className="wf-pill mute">{c}</span>
                      </span>
                    ))}
                  </div>
                </td>
                <td>
                  <span className={`wf-pill ${MODE_PILL[w.mode]}`}>{t(`workflows.mode.${w.mode}`)}</span>
                </td>
                <td className="num">{w.mode === "off" ? "–" : w.active}</td>
                <td className={`num${w.stuck && w.mode !== "off" ? " danger" : ""}`}>{w.mode === "off" ? "–" : w.stuck}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="wf-modes">
        {(["off", "watch", "steer"] as Mode[]).map((m) => (
          <div key={m} className="wf-mode-card">
            <span className={`wf-pill ${MODE_PILL[m]}`}>{t(`workflows.mode.${m}`)}</span>
            <p>{t(`workflows.modeText.${m}`)}</p>
          </div>
        ))}
      </div>

      <div className="wf-callout">
        <BlockIcon type="agent" />
        <p>
          <b>{t("workflows.without.title")}</b> {t("workflows.without.text")}
        </p>
      </div>
    </div>
  );
}
