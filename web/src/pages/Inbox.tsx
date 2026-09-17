import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import {
  api,
  decideApproval,
  decideImprovement,
  inbox,
  type Agent,
  type Approval,
  type ImprovementItem,
  type InboxEntry,
  type Principal,
} from "../api";
import { Markdown } from "../components/Markdown";
import { collapse, diffLines } from "../diff";
import { canManage } from "./agent/roles";

/* The inbox: everything that waits for the decision of a human.

   Two kinds sit here together because they need the same hand movement, not
   because they would be the same thing:

   - The APPROVAL (spec/06) — a guard rail tripped in the middle of an action,
     the task stands still, the agent WAITS.
   - The OPEN POINT (spec/21) — a review is finished, nothing blocks.
     Accepting writes a config version, rejecting keeps the reason.

   On top therefore sits a work queue and not a chronicle: what is open,
   approvals first, the oldest on top. Below the records grouped by kind,
   with a filter and reloadable — that is where you look when you search for
   something, not when you work things off. */

// tool_request: the request for a missing tool (#106). Like a finding — no
// diff, a human decides —, but with its own name, because the list "what the
// staff is missing at its workplaces" is read on its own.
const TYPES = ["approval", "proposal", "finding", "issue", "tool_request"] as const;
type EntryType = (typeof TYPES)[number];

export default function Inbox({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const [mine, setMine] = useState(false);
  const [agent, setAgent] = useState("");
  const [status, setStatus] = useState<"" | "open" | "decided">("");
  const [sort, setSort] = useState<"urgent" | "newest" | "oldest">("urgent");
  const [type, setType] = useState<"" | EntryType>("");
  const [headLimit, setHeadLimit] = useState(10);

  const scope = { mine: mine ? "1" : undefined, agent: agent || undefined };

  // The work queue. Its own query and not a filter over the list below:
  // it is sorted by urgency, the list by what
  // the searcher has set.
  const head = useQuery({
    queryKey: ["inbox", "todo", scope, headLimit],
    queryFn: () => inbox({ ...scope, status: "open", sort: "urgent", limit: headLimit }),
    refetchInterval: 20000,
  });

  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[] | null>("/agents"),
    staleTime: 60_000,
  });

  const offen = head.data?.pending ?? 0;
  const gezeigt = head.data?.items.length ?? 0;
  const sichtbareTypen = TYPES.filter(
    (x) => (type === "" || type === x) && (x === "approval" || me.Role !== "controlling"),
  );

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-1 flex-wrap">
        <h1 className="text-[22px]">{t("inbox.title")}</h1>
        <span className="muted">{t("inbox.open", { count: offen })}</span>
        <div className="flex items-center gap-3 ml-auto">
          <select value={agent} onChange={(e) => setAgent(e.target.value)} aria-label={t("inbox.filterAgent")}>
            <option value="">{t("inbox.allAgents")}</option>
            {(agents.data ?? []).map((a) => (
              <option key={a.id} value={a.id}>
                {a.display_name}
              </option>
            ))}
          </select>
          <label className="muted text-xs flex items-center gap-1">
            <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} />
            {t("inbox.onlyMine")}
          </label>
        </div>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 760 }}>
        {t("inbox.hint")}
      </p>

      <h2 className="text-base mb-2">{t("inbox.todo")}</h2>
      {head.isError && <p className="danger-text">{(head.error as Error).message}</p>}
      {!head.isLoading && gezeigt === 0 && <p className="muted mb-5">{t("inbox.nothingToDo")}</p>}
      {head.data?.items.map((e) => (
        <EntryCard key={`${e.type}:${e.id}`} entry={e} me={me} />
      ))}
      {gezeigt < offen && (
        <button className="btn sm mb-5" onClick={() => setHeadLimit((n) => n + 10)}>
          {t("inbox.more", { count: offen - gezeigt })}
        </button>
      )}

      <div className="flex items-center gap-3 mt-6 mb-3 flex-wrap">
        <h2 className="text-base">{t("inbox.all")}</h2>
        <select value={type} onChange={(e) => setType(e.target.value as "" | EntryType)} aria-label={t("inbox.filterType")}>
          <option value="">{t("inbox.allTypes")}</option>
          {TYPES.map((x) => (
            <option key={x} value={x}>
              {t(`inbox.type.${x}`)}
            </option>
          ))}
        </select>
        <select value={status} onChange={(e) => setStatus(e.target.value as "" | "open" | "decided")} aria-label={t("inbox.filterStatus")}>
          <option value="">{t("inbox.statusAll")}</option>
          <option value="open">{t("inbox.statusOpen")}</option>
          <option value="decided">{t("inbox.statusDecided")}</option>
        </select>
        <select value={sort} onChange={(e) => setSort(e.target.value as typeof sort)} aria-label={t("inbox.sort")}>
          <option value="urgent">{t("inbox.sortUrgent")}</option>
          <option value="newest">{t("inbox.sortNewest")}</option>
          <option value="oldest">{t("inbox.sortOldest")}</option>
        </select>
      </div>

      {sichtbareTypen.map((x) => (
        <TypeGroup key={x} type={x} me={me} scope={scope} status={status} sort={sort} />
      ))}
    </div>
  );
}

