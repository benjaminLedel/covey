import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  totalInput,
  type CostSummary,
} from "../../api";
import { exact, fmtCount, fmtUSD } from "../../format";

// CostBar ist die meistgelesene Zahlenzeile der Oberfläche — sie steht über
// jedem Agenten. Sie schrieb ihre Zahlen lange selbst: vier Nachkommastellen
// auf einem vierstelligen Betrag und Tokens mit englischen Kommas, während
// daneben auf derselben Seite „61,74 $ / Stück" in der vereinbarten Form stand.
//
// Die gekürzte Zahl ist zum Überfliegen; wer eine Rechnung prüft, braucht die
// Ziffern und findet sie im Tooltip daneben (exact).
export function CostBar({ agentId, budget }: { agentId: string; budget: number }) {
  const { t } = useTranslation();
  const cost = useQuery({
    queryKey: ["cost", agentId],
    queryFn: () => api<CostSummary>(`/agents/${agentId}/cost`),
  });
  const c = cost.data;
  if (!c) return null;
  const ein = totalInput(c);
  const cached = c.cache_read_tokens + c.cache_creation_tokens;
  return (
    <div className="card flex gap-8 text-sm">
      <div>
        <div className="muted text-xs">{t("agent.cost.total")}</div>
        <div className="font-medium" title={`${c.total_usd} $`}>
          {fmtUSD(c.total_usd)}
        </div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.tokens")}</div>
        <div className="font-medium" title={`${exact(ein)} / ${exact(c.output_tokens)}`}>
          {fmtCount(ein)} / {fmtCount(c.output_tokens)}
        </div>
        <div className="muted text-xs" title={exact(cached)}>
          {t("agent.cost.cached", { n: fmtCount(cached) })}
        </div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.runs")}</div>
        <div className="font-medium" title={exact(c.entries)}>
          {fmtCount(c.entries)}
        </div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.budget")}</div>
        <div className="font-medium">{budget > 0 ? fmtUSD(budget) : t("agent.cost.noCap")}</div>
      </div>
    </div>
  );
}
