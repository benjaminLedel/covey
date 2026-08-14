import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { api, post, type Principal } from "../api";

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

/* embedded: unter dem Reiter der Verwaltung trägt die Seite ihre Überschrift
   nicht selbst — dieselbe Regel wie beim Audit. */
export default function Diagnostics({ me, embedded = false }: { me: Principal; embedded?: boolean }) {
  const { t } = useTranslation();
  // org_admin: der alte Name platform_admin ist seit 0061 keiner mehr.
  const isAdmin = me.Role === "org_admin";

  const qc = useQueryClient();
  /* Die eine Zeile, die die Plattform nicht selbst erledigen kann: Ob der
     Blockspeicher in der Sicherung liegt, sieht sie nicht — sie kann nur
     festhalten, dass jemand die Pflicht übernommen hat. Ohne diesen Schritt
     stünde der Hinweis für immer da, und ein Hinweis ohne Ende wird nicht mehr
     gelesen — auch der daneben nicht. */
  const bestaetigen = useMutation({
    mutationFn: (confirmed: boolean) =>
      post<{ confirmed_at: string }>("/platform/doctor/home-store-backup", { confirmed }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform-doctor"] }),
  });

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
    <div className="flex flex-col gap-6">
      <div>
        {!embedded && <h1 className="text-[22px]">{t("diagnostics.title")}</h1>}
        <p className="muted text-sm">{t("diagnostics.intro")}</p>
      </div>

      {isAdmin && (
        <div className="card p-4 flex flex-col gap-2">
          <h2 className="text-[15px]">{t("diagnostics.stateTitle")}</h2>
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
                        {f.what === "home store" && (
                          <button
                            className="btn sm"
                            style={{ marginTop: 6 }}
                            disabled={bestaetigen.isPending}
                            onClick={() => bestaetigen.mutate(f.ok !== true)}
                          >
                            {f.ok
                              ? t("diagnostics.backupWithdraw")
                              : t("diagnostics.backupConfirm")}
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </div>
      )}

      <div className="card p-4 flex flex-col gap-2">
        <h2 className="text-[15px]">{t("diagnostics.lintTitle")}</h2>
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
