import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { createNote, deleteNote, listNotes, summarizeNote, type Note } from "../api";
import { Markdown } from "../components/Markdown";

/* The notetaker in the web (#342): the same notes as in the app (#336) —
 * the signed-in seat's own, private — in both shells, because notes belong
 * to the person, not to either view.
 *
 * What the web does not do is record. Browsers' speech recognition (the Web
 * Speech API in Chrome) sends the audio to a cloud service, and the
 * notetaker's promise is that speech is recognised on the device and no
 * audio leaves it. Voice notes and meetings are recorded in the app and read
 * here; the page says so where it offers the typed note.
 *
 * The strings are the notetaker's keys (mobile.*), shared with the app: the
 * same thing has the same words on both surfaces (spec/27). */
export default function Notes() {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [query, setQuery] = useState("");
  const [q, setQ] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [writing, setWriting] = useState(false);

  // A quarter second after the last key: one request per word.
  useEffect(() => {
    const h = setTimeout(() => setQ(query), 250);
    return () => clearTimeout(h);
  }, [query]);

  const notes = useQuery({ queryKey: ["notes", q], queryFn: () => listNotes(q) });
  const list = notes.data?.notes ?? [];
  const current = list.find((n) => n.id === selected) ?? null;

  const locale = i18n.language;
  const days = useMemo(() => {
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
  }, [list, locale, t]);

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2 flex-wrap">
        <h1 className="text-[22px]">{t("mobile.notizen")}</h1>
        <span className="spacer flex-1" />
        <button
          className="btn primary sm"
          onClick={() => {
            setWriting(true);
            setSelected(null);
          }}
        >
          {t("mobile.notizNeu")}
        </button>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 680 }}>
        {t("mobile.webHinweis")}
      </p>

      <div className="notes-layout">
        <div className="notes-list">
          <input
            type="search"
            className="notes-search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("mobile.notizenSuchen")}
            aria-label={t("mobile.notizenSuchen")}
          />
          {notes.isLoading && <p className="muted text-sm">{t("common.loading")}</p>}
          {notes.isError && <p className="danger-text text-sm">{(notes.error as Error).message}</p>}
          {notes.data && list.length === 0 && (
            <p className="muted text-sm">{q.trim() ? t("team.nichtsGefunden") : t("mobile.notizenLeer")}</p>
          )}
          {days.map((g) => (
            <section key={g.day} className="notes-day">
              <h2 className="notes-day-h">{g.day}</h2>
              <div className="notes-group">
                {g.notes.map((n) => (
                  <button
                    key={n.id}
                    className={`notes-row${n.id === selected ? " on" : ""}`}
                    onClick={() => {
                      setSelected(n.id);
                      setWriting(false);
                    }}
                  >
                    <span className="notes-row-h">{heading(n)}</span>
                    <span className="notes-row-m">{meta(n, locale, t, false)}</span>
                  </button>
                ))}
              </div>
            </section>
          ))}
        </div>

        <div className="notes-detail">
          {writing ? (
            <Editor
              onDone={(n) => {
                setWriting(false);
                qc.invalidateQueries({ queryKey: ["notes"] });
                if (n) setSelected(n.id);
              }}
            />
          ) : current ? (
            <Detail
              key={current.id}
              note={current}
              canSummarize={!!notes.data?.summarize}
              onDeleted={() => {
                setSelected(null);
                qc.invalidateQueries({ queryKey: ["notes"] });
              }}
            />
          ) : (
            <p className="muted text-sm">{t("mobile.waehlen")}</p>
          )}
        </div>
      </div>
    </div>
  );
}

const heading = (n: Note) => n.title || n.body.split("\n")[0].replace(/^(#{1,3}\s+|[-*]\s+(\[[ xX]\]\s+)?|>\s?)/, "");

/* A note's pictures live in the media store and are served to their owner
   (#344); anything else is not loaded. */
const noteMedia = (src: string) =>
  src.startsWith("covey-media://") ? `/api/v1/me/notes/media/${encodeURIComponent(src.slice("covey-media://".length))}` : null;

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

function Detail({ note, canSummarize, onDeleted }: { note: Note; canSummarize: boolean; onDeleted: () => void }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const summarize = useMutation({
    mutationFn: () => summarizeNote(note.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["notes"] }),
  });
  const remove = useMutation({ mutationFn: () => deleteNote(note.id), onSuccess: onDeleted });
  const n = summarize.data ?? note;

  return (
    <article className="notes-page">
      <p className="muted text-xs mb-1 notes-tabular">{meta(n, i18n.language, t, true)}</p>
      <h2 className="notes-title">{heading(n)}</h2>

      {n.summary && (
        <div className="notes-summary">
          <Markdown text={n.summary} />
        </div>
      )}

      <div className="flex gap-2 flex-wrap mb-4">
        {/* Summaries are for what was spoken; a typed note is its own summary. */}
        {canSummarize && n.kind !== "text" && (
          <button className="btn sm" onClick={() => summarize.mutate()} disabled={summarize.isPending}>
            {summarize.isPending
              ? t("common.loading")
              : n.summary
                ? t("mobile.zusammenfassenNeu")
                : t("mobile.zusammenfassen")}
          </button>
        )}
        {confirming ? (
          <>
            <span className="text-sm">{t("mobile.loeschenFrage")}</span>
            <button className="btn sm danger" onClick={() => remove.mutate()} disabled={remove.isPending}>
              {t("mobile.loeschen")}
            </button>
            <button className="btn sm" onClick={() => setConfirming(false)}>
              {t("team.abbrechen")}
            </button>
          </>
        ) : (
          <button className="btn sm" onClick={() => setConfirming(true)}>
            {t("mobile.loeschen")}
          </button>
        )}
      </div>
      {summarize.isError && <p className="danger-text text-xs mb-2">{(summarize.error as Error).message}</p>}

      {n.kind !== "text" && <h3 className="notes-section-h">{t("mobile.transkript")}</h3>}
      {/* The body is Markdown (#344): the app's editor writes checklists,
          tables and pictures into it. */}
      <div className="notes-body">
        <Markdown text={n.body} baseLevel={3} resolveImage={noteMedia} />
      </div>
    </article>
  );
}

function Editor({ onDone }: { onDone: (n: Note | null) => void }) {
  const { t } = useTranslation();
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const save = useMutation({ mutationFn: () => createNote(body.trim(), title.trim()), onSuccess: (n) => onDone(n) });

  return (
    <div className="notes-page">
      <input
        className="notes-title-input"
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        placeholder={t("mobile.titelOptional")}
        maxLength={200}
      />
      <textarea
        className="notes-body-input"
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder={t("mobile.notizHinweis")}
        autoFocus
        rows={12}
      />
      {save.isError && <p className="danger-text text-xs mb-2">{(save.error as Error).message}</p>}
      <div className="flex gap-2">
        <button className="btn primary sm" disabled={!body.trim() || save.isPending} onClick={() => save.mutate()}>
          {t("mobile.speichern")}
        </button>
        <button className="btn sm" onClick={() => onDone(null)}>
          {t("team.abbrechen")}
        </button>
      </div>
    </div>
  );
}