// TypeGroup is one kind in the listing — own query, own page.
// Refetching happens server-side: the number next to the heading is the
// total, not what was just downloaded.
function TypeGroup({
  type,
  me,
  scope,
  status,
  sort,
}: {
  type: EntryType;
  me: Principal;
  scope: Record<string, string | undefined>;
  status: string;
  sort: string;
}) {
  const { t } = useTranslation();
  const [limit, setLimit] = useState(5);
  const q = useQuery({
    queryKey: ["inbox", "group", type, scope, status, sort, limit],
    queryFn: () => inbox({ ...scope, type, status: status || undefined, sort, limit }),
  });
  const items = q.data?.items ?? [];
  const total = q.data?.total ?? 0;
  if (!q.isLoading && total === 0) return null;

  return (
    <section className="mb-5">
      <h3 className="text-sm secondary mb-2">
        {t(`inbox.type.${type}`)} <span className="muted">({total})</span>
      </h3>
      {items.map((e) => (
        <EntryRow key={`${e.type}:${e.id}`} entry={e} me={me} />
      ))}
      {items.length < total && (
        <button className="btn sm" onClick={() => setLimit((n) => n + 10)}>
          {t("inbox.more", { count: total - items.length })}
        </button>
      )}
    </section>
  );
}

// EntryRow is the narrow row of the listing. It opens into the full card —
// what you search for you find in the row; what you want to decide
// needs the diff below it.
function EntryRow({ entry, me }: { entry: InboxEntry; me: Principal }) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const locale = i18n.language === "de" ? "de-DE" : "en-US";

  if (open) {
    return (
      <div>
        <EntryCard entry={entry} me={me} onCollapse={() => setOpen(false)} />
      </div>
    );
  }
  return (
    <button className="inbox-row" onClick={() => setOpen(true)}>
      <span className={`badge state st-${entry.status}`}>{t(`inbox.status.${entry.status}`, entry.status)}</span>
      <span className="flex-1 min-w-0 truncate text-sm">{entry.title}</span>
      <span className="muted text-xs mono">{entry.agent_slug}</span>
      <span className="muted text-xs">{new Date(entry.created_at).toLocaleDateString(locale)}</span>
    </button>
  );
}

function EntryCard({ entry, me, onCollapse }: { entry: InboxEntry; me: Principal; onCollapse?: () => void }) {
  if (entry.type === "approval" && entry.approval) {
    return <ApprovalCard entry={entry} approval={entry.approval} me={me} onCollapse={onCollapse} />;
  }
  if (entry.item) return <ItemCard entry={entry} item={entry.item} me={me} onCollapse={onCollapse} />;
  return null;
}

// CardHead is the shared header line of both kinds: where from, about whom, when.
function CardHead({
  entry,
  kindLabel,
  kindClass,
  onCollapse,
}: {
  entry: InboxEntry;
  kindLabel: string;
  kindClass: string;
  onCollapse?: () => void;
}) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  return (
    <div className="flex items-baseline gap-2 mb-1 flex-wrap">
      <span className={`badge ${kindClass}`}>{kindLabel}</span>
      <strong className="text-sm">{entry.title}</strong>
      <Link to={`/agents/${entry.agent_id}`} className="muted text-xs">
        {entry.agent_name}
      </Link>
      <span className="muted text-xs ml-auto">{new Date(entry.created_at).toLocaleString(locale)}</span>
      {onCollapse && (
        <button className="btn sm" onClick={onCollapse}>
          {t("inbox.collapse")}
        </button>
      )}
    </div>
  );
}

