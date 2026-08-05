import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type LintFinding } from "../../api";

// Der Config-Lint auf der Agentenseite.
//
// Es gab ihn als `covey config lint` — und damit praktisch nicht: die Regel zu
// häufigen Turn-Limit-Abbrüchen hätte den Zustand eines QA-Agenten am ersten
// Tag beschrieben (22 von 23 Fehlschlägen am Limit, 300 $ verbrannt, kein
// einziger Merge Request durchgetestet), und niemand hat es gesehen, weil
// niemand auf Verdacht ein Unterkommando aufruft. Wer nach einem Agenten sieht,
// weil etwas mit ihm nicht stimmt, sieht auf seine Seite.
//
// Bewusst über der Reiterleiste und nicht in einem eigenen Tab: ein Befund, den
// man erst aufklappen muss, ist einer, den man nicht liest.
export function LintFindings({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const lint = useQuery({
    queryKey: ["agent-lint", agentId],
    queryFn: () => api<LintFinding[] | null>(`/agents/${agentId}/lint`),
    retry: false,
  });
  const findings = lint.data ?? [];
  if (findings.length === 0) return null;

  return (
    <div className="card mt-3" style={{ padding: "10px 14px" }}>
      <div className="text-xs mb-2" style={{ fontWeight: 600 }}>
        {t("agent.lint.title", { count: findings.length })}
      </div>
      {findings.map((f, i) => (
        <div key={i} className="text-xs" style={{ marginBottom: i === findings.length - 1 ? 0 : 8 }}>
          <div className="flex items-baseline gap-2 flex-wrap">
            <span
              className="badge"
              style={{
                background: "transparent",
                border: "0.5px solid var(--border-strong)",
                color: f.severity === "warn" ? "var(--text-warning)" : "var(--text-secondary)",
              }}
            >
              {f.rule}
            </span>
            <span style={{ fontWeight: 600 }}>{f.message}</span>
            {f.file && (
              <span className="muted mono">
                {f.file}
                {f.line ? `:${f.line}` : ""}
              </span>
            )}
          </div>
          <div className="muted" style={{ marginTop: 2 }}>{f.hint}</div>
        </div>
      ))}
    </div>
  );
}
