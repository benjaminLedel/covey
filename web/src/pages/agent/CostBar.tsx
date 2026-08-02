import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";
import {
  api,
  type CostSummary,
} from "../../api";

export function CostBar({ agentId, budget }: { agentId: string; budget: number }) {
  const { t } = useTranslation();
  const cost = useQuery({
    queryKey: ["cost", agentId],
    queryFn: () => api<CostSummary>(`/agents/${agentId}/cost`),
  });
  const c = cost.data;
  if (!c) return null;
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  return (
    <div className="card flex gap-8 text-sm">
      <div>
        <div className="muted text-xs">{t("agent.cost.total")}</div>
        <div className="font-medium">{c.total_usd.toFixed(4)} $</div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.tokens")}</div>
        <div className="font-medium">
          {c.input_tokens.toLocaleString(locale)} / {c.output_tokens.toLocaleString(locale)}
        </div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.runs")}</div>
        <div className="font-medium">{c.entries}</div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.budget")}</div>
        <div className="font-medium">{budget > 0 ? `${budget.toFixed(2)} $` : t("agent.cost.noCap")}</div>
      </div>
    </div>
  );
}