// ApprovalCard: here an agent waits. That is why the action stands here with
// its parameters and not a summary — exactly this is what gets approved.
function ApprovalCard({
  entry,
  approval,
  me,
  onCollapse,
}: {
  entry: InboxEntry;
  approval: Approval;
  me: Principal;
  onCollapse?: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const decide = useMutation({
    mutationFn: ({ approve, text }: { approve: boolean; text?: string }) =>
      decideApproval(approval.id, approve, text),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["inbox"] }),
  });
  const darf = canManage(me.Role) || me.Role === "security";
  // The agent's own prose inside the parameters. Only a single passage can be
  // corrected in place — with two, it would not be clear which one the reviewer
  // rewrote, and a pair that names the wrong half teaches the wrong thing.
  const original = loneProse(approval.params);
  const [text, setText] = useState<string | null>(null);
  const changed = text !== null && text.trim() !== "" && text.trim() !== original.trim();

  return (
    <div className="card mb-2">
      <CardHead entry={entry} kindLabel={t("inbox.type.approval")} kindClass="kind-approval" onCollapse={onCollapse} />
      <div className="muted text-xs mb-2">{t("inbox.agentWaiting")}</div>
      {original && entry.pending && darf ? (
        <>
          {/* Correcting instead of only refusing. A reviewer who disliked one
              sentence had to send the agent back to guess — and the pair that
              falls out of an edit is what a voice learns from (spec/24). */}
          <textarea
            className="mb-1"
            rows={Math.min(14, Math.max(4, original.split("\n").length + 2))}
            style={{ width: "100%" }}
            value={text ?? original}
            onChange={(e) => setText(e.target.value)}
          />
          <div className="muted text-xs mb-2">{t("approvals.editHint")}</div>
        </>
      ) : (
        <pre className="diff-body mb-2" style={{ padding: "6px 10px" }}>
          {JSON.stringify(approval.params, null, 2)}
        </pre>
      )}
      {entry.pending && darf && (
        <div className="flex items-center gap-2">
          <button
            className="btn sm primary"
            disabled={decide.isPending}
            onClick={() => decide.mutate({ approve: true, text: changed ? (text as string) : undefined })}
          >
            {changed ? t("approvals.approveCorrected") : t("approvals.approve")}
          </button>
          <button className="btn sm danger" disabled={decide.isPending} onClick={() => decide.mutate({ approve: false })}>
            {t("approvals.deny")}
          </button>
          {decide.isError && <span className="danger-text text-xs">{(decide.error as Error).message}</span>}
        </div>
      )}
    </div>
  );
}

/* The one piece of prose in an action's parameters, or nothing.
 *
 * The server decides what counts as prose when it stores the pair; this is the
 * conservative half of the same question — exactly one long string, so the
 * field a reviewer edits is unambiguous. Everything else keeps the raw view. */
function loneProse(params: unknown): string {
  const found: string[] = [];
  const walk = (x: unknown) => {
    if (typeof x === "string") {
      if (x.trim().split(/\s+/).length >= 20) found.push(x);
      return;
    }
    if (Array.isArray(x)) return x.forEach(walk);
    if (x && typeof x === "object") return Object.values(x).forEach(walk);
  };
  walk(params);
  return found.length === 1 ? found[0] : "";
}

