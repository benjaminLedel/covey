import { useQuery } from "@tanstack/react-query";
import { api, type IndicatorReport } from "../../api";
import { PriceList } from "../../components/PriceList";

/** The employee's performance page — directly below the cost bar, because both
 *  answer the same question from two sides: what does this employee cost, and
 *  what does it deliver.
 *
 *  Deliberately in the compact display and without its own heading: the agent
 *  page carries the tabs below it (Backlog, Recording, Memory …), and they are
 *  the actual content. A block that pushes them out of the visible area harms
 *  more than the numbers help — they should be readable in passing, not take
 *  over the page.
 *
 *  Without metrics in KPIS.md the row hides itself entirely: an empty box on
 *  every agent page would be noise, and the note on how to define metrics
 *  stands on the cost page. */
export function Performance({ agentId }: { agentId: string }) {
  const rep = useQuery({
    queryKey: ["cost", "indicators", agentId, 30],
    queryFn: () => api<IndicatorReport>(`/agents/${agentId}/cost/indicators?days=30`),
    // The metrics of ONE agent follow the work record (spec/21): controlling
    // gets a 403 here. No retry and no error text — a role that may not see
    // something should not be shown it as broken, but should be shown
    // nothing at all.
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
