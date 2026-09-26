import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  createNote,
  deleteNote,
  listNotes,
  summarizeNote,
  updateNote,
  uploadNoteMedia,
  type Note,
  type NoteChange,
} from "../api";
import { Markdown } from "../components/Markdown";
import BlockEditor from "../notes/BlockEditor";

/* The notetaker in the web (#342): the same notes as in the app (#336) —
 * the signed-in seat's own, private — edited the way the app edits them
 * (#386): a page with an icon, a cover and properties, a body of blocks,
 * saved as one types; a list and a board to find them.
 *
 * What the web does not do is record. Browsers' speech recognition sends
 * the audio to a cloud service, and the notetaker's promise is that speech
 * is recognised on the device. Voice notes and meetings are recorded in the
 * app and read and edited here.
 *
 * The strings are the notetaker's keys (mobile.*), shared with the app: the
 * same thing has the same words on both surfaces (spec/27). */

type View = "list" | "board";
const STATUSES = ["", "todo", "doing", "done"] as const;
type Status = (typeof STATUSES)[number];

/* The app's page icons and covers (mobile/lib/screens/notes.dart). */
const PAGE_ICONS = [
  "📝", "📌", "📎", "📚", "📅", "🗓️", "✅", "💡", "🎯", "🚀", "⭐", "🔥",
  "📈", "📊", "💼", "🏢", "🤝", "💬", "📣", "🧭", "🛠️", "⚙️", "🧪", "🔒",
  "🏠", "🌱", "🌍", "✈️", "🎓", "❤️", "🙂", "🎉", "🍀", "☕", "🐞", "🧾",
];
const GRADIENTS: Record<string, string> = {
  clay: "linear-gradient(90deg, #cc7a5b, #edc4a3)",
  dusk: "linear-gradient(90deg, #3b2e5a, #c77d8a)",
  sea: "linear-gradient(90deg, #1f4e6b, #6fb3b8)",
  moss: "linear-gradient(90deg, #3c5a3a, #a3b86c)",
  sand: "linear-gradient(90deg, #d9c3a0, #f3e7d3)",
  night: "linear-gradient(90deg, #0f1a2b, #34495e)",
};

/* A note's pictures live in the media store and are served to their owner
   (#344); anything else is not loaded. */
const noteMedia = (src: string) =>
  src.startsWith("covey-media://") ? `/api/v1/me/notes/media/${encodeURIComponent(src.slice("covey-media://".length))}` : null;