function ItemCard({
  entry,
  item,
  me,
  onCollapse,
}: {
  entry: InboxEntry;
  item: ImprovementItem;
  me: Principal;
  onCollapse?: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [note, setNote] = useState("");
  const [openDiff, setOpenDiff] = useState(false);
  const decide = useMutation({
    mutationFn: (accept: boolean) => decideImprovement(item.id, accept, note),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["inbox"] }),
  });

  const konflikt = (item.conflicts?.length ?? 0) > 0;
  // Who may decide: the depth of the proposal decides it, not the
  // click. If it touches ACCESS.md or EGRESS.md, security decides.
  const darfEntscheiden = canManage(me.Role) || me.Role === "security";
  const darfAnnehmen =
    darfEntscheiden &&
    !konflikt &&
    (!item.needs_security || me.Role === "org_admin" || me.Role === "security");

  return (
    <div className="card mb-2">
      <CardHead
        entry={entry}
        kindLabel={t(`inbox.type.${item.kind}`)}
        kindClass={`kind-${item.kind}`}
        onCollapse={onCollapse}
      />
      {item.author_name && (
        <div className="muted text-xs mb-2">{t("improvements.by", { name: item.author_name })}</div>
      )}

      {item.rationale && (
        <div className="text-sm mb-2" style={{ maxWidth: 780 }}>
          <Markdown text={item.rationale} />
        </div>
      )}

      {/* For the issue it says where the report already sits — otherwise every
          reader has to search for it. */}
      {item.link && (
        <a className="text-xs" href={item.link} target="_blank" rel="noreferrer">
          {item.link}
        </a>
      )}

      <div className="flex items-center gap-2 flex-wrap mb-2">
        {item.needs_security && <span className="badge st-pending">{t("improvements.needsSecurity")}</span>}
        {konflikt && (
          <span className="badge st-failed">{t("improvements.conflict", { files: item.conflicts!.join(", ") })}</span>
        )}
        {!konflikt && item.stale && (
          <span className="muted text-xs">
            {t("improvements.stale", { base: item.base_version, current: item.current_version })}
          </span>
        )}
        {item.status === "accepted" && item.applied_version > 0 && (
          <span className="muted text-xs">{t("improvements.appliedAs", { v: item.applied_version })}</span>
        )}
        {item.decision_note && <span className="muted text-xs">„{item.decision_note}"</span>}
      </div>

      {item.diff && item.diff.length > 0 && (
        <>
          <button className="assist-toggle mb-1" aria-expanded={openDiff} onClick={() => setOpenDiff((v) => !v)}>
            <span className="caret">▶</span>
            {t("improvements.showDiff", { files: item.diff.map((d) => d.file).join(", ") })}
          </button>
          {openDiff && item.diff.map((d) => <FileDiff key={d.file} file={d.file} before={d.before} after={d.after} />)}
        </>
      )}

      {entry.pending && darfEntscheiden && (
        <div className="flex items-center gap-2 mt-3 flex-wrap">
          <input
            placeholder={t("improvements.notePlaceholder")}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            style={{ width: 260 }}
          />
          <button
            className="btn sm primary"
            disabled={!darfAnnehmen || decide.isPending}
            title={darfAnnehmen ? undefined : t(konflikt ? "improvements.blockedConflict" : "improvements.blockedSecurity")}
            onClick={() => decide.mutate(true)}
          >
            {item.kind === "proposal" ? t("improvements.accept") : t("improvements.done")}
          </button>
          <button className="btn sm danger" disabled={decide.isPending} onClick={() => decide.mutate(false)}>
            {t("improvements.reject")}
          </button>
          {decide.isError && <span className="danger-text text-xs">{(decide.error as Error).message}</span>}
        </div>
      )}
    </div>
  );
}

// FileDiff shows the changed file line by line against the RUNNING state —
// what gets judged is the change that accepting produces.
function FileDiff({ file, before, after }: { file: string; before: string; after: string }) {
  const { t } = useTranslation();
  const chunks = collapse(diffLines(before, after));
  return (
    <div className="diff mb-2">
      <div className="diff-head mono">
        {file}
        {before === "" && <span className="muted"> · {t("improvements.newFile")}</span>}
      </div>
      <pre className="diff-body">
        {chunks.map((c, i) =>
          c.kind === "skip" ? (
            <span key={i} className="diff-skip">
              {t("improvements.skipped", { count: c.skipped })}
            </span>
          ) : (
            <span key={i} className={`diff-line ${c.kind}`}>
              {c.kind === "add" ? "+" : c.kind === "del" ? "−" : " "} {c.text}
            </span>
          ),
        )}
      </pre>
    </div>
  );
}
