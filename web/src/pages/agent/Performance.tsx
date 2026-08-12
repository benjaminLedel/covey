import { useQuery } from "@tanstack/react-query";
import { api, type IndicatorReport } from "../../api";
import { PriceList } from "../../components/PriceList";

/** Die Leistungsseite des Mitarbeiters — direkt unter der Kostenleiste, weil
 *  beide dieselbe Frage von zwei Seiten beantworten: was kostet dieser
 *  Mitarbeiter, und was liefert er.
 *
 *  Bewusst in der kompakten Darstellung und ohne eigene Überschrift: die
 *  Agentenseite trägt darunter die Reiter (Backlog, Recording, Speicher …), und
 *  die sind der eigentliche Inhalt. Ein Block, der sie aus dem sichtbaren
 *  Bereich schiebt, schadet mehr, als die Zahlen nützen — sie sollen im
 *  Vorbeigehen lesbar sein, nicht die Seite übernehmen.
 *
 *  Ohne Kennzahlen in der KPIS.md blendet sich die Zeile ganz aus: ein leerer
 *  Kasten auf jeder Agentenseite wäre Rauschen, und der Hinweis, wie man
 *  Kennzahlen anlegt, steht auf der Kostenseite. */
export function Performance({ agentId }: { agentId: string }) {
  const rep = useQuery({
    queryKey: ["cost", "indicators", agentId, 30],
    queryFn: () => api<IndicatorReport>(`/agents/${agentId}/cost/indicators?days=30`),
    // Die Kennzahlen EINES Agenten folgen der Arbeitsakte (spec/21):
    // Controlling bekommt hier eine 403. Kein Wiederholen und kein Fehlertext —
    // eine Rolle, die etwas nicht sehen darf, soll es nicht als kaputt
    // angezeigt bekommen, sondern gar nicht.
    retry: false,
  });
  const data = rep.data;
  if (!data || ((data.indicators ?? []).length === 0 && data.failed === 0)) return null;
  return (
    <div className="mt-3">
      <PriceList rep={data} compact />
    </div>
  );
}
