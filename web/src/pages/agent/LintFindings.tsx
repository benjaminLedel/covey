import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type LintFinding } from "../../api";

// The config lint on the agent page.
//
// It existed as `covey config lint` — and thus practically not: the rule on
// frequent turn-limit aborts would have described the state of a QA agent on
// its first day (22 of 23 failures at the limit, $300 burned, not a single
// merge request tested through), and nobody saw it, because nobody invokes a
// subcommand on a hunch. Whoever checks on an agent because something is wrong
// with it, looks at its page.
//
// Deliberately above the tab bar, not in its own tab: a finding you first have
// to unfold is a finding you do not read.
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
