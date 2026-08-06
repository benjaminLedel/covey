import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type IndicatorReport } from "../../api";
import { PriceList } from "../../components/PriceList";

/** Die Leistungsseite des Mitarbeiters — neben der Kostenleiste, weil beide
 *  dieselbe Frage von zwei Seiten beantworten: was kostet dieser Mitarbeiter,
 *  und was liefert er.
 *
 *  Sie zeigt dieselbe Preisliste wie die Kostenseite, auf diesen einen Agenten
 *  eingegrenzt. Dieselbe Komponente, damit die Zahlen nicht zweimal gebaut
 *  werden und darum auch nicht zweimal unterschiedlich sein können.
 *
 *  Ohne Kennzahlen in der KPIS.md blendet sich der Block ganz aus: ein leerer
 *  Kasten auf jeder Agentenseite wäre Rauschen, und der Hinweis, wie man sie
 *  anlegt, steht auf der Kostenseite. */
export function Performance({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const rep = useQuery({
    queryKey: ["cost", "indicators", agentId, 30],
    queryFn: () => api<IndicatorReport>(`/agents/${agentId}/cost/indicators?days=30`),
  });
  const data = rep.data;
  if (!data || ((data.indicators ?? []).length === 0 && data.failed === 0)) return null;
  return (
    <div className="card mt-3">
      <div className="flex items-baseline justify-between mb-1">
        <span style={{ fontWeight: 600 }}>{t("agent.performance.title")}</span>
        <span className="muted text-xs">{t("agent.performance.window")}</span>
      </div>
      <p className="muted text-xs mb-3">{t("agent.performance.hint")}</p>
      <PriceList rep={data} />
    </div>
  );
}
