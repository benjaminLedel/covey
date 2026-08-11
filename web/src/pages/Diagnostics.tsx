import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { api, type Principal } from "../api";

// Plattform-Diagnose: was ein Neustart hier anträfe, und welche Agenten-Configs
// nach einem Upgrade nachziehen müssen.
//
// Beides gab es nur als Unterbefehl — also nur für den, der eine Shell auf dem
// Host hat. Der Config-Lint hat die Lektion zuerst gelernt: eine Prüfung, die
// niemand aufruft, ist eine, die es faktisch nicht gibt.

type Finding = {
  ok: boolean;
  blocking: boolean;
  what: string;
  detail: string;
  remedy?: string;
};

type DoctorReport = { findings: Finding[]; blocking: number };

type LintFinding = {
  agent_slug: string;
  rule: string;
  severity: string;
  message: string;
  hint?: string;
};

type AgentFindings = {
  agent_id: string;
  slug: string;
  findings: LintFinding[];
};

export default function Diagnostics({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const isAdmin = me.Role === "platform_admin";

  const doctor = useQuery({
    queryKey: ["platform-doctor"],
    queryFn: () => api<DoctorReport>("/platform/doctor"),
    enabled: isAdmin,
  });
  const lint = useQuery({
    queryKey: ["platform-lint"],
    queryFn: () => api<AgentFindings[]>("/platform/lint"),
  });

  return (
    <div className="stack-lg">
      <div>
        <h1>{t("diagnostics.title")}</h1>
        <p className="muted text-sm">{t("diagnostics.intro")}</p>
      </div>

      {isAdmin && (
        <div className="card p-4 stack-sm">
          <h2 className="text-sm">{t("diagnostics.stateTitle")}</h2>
          <p className="muted text-xs">{t("diagnostics.stateHint")}</p>
          {doctor.isLoading && <p className="muted text-sm">{t("common.loading")}</p>}
          {doctor.isError && (
            <p className="danger-text text-sm">{(doctor.error as Error).message}</p>
          )}
          {doctor.data && (
            <>
              {doctor.data.blocking > 0 && (
                <p className="danger-text text-sm">
                  {t("diagnostics.blocking", { count: doctor.data.blocking })}
                </p>
              )}
              <table className="tbl text-sm">
                <tbody>
                  {doctor.data.findings.map((f) => (
                    <tr key={f.what}>
                      <td style={{ width: 24 }}>
                        {/* Blockierend, Hinweis, in Ordnung — drei Zustände, und
                            der mittlere ist der häufigste. */}
                        <span
                          title={f.blocking ? t("diagnostics.markBlocking") : f.ok ? t("diagnostics.markOk") : t("diagnostics.markNote")}
                          style={{
                            color: f.blocking ? "var(--error)" : f.ok ? "var(--ok, var(--accent))" : "var(--warn, #b58900)",
                          }}
                        >
                          {f.blocking ? "×" : f.ok ? "·" : "!"}
                        </span>
                      </td>
                      <td style={{ whiteSpace: "nowrap" }}>{f.what}</td>
                      <td>
                        <div>{f.detail}</div>
                        {f.remedy && <div className="muted text-xs">→ {f.remedy}</div>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </div>
      )}

      <div className="card p-4 stack-sm">
        <h2 className="text-sm">{t("diagnostics.lintTitle")}</h2>
        <p className="muted text-xs">{t("diagnostics.lintHint")}</p>
        {lint.isLoading && <p className="muted text-sm">{t("common.loading")}</p>}
        {lint.data && lint.data.length === 0 && (
          <p className="muted text-sm">{t("diagnostics.lintClean")}</p>
        )}
        {lint.data && lint.data.length > 0 && (
          <table className="tbl text-sm">
            <tbody>
              {lint.data.map((a) => (
                <tr key={a.agent_id}>
                  <td style={{ whiteSpace: "nowrap", verticalAlign: "top" }}>
                    <Link to={`/agents/${a.agent_id}`}>{a.slug}</Link>
                  </td>
                  <td>
                    {a.findings.map((f, i) => (
                      <div key={i} style={{ marginBottom: 4 }}>
                        <span
                          className="text-xs mono"
                          style={{
                            color: f.severity === "warn" ? "var(--warn, #b58900)" : "var(--muted)",
                          }}
                        >
                          {f.rule}
                        </span>{" "}
                        {f.message}
                        {f.hint && <div className="muted text-xs">→ {f.hint}</div>}
                      </div>
                    ))}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
