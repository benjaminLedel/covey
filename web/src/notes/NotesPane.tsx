import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { listNotes } from "../api";
import { Board, NotePage, groupByDay, heading, kindGlyph, meta, useNotesView } from "../pages/Notes";

/* The notes in the team shell's three columns (#388): the list in the
 * middle column, the note or the board on the right. The open note is in
 * the address — /team/notes/<id>, /team/notes/new — so it can be linked
 * and the browser's back button goes where one expects. */

const useNotesQuery = (q: string) => useQuery({ queryKey: ["notes", q], queryFn: () => listNotes(q) });

export function NotesSidebar({ selected }: { selected: string | null }) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [view, setView] = useNotesView();
  const [query, setQuery] = useState("");
  const [q, setQ] = useState("");
  useEffect(() => {
    const h = setTimeout(() => setQ(query), 250);
    return () => clearTimeout(h);
  }, [query]);
  const notes = useNotesQuery(q);
  const list = notes.data?.notes ?? [];
  const days = useMemo(() => groupByDay(list, i18n.language, t), [list, i18n.language, t]);

  return (
    <div className="tm-spalte">
      <div className="tm-spalte-kopf">
        <h1 className="tm-spalte-titel">{t("mobile.notizen")}</h1>
        <button
          type="button"
          className="tm-spalte-plus"
          onClick={() => navigate("/team/notes/new")}
          title={t("mobile.notizNeu")}
          aria-label={t("mobile.notizNeu")}
        >
          +
        </button>
      </div>
      <nav className="notes-tabs tm-spalte-tabs" aria-label={t("mobile.notizen")}>
        {(["list", "board"] as const).map((v) => (
          <button key={v} type="button" className={view === v ? "on" : ""} aria-pressed={view === v} onClick={() => setView(v)}>
            {t(v === "list" ? "mobile.ansichtListe" : "mobile.ansichtBoard")}
          </button>
        ))}
      </nav>
      <input
        type="search"
        className="notes-search tm-spalte-suche"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={t("mobile.notizenSuchen")}
        aria-label={t("mobile.notizenSuchen")}
      />
      <div className="tm-spalte-liste">
        {notes.isLoading && <p className="tm-leise">{t("common.loading")}</p>}
        {notes.data && list.length === 0 && (
          <p className="tm-leise">{q.trim() ? t("team.nichtsGefunden") : t("mobile.notizenLeer")}</p>
        )}
        {days.map((g) => (
          <section key={g.day} className="notes-day">
            <h2 className="notes-day-h">{g.day}</h2>
            {g.notes.map((n) => (
              <button
                key={n.id}
                type="button"
                className={`notes-row${n.id === selected ? " on" : ""}`}
                onClick={() => navigate(`/team/notes/${n.id}`)}
              >
                <span className="notes-row-icon" aria-hidden="true">
                  {n.icon || kindGlyph(n)}
                </span>
                <span className="notes-row-text">
                  <span className="notes-row-h">{heading(n) || t("mobile.ohneTitel")}</span>
                  <span className="notes-row-m">
                    {meta(n, i18n.language, t, false)}
                    {n.status ? ` · ${t(`mobile.nstatus_${n.status}`)}` : ""}
                  </span>
                </span>
              </button>
            ))}
          </section>
        ))}
      </div>
    </div>
  );
}

export function NotesMain({ noteId }: { noteId: string | null }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [view] = useNotesView();
  const notes = useNotesQuery("");
  const list = notes.data?.notes ?? [];
  const current = noteId && noteId !== "new" ? (list.find((n) => n.id === noteId) ?? null) : null;

  const page =
    noteId === "new" || current ? (
      <NotePage
        key={noteId === "new" ? "new" : current!.id}
        note={current}
        canSummarize={!!notes.data?.summarize}
        onCreated={(n) => {
          qc.invalidateQueries({ queryKey: ["notes"] });
          navigate(`/team/notes/${n.id}`, { replace: true });
        }}
        onDeleted={() => {
          qc.invalidateQueries({ queryKey: ["notes"] });
          navigate("/team/notes");
        }}
      />
    ) : null;

  if (view === "board") {
    return (
      <div className="tm-notizen-seite">
        <Board notes={list} open={noteId} onOpen={(id) => navigate(`/team/notes/${id}`)} />
        {page && (
          <div className="notes-drawer" role="dialog" aria-modal="false">
            <button type="button" className="notes-drawer-close" onClick={() => navigate("/team/notes")} aria-label={t("team.abbrechen")}>
              ×
            </button>
            {page}
          </div>
        )}
      </div>
    );
  }
  return (
    <div className="tm-notizen-seite">
      {page ?? (
        <div className="notes-empty">
          <p>{t("mobile.notizWaehlen")}</p>
          {/* Recording is the app's (#342): speech is recognised on the device. */}
          <p className="text-xs">{t("mobile.webHinweis")}</p>
        </div>
      )}
    </div>
  );
}
