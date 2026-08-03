import { useState, useEffect, useMemo, useCallback } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";
import {
  api,
  post,
  patch,
  del,
  type MemoryEntry,
  type WikiLogEntry,
  type WikiHealth,
  type WikiFinding,
} from "../../api";
import { WikiOpIcon, WikiTypeIcon } from "../../components/WikiIcon";

import { Dreams, logDetail } from "./Dreams";
import { WikiGraph } from "./WikiGraph";
import { WIKI_SORTS, WIKI_SORT_KEY, WIKI_TYPES, WikiBody, WikiSort, linkContext, wikiPreview } from "./wiki";

export function Memories({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  // Offene Wiki-Seite lebt in der URL (?page=<slug>) — deep-linkbar, Browser-Zurück.
  const [sp, setSp] = useSearchParams();
  // Vier Sichten auf dasselbe Gedaechtnis: die Seiten, ihr Graph, das Protokoll
  // der Schreibvorgaenge — und die Traeume, in denen der Agent aufraeumt. Als
  // eigener Reiter stand "Traeume" gleichrangig neben "Gedaechtnis", obwohl es
  // nichts anderes zeigt als dessen Pflege.
  const [view, setView] = useState<"pages" | "graph" | "log" | "dreams">(
    sp.get("view") === "dreams" ? "dreams" : "pages",
  );
  const selected = sp.get("page");
  const setSelected = (slug: string | null) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        if (slug) n.set("page", slug);
        else n.delete("page");
        n.set("tab", "memory");
        return n;
      },
      { replace: false },
    );
  // Semantische Suche (spec/05, pgvector): Eingabe entprellt, dann Backend ?q=.
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const h = setTimeout(() => setDebounced(query.trim()), 250);
    return () => clearTimeout(h);
  }, [query]);
  // Volle Seitenliste — trägt Link-Auflösung (has), Backlinks, Baum und Graph.
  const mems = useQuery({
    queryKey: ["memories", agentId],
    queryFn: () => api<MemoryEntry[] | null>(`/agents/${agentId}/memories`),
  });
  const search = useQuery({
    queryKey: ["memories-search", agentId, debounced],
    queryFn: () => api<MemoryEntry[] | null>(`/agents/${agentId}/memories?q=${encodeURIComponent(debounced)}`),
    enabled: debounced.length > 0,
  });
  const log = useQuery({
    queryKey: ["wiki-log", agentId],
    queryFn: () => api<WikiLogEntry[] | null>(`/agents/${agentId}/wiki/log`),
    enabled: view === "log",
  });
  // Qualitätsbefunde (spec/05): was am Wiki verwahrlost, soll man sehen, ohne
  // es selbst nachzuzählen.
  const health = useQuery({
    queryKey: ["wiki-health", agentId],
    queryFn: () => api<WikiHealth>(`/agents/${agentId}/wiki/health`),
  });

  const [draft, setDraft] = useState("");
  const [draftTitle, setDraftTitle] = useState("");
  const [draftType, setDraftType] = useState("");
  const [editId, setEditId] = useState<string | null>(null);
  const [editTitle, setEditTitle] = useState("");
  const [editText, setEditText] = useState("");
  const [filter, setFilter] = useState<WikiFinding["kind"] | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<WikiSort>(() => {
    const saved = localStorage.getItem(WIKI_SORT_KEY) as WikiSort | null;
    return saved && WIKI_SORTS.includes(saved) ? saved : "recent";
  });
  const changeSort = (s: WikiSort) => {
    localStorage.setItem(WIKI_SORT_KEY, s);
    setSort(s);
  };
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["memories", agentId] });
    qc.invalidateQueries({ queryKey: ["wiki-log", agentId] });
    qc.invalidateQueries({ queryKey: ["wiki-health", agentId] });
  };

  const add = useMutation({
    mutationFn: () => post(`/agents/${agentId}/memories`, { content: draft, title: draftTitle, type: draftType }),
    onSuccess: () => {
      setDraft("");
      setDraftTitle("");
      setDraftType("");
      invalidate();
    },
  });
  const save = useMutation({
    mutationFn: () => patch(`/memories/${editId}`, { content: editText, title: editTitle }),
    onSuccess: () => {
      setEditId(null);
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/memories/${id}`),
    onSuccess: invalidate,
  });
  const list = useMemo(() => mems.data ?? [], [mems.data]);
  const logs = log.data ?? [];
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const opLabel = (op: string) =>
    ({
      ingest: t("agent.memory.opIngest"),
      write: t("agent.memory.opWrite"),
      append: t("agent.memory.opWrite"),
      merge: t("agent.memory.opMerge"),
      delete: t("agent.memory.opDelete"),
    })[op] ?? op;

  const bySlug = useMemo(() => new Map(list.map((p) => [p.slug, p])), [list]);

  // Protokoll nach Tagen gruppieren: 25 Zeitstempel untereinander liest niemand,
  // drei Tagesblöcke mit Uhrzeiten schon.
  const logDays = useMemo(() => {
    const today = new Date().toDateString();
    const yest = new Date(Date.now() - 86400000).toDateString();
    const out: { key: string; label: string; rows: WikiLogEntry[] }[] = [];
    logs.forEach((l) => {
      const d = new Date(l.created_at);
      const key = d.toDateString();
      let group = out.find((g) => g.key === key);
      if (!group) {
        const label =
          key === today
            ? t("agent.memory.today")
            : key === yest
              ? t("agent.memory.yesterday")
              : d.toLocaleDateString(locale, { day: "2-digit", month: "long", year: "numeric" });
        group = { key, label, rows: [] };
        out.push(group);
      }
      group.rows.push(l);
    });
    return out;
  }, [logs, locale, t]);
  const has = (slug: string) => bySlug.has(slug);
  const openPage = (slug: string) => {
    setView("pages");
    setEditId(null);
    setSelected(slug);
  };
  const current = selected ? (bySlug.get(selected) ?? null) : null;
  const backlinks = useMemo(
    () => (current ? list.filter((p) => p.id !== current.id && (p.links ?? []).includes(current.slug)) : []),
    [current, list],
  );
  const searching = debounced.length > 0;

  // Nachbarschaft der offenen Seite (sie selbst, ihre Ziele, ihre Rückverweise).
  // Memoisiert, weil der Graph bei neuer Array-Identität sein Layout verwirft
  // und die Simulation neu rechnet.
  const localPages = useMemo(() => {
    if (!current) return [];
    const names = new Set<string>([current.slug]);
    (current.links ?? []).forEach((l) => bySlug.has(l) && names.add(l));
    backlinks.forEach((b) => names.add(b.slug));
    return list.filter((p) => names.has(p.slug));
  }, [current, backlinks, list, bySlug]);

  // Verwaist = kein lebender Verweis hinein oder hinaus. Wird im Baum gedämpft
  // dargestellt; die Zahl steht in der Qualitätsleiste.
  const orphanSlugs = useMemo(() => {
    const inbound = new Set<string>();
    list.forEach((p) => (p.links ?? []).forEach((l) => bySlug.has(l) && inbound.add(l)));
    return new Set(
      list.filter((p) => !inbound.has(p.slug) && !(p.links ?? []).some((l) => bySlug.has(l))).map((p) => p.slug),
    );
  }, [list, bySlug]);

  // Grad einer Seite im Wiki-Graph — das einzige Relevanzsignal, das es gibt:
  // Zugriffe werden nirgends gezählt. Eingehende Verweise wiegen doppelt, denn
  // eine Seite, auf die andere zeigen, ist ein Knotenpunkt; eine, die nur selbst
  // viel verlinkt, ist bloß geschwätzig. Tote Verweise zählen nicht mit.
  const degree = useMemo(() => {
    const d = new Map<string, number>();
    list.forEach((p) => d.set(p.slug, 0));
    list.forEach((p) =>
      (p.links ?? []).forEach((l) => {
        if (!bySlug.has(l) || l === p.slug) return;
        d.set(l, (d.get(l) ?? 0) + 2);
        d.set(p.slug, (d.get(p.slug) ?? 0) + 1);
      }),
    );
    return d;
  }, [list, bySlug]);

  // Vergleicher für eine Baumebene. Gleichstand fällt immer auf „zuletzt
  // geändert" zurück — sonst wandern Seiten bei jedem Rendern umher.
  const sortPages = useCallback(
    (a: MemoryEntry, b: MemoryEntry) => {
      if (sort === "title") return (a.title || a.slug).localeCompare(b.title || b.slug, locale);
      if (sort === "relevance") {
        const d = (degree.get(b.slug) ?? 0) - (degree.get(a.slug) ?? 0);
        if (d !== 0) return d;
      }
      return (b.updated_at || b.created_at).localeCompare(a.updated_at || a.created_at);
    },
    [sort, degree, locale],
  );

  // Auf einen Befund gefilterte Seitenmenge.
  const filtered = useMemo(() => {
    if (!filter) return null;
    const slugs = new Set((health.data?.findings ?? []).filter((f) => f.kind === filter).map((f) => f.slug));
    return slugs;
  }, [filter, health.data]);

  const typeLabel = (ty: string) =>
    ty === ""
      ? t("agent.memory.typeNone")
      : (
          {
            kunde: t("agent.memory.typeKunde"),
            projekt: t("agent.memory.typeProjekt"),
            system: t("agent.memory.typeSystem"),
            person: t("agent.memory.typePerson"),
            problem: t("agent.memory.typeProblem"),
            thema: t("agent.memory.typeThema"),
          } as Record<string, string>
        )[ty] ?? ty;

  const visible = useMemo(
    () => list.filter((p) => !filtered || filtered.has(p.slug)),
    [list, filtered],
  );

  // ── Baum: erste Ebene ist der Seitentyp, darunter die Seiten; eine Seite
  // lässt sich aufklappen und zeigt dann, worauf sie verweist. ────────────────
  const treeRow = (p: MemoryEntry, child: boolean) => {
    const kids = (p.links ?? [])
      .map((l) => bySlug.get(l))
      .filter((k): k is MemoryEntry => !!k && k.slug !== p.slug)
      .sort(sortPages);
    const isOpen = expanded.has(p.slug);
    return (
      <div key={p.slug + (child ? "-c" : "")}>
        <div className={`wiki-node${p.slug === selected ? " sel" : ""}${orphanSlugs.has(p.slug) ? " orphan" : ""}`}>
          <button
            type="button"
            className="tw"
            disabled={kids.length === 0 || child}
            aria-label={p.title || p.slug}
            onClick={() =>
              setExpanded((prev) => {
                const n = new Set(prev);
                if (n.has(p.slug)) n.delete(p.slug);
                else n.add(p.slug);
                return n;
              })
            }
          >
            {kids.length > 0 && !child ? (isOpen ? "▾" : "▸") : "·"}
          </button>
          {/* Der Tooltip zeigte nur den Inhalt — bei einem abgeschnittenen Titel
              ist aber der Titel das, was fehlt. Erst er, dann der Auszug. */}
          <button
            type="button"
            className="lbl"
            title={[p.title || p.slug, wikiPreview(p.content)].filter(Boolean).join("\n\n")}
            onClick={() => setSelected(p.slug)}
          >
            {p.title || p.slug}
          </button>
          {/* Nach Relevanz sortiert steht dort der Grad — eine Reihenfolge ohne
              sichtbaren Grund liest sich als Zufall. Sonst: ausgehende Verweise. */}
          {sort === "relevance"
            ? (degree.get(p.slug) ?? 0) > 0 && (
                <span className="cnt" title={t("agent.memory.sortDegreeHelp")}>
                  {degree.get(p.slug)}
                </span>
              )
            : kids.length > 0 && <span className="cnt">{kids.length}</span>}
        </div>
        {isOpen && !child && <div className="wiki-kids">{kids.map((k) => treeRow(k, true))}</div>}
      </div>
    );
  };

  const tree = WIKI_TYPES.map((ty) => {
    const items = visible.filter((p) => (p.type ?? "") === ty).sort(sortPages);
    if (items.length === 0) return null;
    return (
      <div className="wiki-group" key={ty || "none"}>
        <div className="wiki-group-h">
          <WikiTypeIcon type={ty} size={14} />
          <span>{typeLabel(ty)}</span>
          <span className="cnt">{items.length}</span>
        </div>
        {items.map((p) => treeRow(p, false))}
      </div>
    );
  });

  // ── Qualitätsleiste ────────────────────────────────────────────────────────
  const h = health.data;
  type QualityItem = { kind: WikiFinding["kind"]; n: number; label: string; help: string };
  const quality: QualityItem[] = h
    ? ([
        { kind: "orphan", n: h.orphans, label: t("agent.memory.qOrphans", { count: h.orphans }), help: t("agent.memory.qOrphansHelp") },
        { kind: "dead_link", n: h.dead_links, label: t("agent.memory.qDeadLinks", { count: h.dead_links }), help: t("agent.memory.qDeadLinksHelp") },
        { kind: "untyped", n: h.untyped, label: t("agent.memory.qUntyped", { count: h.untyped }), help: t("agent.memory.qUntypedHelp") },
        { kind: "episodic", n: h.episodic, label: t("agent.memory.qEpisodic", { count: h.episodic }), help: t("agent.memory.qEpisodicHelp") },
        { kind: "duplicate", n: h.duplicate, label: t("agent.memory.qDuplicate", { count: h.duplicate }), help: t("agent.memory.qDuplicateHelp") },
        { kind: "stub", n: h.stubs, label: t("agent.memory.qStubs", { count: h.stubs }), help: t("agent.memory.qStubsHelp") },
      ] as QualityItem[]).filter((q) => q.n > 0)
    : [];

  return (
    <div>
      {view !== "dreams" && <p className="muted text-[12.5px] mb-3">{t("agent.memory.hint")}</p>}
      <div className="flex items-center gap-2 mb-3">
        <div className="seg" role="tablist">
          <button className={view === "dreams" ? "active" : ""} onClick={() => setView("dreams")}>
            {t("agent.tabs.dreams")}
          </button>
          <button className={view === "pages" ? "active" : ""} onClick={() => setView("pages")}>
            {t("agent.memory.pages")}
            {list.length > 0 && ` (${list.length})`}
          </button>
          <button className={view === "graph" ? "active" : ""} onClick={() => setView("graph")}>
            {t("agent.memory.graph")}
          </button>
          <button className={view === "log" ? "active" : ""} onClick={() => setView("log")}>
            {t("agent.memory.log")}
          </button>
        </div>
        <span className="flex-1" />
      </div>

      {/* Qualitätsbefunde: Zahlen, die zugleich Filter sind. */}
      {h && list.length > 0 && view !== "dreams" && (
        <div className="wiki-quality mb-3">
          <span className="muted text-[11px] uppercase tracking-wide">{t("agent.memory.quality")}</span>
          {quality.length === 0 ? (
            <span className="chip ok">{t("agent.memory.qClean")}</span>
          ) : (
            quality.map((q) => (
              <button
                key={q.kind}
                type="button"
                title={q.help}
                className={`chip q${filter === q.kind ? " on" : ""}`}
                onClick={() => {
                  setFilter(filter === q.kind ? null : q.kind);
                  setView("pages");
                }}
              >
                {q.label}
              </button>
            ))
          )}
          {filter && (
            <button type="button" className="btn sm" onClick={() => setFilter(null)}>
              {t("agent.memory.filterClear")}
            </button>
          )}
        </div>
      )}

      {view === "dreams" ? (
        <Dreams agentId={agentId} canManage={canManage} />
      ) : view === "log" ? (
        <>
          {logs.length === 0 && <p className="muted">{t("agent.memory.logEmpty")}</p>}
          {logDays.map((day) => (
            <div key={day.key} className="wiki-log-day">
              <div className="wiki-log-day-h">{day.label}</div>
              <div className="card" style={{ padding: "2px 14px" }}>
                {day.rows.map((l) => {
                  // Seiten beim Namen nennen, wo es sie noch gibt — der rohe Slug
                  // ist bis zu 64 Zeichen lang und sagt weniger als der Titel.
                  const page = l.page_slug ? bySlug.get(l.page_slug) : undefined;
                  const name = page?.title || page?.slug || l.page_slug || "";
                  const extra = logDetail(l.summary, name);
                  // Gibt es die Seite nicht mehr, ist ihr Slug kryptisch und bis
                  // 64 Zeichen lang; dann trägt der Satz aus dem Protokoll mehr.
                  const primary = page ? name : extra || name;
                  const detail = page ? extra : "";
                  return (
                    <div key={l.id} className="wiki-log-row">
                      <span className="at">
                        {new Date(l.created_at).toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" })}
                      </span>
                      <span className={`op op-${l.op}`}>
                        <WikiOpIcon op={l.op} />
                        {opLabel(l.op)}
                      </span>
                      <span className="cell">
                        {page ? (
                          <button type="button" className="wikilink" onClick={() => openPage(page.slug)} title={page.slug}>
                            {name}
                          </button>
                        ) : (
                          <span className="wikilink missing" title={l.page_slug}>
                            {primary}
                          </span>
                        )}
                        {detail && (
                          <span className="detail" title={l.summary}>
                            {" · "}
                            {detail}
                          </span>
                        )}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </>
      ) : view === "graph" ? (
        <div className="card" style={{ padding: 0, overflow: "hidden" }}>
          <WikiGraph pages={visible} current={selected ?? undefined} onOpen={openPage} height={520} />
          <div className="wiki-legend">
            <span className="key">
              <span className="dot hub" /> {t("agent.memory.graphHub")}
            </span>
            <span className="key">
              <span className="dot" /> {t("agent.memory.graphLinked")}
            </span>
            <span className="key">
              <span className="dot orph" /> {t("agent.memory.graphOrphanKey")}
            </span>
            <span className="flex-1" />
            <span className="muted">{t("agent.memory.graphHint")}</span>
          </div>
        </div>
      ) : (
        // ── Arbeitsfläche: Baum | Seite | Kontext ──────────────────────────────
        <div className="wiki-panes">
          <div className="card wiki-pane" style={{ padding: "10px 12px" }}>
            <div className="wiki-search mb-2">
              <input type="search" placeholder={t("agent.memory.searchPlaceholder")} value={query} onChange={(e) => setQuery(e.target.value)} />
            </div>
            {/* Sortierung je Ebene. Bei der Suche ohne Wirkung — dort ordnet die
                semantische Ähnlichkeit, und die soll nichts überstimmen. */}
            {!searching && list.length > 0 && (
              <div className="wiki-sort mb-1">
                <select value={sort} onChange={(e) => changeSort(e.target.value as WikiSort)} aria-label={t("agent.memory.sortLabel")}>
                  <option value="recent">{t("agent.memory.sortRecent")}</option>
                  <option value="relevance">{t("agent.memory.sortRelevance")}</option>
                  <option value="title">{t("agent.memory.sortTitle")}</option>
                </select>
              </div>
            )}
            <div className="wiki-tree">
              {searching ? (
                (search.data ?? []).length === 0 && !search.isFetching ? (
                  <p className="muted text-[12.5px]">{t("agent.memory.searchEmpty")}</p>
                ) : (
                  (search.data ?? []).map((m) => (
                    <div key={m.id} className={`wiki-node${m.slug === selected ? " sel" : ""}`}>
                      <span className="tw">
                        <WikiTypeIcon type={m.type} size={13} />
                      </span>
                      <button type="button" className="lbl" onClick={() => setSelected(m.slug)}>
                        {m.title || m.slug}
                      </button>
                      {typeof m.score === "number" && <span className="cnt">{Math.round(m.score * 100)}%</span>}
                    </div>
                  ))
                )
              ) : list.length === 0 ? (
                <p className="muted text-[12.5px]">{t("agent.memory.nothingLearned")}</p>
              ) : (
                tree
              )}
            </div>
          </div>

          <div className="card wiki-pane" style={{ padding: 0 }}>
            {!current ? (
              <p className="muted text-[12.5px]" style={{ padding: "14px 16px" }}>
                {t("agent.memory.preview")}
              </p>
            ) : editId === current.id ? (
              <div style={{ padding: "12px 14px" }}>
                <input className="mb-2" value={editTitle} onChange={(e) => setEditTitle(e.target.value)} placeholder={t("agent.memory.titlePlaceholder")} />
                <textarea rows={10} value={editText} onChange={(e) => setEditText(e.target.value)} />
                <div className="flex items-center gap-2 mt-2">
                  <button className="btn primary sm" disabled={!editText.trim() || save.isPending} onClick={() => save.mutate()}>
                    {t("agent.memory.save")}
                  </button>
                  <button className="btn sm" onClick={() => setEditId(null)}>
                    {t("agent.memory.cancel")}
                  </button>
                  {save.isError && <span className="danger-text text-xs">{(save.error as Error).message}</span>}
                </div>
              </div>
            ) : (
              <div style={{ padding: "14px 18px 18px" }}>
                <div className="flex items-start justify-between gap-3 mb-1">
                  <span className="flex items-center gap-2 min-w-0 flex-wrap">
                    <span className="font-medium text-[16px]">{current.title || current.slug}</span>
                    <span className={`chip${current.type ? " is-fixed" : " q"}`} style={{ fontSize: "10px" }}>
                      <WikiTypeIcon type={current.type} size={12} />
                      {current.type ? typeLabel(current.type) : t("agent.memory.noType")}
                    </span>
                    {current.source && (
                      <span className="chip is-fixed" style={{ fontSize: "10px" }}>
                        {current.source === "manual" ? t("agent.memory.sourceManual") : t("agent.memory.sourceAgent")}
                      </span>
                    )}
                  </span>
                  <span className="muted text-[11px] shrink-0 flex items-center gap-2">
                    {new Date(current.updated_at || current.created_at).toLocaleDateString(locale)}
                    {canManage && (
                      <>
                        <button
                          className="btn sm"
                          onClick={() => {
                            setEditId(current.id);
                            setEditTitle(current.title ?? "");
                            setEditText(current.content);
                          }}
                        >
                          {t("agent.memory.change")}
                        </button>
                        <button
                          className="btn sm danger"
                          disabled={remove.isPending}
                          onClick={() => {
                            remove.mutate(current.id);
                            setSelected(null);
                          }}
                        >
                          {t("agent.memory.forget")}
                        </button>
                      </>
                    )}
                  </span>
                </div>
                <div className="slug mb-3">{current.slug}</div>
                <WikiBody text={current.content} has={has} onNav={openPage} />
              </div>
            )}
          </div>

          <div className="card wiki-pane" style={{ padding: "10px 12px" }}>
            {!current ? (
              canManage && (
                <>
                  <div className="muted text-[11px] mb-1.5">{t("agent.memory.remember")}</div>
                  <input className="mb-2" placeholder={t("agent.memory.titlePlaceholder")} value={draftTitle} onChange={(e) => setDraftTitle(e.target.value)} />
                  <select className="mb-2" value={draftType} onChange={(e) => setDraftType(e.target.value)}>
                    <option value="">{t("agent.memory.noType")}</option>
                    {WIKI_TYPES.filter((x) => x !== "").map((ty) => (
                      <option key={ty} value={ty}>
                        {typeLabel(ty)}
                      </option>
                    ))}
                  </select>
                  <textarea rows={4} placeholder={t("agent.memory.addPlaceholder")} value={draft} onChange={(e) => setDraft(e.target.value)} />
                  <button className="btn primary sm mt-2" disabled={!draft.trim() || add.isPending} onClick={() => add.mutate()}>
                    {t("agent.memory.remember")}
                  </button>
                  {add.isError && <span className="danger-text text-xs">{(add.error as Error).message}</span>}
                </>
              )
            ) : (
              <>
                <div className="muted text-[11px] mb-1.5">
                  {t("agent.memory.backlinks")} ({backlinks.length})
                </div>
                {backlinks.length === 0 ? (
                  <p className="muted text-[12.5px] mb-3">{t("agent.memory.noBacklinks")}</p>
                ) : (
                  <div className="mb-3">
                    {backlinks.map((b) => {
                      const ctx = linkContext(b.content, current.slug);
                      return (
                        <div key={b.id} className="wiki-ctx">
                          <button type="button" className="wikilink text-[12.5px]" onClick={() => openPage(b.slug)}>
                            {b.title || b.slug}
                          </button>
                          {ctx && <p className="wiki-quote">„{ctx}“</p>}
                        </div>
                      );
                    })}
                  </div>
                )}

                <div className="muted text-[11px] mb-1.5">
                  {t("agent.memory.outgoing")} ({(current.links ?? []).length})
                </div>
                <div className="flex flex-wrap gap-x-3 gap-y-1 mb-3">
                  {(current.links ?? []).length === 0 && <span className="muted text-[12.5px]">—</span>}
                  {(current.links ?? []).map((l) =>
                    has(l) ? (
                      <button key={l} type="button" className="wikilink text-[12.5px]" onClick={() => openPage(l)}>
                        {bySlug.get(l)?.title || l}
                      </button>
                    ) : (
                      <span key={l} className="wikilink missing text-[12.5px]" title={t("agent.memory.missing")}>
                        {l}
                      </span>
                    ),
                  )}
                </div>

                <div className="muted text-[11px] mb-1">{t("agent.memory.localGraph")}</div>
                {localPages.length < 2 ? (
                  <p className="muted text-[12.5px]">{t("agent.memory.graphOrphan")}</p>
                ) : (
                  <WikiGraph pages={localPages} current={current.slug} onOpen={openPage} height={140} labels={false} />
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