const heading = (n: Note) => n.title || n.body.split("\n")[0].replace(/^(#{1,3}\s+|[-*]\s+(\[[ xX]\]\s+)?|>\s?)/, "");

const readView = (): View => {
  try {
    return localStorage.getItem("covey.notes.view") === "board" ? "board" : "list";
  } catch {
    return "list";
  }
};

export default function Notes() {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [query, setQuery] = useState("");
  const [q, setQ] = useState("");
  // The open note: an id, "new" for one not yet written, or none.
  const [open, setOpen] = useState<string | null>(null);
  const [view, setViewState] = useState<View>(readView);
  const setView = (v: View) => {
    setViewState(v);
    try {
      localStorage.setItem("covey.notes.view", v);
    } catch {
      /* a private window: the choice lasts the visit */
    }
  };

  // A quarter second after the last key: one request per word.
  useEffect(() => {
    const h = setTimeout(() => setQ(query), 250);
    return () => clearTimeout(h);
  }, [query]);

  const notes = useQuery({ queryKey: ["notes", q], queryFn: () => listNotes(q) });
  const list = notes.data?.notes ?? [];
  const current = open && open !== "new" ? (list.find((n) => n.id === open) ?? null) : null;

  const locale = i18n.language;
  const days = useMemo(() => groupByDay(list, locale, t), [list, locale, t]);

  const page =
    open === "new" || current ? (
      <NotePage
        key={open === "new" ? "new" : current!.id}
        note={current}
        canSummarize={!!notes.data?.summarize}
        onCreated={(n) => {
          setOpen(n.id);
          qc.invalidateQueries({ queryKey: ["notes"] });
        }}
        onDeleted={() => {
          setOpen(null);
          qc.invalidateQueries({ queryKey: ["notes"] });
        }}
      />
    ) : null;

  return (
    <div className={`notes view-${view}`}>
      <header className="notes-head">
        <h1 className="notes-h1">{t("mobile.notizen")}</h1>
        {/* Two quiet tabs, not a segmented control (#373). */}
        <nav className="notes-tabs" aria-label={t("mobile.notizen")}>
          {(["list", "board"] as const).map((v) => (
            <button key={v} type="button" className={view === v ? "on" : ""} aria-pressed={view === v} onClick={() => setView(v)}>
              {t(v === "list" ? "mobile.ansichtListe" : "mobile.ansichtBoard")}
            </button>
          ))}
        </nav>
        <span className="flex-1" />
        <input
          type="search"
          className="notes-search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("mobile.notizenSuchen")}
          aria-label={t("mobile.notizenSuchen")}
        />
        <button type="button" className="btn primary sm" onClick={() => setOpen("new")}>
          + {t("mobile.notizNeu")}
        </button>
      </header>

      {notes.isError && <p className="danger-text text-sm">{(notes.error as Error).message}</p>}

      {view === "list" ? (
        <div className="notes-layout">
          <div className="notes-list">
            {notes.isLoading && <p className="muted text-sm">{t("common.loading")}</p>}
            {notes.data && list.length === 0 && (
              <p className="muted text-sm">{q.trim() ? t("team.nichtsGefunden") : t("mobile.notizenLeer")}</p>
            )}
            {days.map((g) => (
              <section key={g.day} className="notes-day">
                <h2 className="notes-day-h">{g.day}</h2>
                {g.notes.map((n) => (
                  <button key={n.id} type="button" className={`notes-row${n.id === open ? " on" : ""}`} onClick={() => setOpen(n.id)}>
                    <span className="notes-row-icon" aria-hidden="true">
                      {n.icon || kindGlyph(n)}
                    </span>
                    <span className="notes-row-text">
                      <span className="notes-row-h">{heading(n) || t("mobile.ohneTitel")}</span>
                      <span className="notes-row-m">
                        {meta(n, locale, t, false)}
                        {n.status ? ` · ${t(`mobile.nstatus_${n.status}`)}` : ""}
                      </span>
                    </span>
                  </button>
                ))}
              </section>
            ))}
          </div>
          <div className="notes-detail">
            {page ?? (
              <div className="notes-empty">
                <p>{t("mobile.notizWaehlen")}</p>
                {/* Recording is the app's (#342): speech is recognised on the device. */}
                <p className="text-xs">{t("mobile.webHinweis")}</p>
              </div>
            )}
          </div>
        </div>
      ) : (
        <>
          <Board notes={list} open={open} onOpen={setOpen} />
          {page && (
            <div className="notes-drawer" role="dialog" aria-modal="false">
              <button type="button" className="notes-drawer-close" onClick={() => setOpen(null)} aria-label={t("team.abbrechen")}>
                ×
              </button>
              {page}
            </div>
          )}
        </>
      )}
    </div>
  );
}

const kindGlyph = (n: Note) => (n.kind === "meeting" ? "👥" : n.kind === "voice" ? "🎙️" : "📄");

function groupByDay(list: Note[], locale: string, t: (k: string) => string) {
  const out: { day: string; notes: Note[] }[] = [];
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const yesterday = new Date(today.getTime() - 86_400_000);
  for (const n of list) {
    const d = new Date(n.created_at);
    const day0 = new Date(d);
    day0.setHours(0, 0, 0, 0);
    const label =
      day0.getTime() === today.getTime()
        ? t("mobile.heute")
        : day0.getTime() === yesterday.getTime()
          ? t("team.gestern")
          : d.toLocaleDateString(locale, { day: "numeric", month: "long", year: "numeric" });
    const last = out[out.length - 1];
    if (last && last.day === label) last.notes.push(n);
    else out.push({ day: label, notes: [n] });
  }
  return out;
}

/* The board (#373): a column per status, a card per note; a card dragged
   into another column takes its status. */
function Board({ notes, open, onOpen }: { notes: Note[]; open: string | null; onOpen: (id: string) => void }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [over, setOver] = useState<Status | null>(null);
  const move = useMutation({
    mutationFn: ({ id, status }: { id: string; status: Status }) => updateNote(id, { status }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["notes"] }),
  });
  return (
    <div className="notes-board">
      {STATUSES.map((st) => {
        const cards = notes.filter((n) => (n.status ?? "") === st);
        return (
          <section
            key={st || "none"}
            className={`notes-col${over === st ? " over" : ""}`}
            onDragOver={(e) => {
              e.preventDefault();
              setOver(st);
            }}
            onDragLeave={() => setOver((o) => (o === st ? null : o))}
            onDrop={(e) => {
              e.preventDefault();
              setOver(null);
              const id = e.dataTransfer.getData("text/note");
              if (id) move.mutate({ id, status: st });
            }}
          >
            <h2 className="notes-col-h">
              <span className={`notes-dot st-${st || "none"}`} aria-hidden="true" />
              {t(`mobile.nstatus_${st || "none"}`)}
              <span className="notes-col-n">{cards.length}</span>
            </h2>
            {cards.map((n) => (
              <button
                key={n.id}
                type="button"
                draggable
                onDragStart={(e) => e.dataTransfer.setData("text/note", n.id)}
                className={`notes-card${n.id === open ? " on" : ""}`}
                onClick={() => onOpen(n.id)}
              >
                {n.cover && <span className="notes-card-cover" style={coverStyle(n.cover)} />}
                <span className="notes-card-h">
                  {n.icon && <span aria-hidden="true">{n.icon} </span>}
                  {heading(n) || t("mobile.ohneTitel")}
                </span>
                {(n.due || (n.tags ?? []).length > 0) && (
                  <span className="notes-card-m">
                    {n.due && <span>{new Date(n.due + "T00:00:00").toLocaleDateString(i18n.language, { day: "numeric", month: "short" })}</span>}
                    {(n.tags ?? []).map((tag) => (
                      <span key={tag} className="notes-tag">
                        {tag}
                      </span>
                    ))}
                  </span>
                )}
              </button>
            ))}
          </section>
        );
      })}
    </div>
  );
}

const coverStyle = (cover: string) => {
  const g = cover.startsWith("gradient:") ? GRADIENTS[cover.slice("gradient:".length)] : undefined;
  const src = g ? null : noteMedia(cover);
  return g ? { background: g } : src ? { backgroundImage: `url(${src})` } : {};
};

function duration(s: number) {
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const two = (v: number) => String(v).padStart(2, "0");
  return h > 0 ? `${h}:${two(m)}:${two(sec)}` : `${m}:${two(sec)}`;
}

/* The line under a note: kind, time (with the date where the day heading
   does not already say it), and for a meeting how long it ran. */
function meta(n: Note, locale: string, t: (k: string) => string, withDate: boolean) {
  const d = new Date(n.created_at);
  const when = withDate
    ? d.toLocaleString(locale, { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" })
    : d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
  return [t(`mobile.art_${n.kind}`), when, n.kind === "meeting" && n.duration_seconds > 0 ? duration(n.duration_seconds) : ""]
    .filter(Boolean)
    .join(" · ");
}

/* A note as a page: cover, icon, title, properties, the body as blocks. It
   saves as one types, a moment after the last key; a note never written is
   created with its first word, since the instance keeps no empty note. */
function NotePage({
  note,
  canSummarize,
  onCreated,
  onDeleted,
}: {
  note: Note | null;
  canSummarize: boolean;
  onCreated: (n: Note) => void;
  onDeleted: () => void;
}) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [n, setN] = useState<Note | null>(note);
  const [title, setTitle] = useState(note?.title ?? "");
  const body = useRef(note?.body ?? "");
  const [state, setState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [picker, setPicker] = useState<"icon" | "cover" | null>(null);
  const [more, setMore] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const saving = useRef<Promise<unknown>>(Promise.resolve());
  // The note's id, once it has one: read inside the queue, where state is stale.
  const id = useRef(note?.id ?? "");
  // The change a timer still holds, sent at once when the page closes.
  const pending = useRef<(() => NoteChange) | null>(null);

  /* One save at a time, in order: a later one waits for the earlier, so the
     instance never ends with an older text than the page. */
  const save = useCallback(
    (change: NoteChange) => {
      saving.current = saving.current.then(async () => {
        setState("saving");
        try {
          let saved: Note;
          if (!id.current) {
            const text = (change.body ?? body.current).trim();
            // A slash command still being typed is not yet anything to keep.
            if (!text.replace(/(^|\s)\/\S*$/, "").trim()) return setState("idle");
            saved = await createNote(text, change.title ?? title);
            const rest: NoteChange = { ...change };
            delete rest.body;
            delete rest.title;
            if (Object.keys(rest).length) saved = await updateNote(saved.id, rest);
            id.current = saved.id;
            setN(saved);
            onCreated(saved);
          } else {
            if (change.body !== undefined && !change.body.trim()) delete change.body;
            if (!Object.keys(change).length) return setState("idle");
            saved = await updateNote(id.current, change);
            setN(saved);
            qc.setQueriesData<{ notes: Note[] }>({ queryKey: ["notes"] }, (d) =>
              d ? { ...d, notes: d.notes.map((x) => (x.id === saved.id ? saved : x)) } : d,
            );
          }
          setState("saved");
        } catch {
          setState("error");
        }
      });
    },
    [title, onCreated, qc],
  );

  const later = (change: () => NoteChange) => {
    clearTimeout(timer.current);
    setState("saving");
    // Title and body wait together: the later change keeps the earlier one.
    const before = pending.current;
    const both = () => ({ ...(before ? before() : {}), ...change() });
    pending.current = both;
    timer.current = setTimeout(() => {
      pending.current = null;
      save(both());
    }, 700);
  };
  // What is still waiting goes out when the page closes.
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(
    () => () => {
      clearTimeout(timer.current);
      if (pending.current) saveRef.current(pending.current());
    },
    [],
  );

  const summarize = useMutation({
    mutationFn: () => summarizeNote((n ?? note)!.id),
    onSuccess: (s) => {
      setN(s);
      qc.invalidateQueries({ queryKey: ["notes"] });
    },
  });
  const remove = useMutation({ mutationFn: () => deleteNote((n ?? note)!.id), onSuccess: onDeleted });

  const cur = n ?? note;
  const cover = cur?.cover ?? "";
  const icon = cur?.icon ?? "";
  const exists = !!cur?.id;

  return (
    <article className="np">
      {cover ? (
        <div className="np-cover" style={coverStyle(cover)}>
          <button type="button" className="np-cover-btn" onClick={() => setPicker(picker === "cover" ? null : "cover")}>
            {t("mobile.coverHinzu")}
          </button>
        </div>
      ) : null}
      <div className={`np-inner${cover ? " with-cover" : ""}`}>
        {icon && (
          <button type="button" className="np-icon" onClick={() => setPicker(picker === "icon" ? null : "icon")} aria-label={t("mobile.iconHinzu")}>
            {icon}
          </button>
        )}
        {/* What a page can still get, shown where it will appear — quiet
            until the pointer is on the page. */}
        <div className="np-adds">
          {!icon && exists && (
            <button type="button" onClick={() => setPicker("icon")}>
              ☺ {t("mobile.iconHinzu")}
            </button>
          )}
          {!cover && exists && (
            <button type="button" onClick={() => setPicker("cover")}>
              ▭ {t("mobile.coverHinzu")}
            </button>
          )}
          <span className="flex-1" />
          <span className={`np-state ${state}`} aria-live="polite">
            {state === "saving" ? t("mobile.speichert") : state === "saved" ? t("mobile.gespeichert") : state === "error" ? t("mobile.nichtGespeichert") : ""}
          </span>
          {exists && (
            <span className="np-more-wrap">
              <button type="button" className="np-more" onClick={() => setMore((m) => !m)} aria-label={t("mobile.notizMenue")}>
                ⋯
              </button>
              {more && (
                <div className="nb-menu np-menu" onMouseLeave={() => setMore(false)}>
                  {canSummarize && cur!.kind !== "text" && (
                    <button
                      type="button"
                      onClick={() => {
                        setMore(false);
                        summarize.mutate();
                      }}
                    >
                      {cur!.summary ? t("mobile.zusammenfassenNeu") : t("mobile.zusammenfassen")}
                    </button>
                  )}
                  <button
                    type="button"
                    className="danger"
                    onClick={() => {
                      setMore(false);
                      setConfirming(true);
                    }}
                  >
                    {t("mobile.loeschen")}
                  </button>
                </div>
              )}
            </span>
          )}
        </div>

        {picker === "icon" && (
          <div className="np-picker" onMouseLeave={() => setPicker(null)}>
            {PAGE_ICONS.map((em) => (
              <button
                key={em}
                type="button"
                onClick={() => {
                  setPicker(null);
                  save({ icon: em });
                }}
              >
                {em}
              </button>
            ))}
            {icon && (
              <button type="button" className="np-picker-remove" onClick={() => (setPicker(null), save({ icon: "" }))}>
                {t("mobile.iconEntfernen")}
              </button>
            )}
          </div>
        )}
        {picker === "cover" && (
          <div className="np-picker np-covers" onMouseLeave={() => setPicker(null)}>
            {Object.entries(GRADIENTS).map(([name, g]) => (
              <button
                key={name}
                type="button"
                className="np-swatch"
                style={{ background: g }}
                aria-label={name}
                onClick={() => {
                  setPicker(null);
                  save({ cover: `gradient:${name}` });
                }}
              />
            ))}
            {cover && (
              <button type="button" className="np-picker-remove" onClick={() => (setPicker(null), save({ cover: "" }))}>
                {t("mobile.coverEntfernen")}
              </button>
            )}
          </div>
        )}

        <input
          className="np-title"
          value={title}
          placeholder={t("mobile.ohneTitel")}
          maxLength={200}
          onChange={(e) => {
            const v = e.target.value;
            setTitle(v);
            later(() => ({ title: v }));
          }}
        />
        {cur && <p className="np-meta">{meta(cur, i18n.language, t, true)}</p>}
        {exists && <Properties note={cur!} onChange={save} />}

        {confirming && (
          <div className="np-confirm">
            <span>{t("mobile.loeschenFrage")}</span>
            <button type="button" className="btn sm danger" onClick={() => remove.mutate()} disabled={remove.isPending}>
              {t("mobile.loeschen")}
            </button>
            <button type="button" className="btn sm" onClick={() => setConfirming(false)}>
              {t("team.abbrechen")}
            </button>
          </div>
        )}

        {summarize.isPending && <p className="muted text-sm">{t("common.loading")}</p>}
        {cur?.summary && (
          <div className="np-summary">
            <Markdown text={cur.summary} />
          </div>
        )}
        {cur && cur.kind !== "text" && <h3 className="np-section">{t("mobile.transkript")}</h3>}

        <BlockEditor
          initial={note?.body ?? ""}
          placeholder={t("mobile.notizHinweis")}
          resolveImage={noteMedia}
          upload={async (f) => (await uploadNoteMedia(f)).ref}
          onChange={(md) => {
            body.current = md;
            later(() => ({ body: md }));
          }}
        />
      </div>
    </article>
  );
}

/* A note's properties (#373): status, date and tags, set in place. */
function Properties({ note, onChange }: { note: Note; onChange: (c: NoteChange) => void }) {
  const { t } = useTranslation();
  const [tag, setTag] = useState("");
  const tags = note.tags ?? [];
  const addTag = () => {
    const v = tag.trim().replace(/^#/, "");
    setTag("");
    if (v && !tags.includes(v)) onChange({ tags: [...tags, v] });
  };
  return (
    <div className="np-props">
      <label className="np-prop">
        <span className="np-prop-k">{t("mobile.propStatus")}</span>
        <select
          className={`np-status st-${note.status || "none"}`}
          value={note.status ?? ""}
          onChange={(e) => onChange({ status: e.target.value as Status })}
        >
          {STATUSES.map((s) => (
            <option key={s || "none"} value={s}>
              {t(`mobile.nstatus_${s || "none"}`)}
            </option>
          ))}
        </select>
      </label>
      <label className="np-prop">
        <span className="np-prop-k">{t("mobile.propDatum")}</span>
        <input type="date" value={note.due ?? ""} onChange={(e) => onChange({ due: e.target.value })} />
      </label>
      <div className="np-prop">
        <span className="np-prop-k">{t("mobile.propTags")}</span>
        <span className="np-tags">
          {tags.map((tg) => (
            <span key={tg} className="notes-tag">
              {tg}
              <button type="button" aria-label={`${t("mobile.entfernen")}: ${tg}`} onClick={() => onChange({ tags: tags.filter((x) => x !== tg) })}>
                ×
              </button>
            </span>
          ))}
          <input
            className="np-tag-input"
            value={tag}
            placeholder={tags.length ? "" : t("mobile.tagHinweis")}
            onChange={(e) => setTag(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === ",") {
                e.preventDefault();
                addTag();
              }
            }}
            onBlur={addTag}
            maxLength={30}
          />
        </span>
      </div>
    </div>
  );
}
