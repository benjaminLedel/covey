import { Fragment, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, post, type Agent, type ChatEntry, type Principal } from "../api";
import { Markdown } from "../components/Markdown";
import { canManage } from "../pages/agent/roles";

/* Der Verlauf mit einem Agenten.
 *
 * Zwei Entscheidungen tragen ihn, und beide sind aus dem ersten Entwurf
 * gelernt:
 *
 * 1. **Die Antwort steht an der Frage, nicht am Eingabefeld.** Vorher hat
 *    jede parkende Aufgabe das Feld unten gekapert: Wer neue Arbeit übergeben
 *    wollte, während irgendein Heartbeat-Lauf auf eine Rückfrage wartete,
 *    beantwortete stattdessen diese Rückfrage — und die gemeinte Arbeit
 *    entstand nie. Jetzt trägt jede offene Frage ihr eigenes Feld, und das
 *    Feld unten legt immer eine neue Aufgabe an. Zwei Absichten, zwei Orte.
 *
 * 2. **Der Verlauf ist nach Vorgang gegliedert.** Ein Agent bekommt Arbeit aus
 *    dem Chat, aus einem Webhook, aus seinem Takt; alles davon steht hier
 *    nebeneinander. Ohne Trennlinie liest sich das als ein einziges
 *    durcheinandergeredetes Gespräch. Mit ihr ist es, was es ist: mehrere
 *    Vorgänge, jeder mit Anfang und Ende.
 */

const vomMenschen = (e: ChatEntry) => e.kind === "message" || e.author.startsWith("human:");

/** Die Person hinter einer Herkunft oder einem Verfasser ("chat:a@b" → "a@b"). */
const wer = (author: string) => author.split(":").slice(1).join(":") || author;

/** Woher die Arbeit kam, wenn nicht aus dem Chat: webhook:zammad → zammad. */
const herkunft = (author: string) => (author.startsWith("chat:") ? "" : author.split(":")[0]);

/* Ein Verlauf ohne Uhrzeiten ist eine Liste von Sätzen. Die Uhrzeit steht an
   jedem Eintrag, das Datum nur dort, wo es sich ändert — sonst liest man
   dreißigmal denselben Tag. */
const uhr = (iso: string, lang: string) =>
  new Date(iso).toLocaleTimeString(lang, { hour: "2-digit", minute: "2-digit" });
const tag = (iso: string) => new Date(iso).toDateString();
const tagName = (iso: string, lang: string) => {
  const d = new Date(iso);
  const heute = new Date();
  const gestern = new Date(heute);
  gestern.setDate(heute.getDate() - 1);
  if (d.toDateString() === heute.toDateString()) return null; // „heute" steht nicht dran
  if (d.toDateString() === gestern.toDateString()) return "gestern";
  return d.toLocaleDateString(lang, { day: "numeric", month: "long", year: "numeric" });
};

export default function Thread({ agentId, me }: { agentId: string; me: Principal }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [antwortAuf, setAntwortAuf] = useState<string | null>(null);
  const [antwort, setAntwort] = useState("");
  const [suche, setSuche] = useState("");
  const ende = useRef<HTMLDivElement>(null);

  const agent = useQuery({
    queryKey: ["agent", agentId],
    queryFn: () => api<Agent>(`/agents/${agentId}`),
  });
  const thread = useQuery({
    queryKey: ["thread", agentId],
    queryFn: () => api<{ entries: ChatEntry[] }>(`/agents/${agentId}/thread`),
    /* Zehn Sekunden: Der Agent antwortet, während jemand auf die Seite sieht,
       und einen Push dafür gibt es nicht. */
    refetchInterval: 10_000,
  });

  const alle = thread.data?.entries ?? [];
  /* Die Suche filtert, was geladen ist — die letzten zwanzig Vorgänge. Was
     älter ist, liegt im Backlog und wird dort gesucht; der Verweis daneben
     sagt das, statt so zu tun, als sei dies eine Volltextsuche. */
  const begriff = suche.trim().toLowerCase();
  const entries = begriff
    ? alle.filter((e) => (e.text + " " + e.task_title).toLowerCase().includes(begriff))
    : alle;
  const darfSchreiben = canManage(me.Role);

  const neu = useMutation({
    mutationFn: (nachricht: string) => post(`/agents/${agentId}/messages`, { text: nachricht }),
    onSuccess: () => {
      setText("");
      qc.invalidateQueries({ queryKey: ["thread", agentId] });
    },
  });

  const beantworten = useMutation({
    mutationFn: ({ taskId, text }: { taskId: string; text: string }) =>
      post<{ woken: boolean }>(`/tasks/${taskId}/reply`, { text }),
    onSuccess: () => {
      setAntwortAuf(null);
      setAntwort("");
      qc.invalidateQueries({ queryKey: ["thread", agentId] });
      qc.invalidateQueries({ queryKey: ["inbox"] });
    },
  });

  useEffect(() => {
    ende.current?.scrollIntoView?.({ block: "end" });
  }, [entries.length]);

  const abschicken = () => {
    const n = text.trim();
    if (n && !neu.isPending) neu.mutate(n);
  };

  return (
    <div className="ws-thread">
      <header className="ws-thread-kopf">
        <div>
          <h1>{agent.data?.display_name ?? "…"}</h1>
          <p>{agent.data?.job_title || agent.data?.slug}</p>
        </div>
        <div className="ws-thread-werkzeuge">
          <input
            type="search"
            value={suche}
            onChange={(e) => setSuche(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && setSuche("")}
            placeholder={t("workspace.imVerlaufSuchen")}
            aria-label={t("workspace.imVerlaufSuchen")}
          />
          {/* Der eine Weg von hier in die Konsole: an dem Agenten, den man
              gerade vor sich hat. */}
          <Link to={`/agents/${agentId}`} className="ws-thread-verwaltung">
            {t("workspace.imAdmin")}
          </Link>
        </div>
      </header>

      <div className="ws-verlauf">
        {thread.isLoading && <p className="ws-leise">{t("common.loading")}</p>}
        {!thread.isLoading && entries.length === 0 && !begriff && (
          <div className="ws-leer">
            <p>{t("workspace.leerTitel", { name: agent.data?.display_name ?? "" })}</p>
            <p className="ws-leise">{t("workspace.leerText")}</p>
          </div>
        )}
        {!thread.isLoading && entries.length === 0 && begriff && (
          <div className="ws-leer">
            <p>{t("workspace.nichtsGefunden")}</p>
            <Link to={`/agents/${agentId}?q=${encodeURIComponent(suche.trim())}`} className="ws-thread-verwaltung">
              {t("workspace.imBacklogSuchen")}
            </Link>
          </div>
        )}

        {entries.map((e, i) => {
          const neuerVorgang = i === 0 || entries[i - 1].task_id !== e.task_id;
          const neuerTag = i === 0 || tag(entries[i - 1].at) !== tag(e.at);
          const datum = neuerTag ? tagName(e.at, i18n.language) : null;
          const quelle = e.kind === "message" ? herkunft(e.author) : "";
          return (
            <Fragment key={`${e.task_id}-${e.kind}-${i}`}>
              {datum && <div className="ws-tag">{datum === "gestern" ? t("workspace.gestern") : datum}</div>}
              {neuerVorgang && (
                <div className="ws-vorgang">
                  <span className="ws-vorgang-linie" aria-hidden="true" />
                  <Link to={`/agents/${agentId}?task=${e.task_id}`} className="ws-vorgang-titel">
                    {e.task_title}
                  </Link>
                  {quelle && <span className="ws-vorgang-quelle">{t("workspace.quelle", { quelle })}</span>}
                  <span className="ws-vorgang-linie" aria-hidden="true" />
                </div>
              )}

              <article className={`ws-blase ${vomMenschen(e) ? "ich" : "er"} k-${e.kind}`}>
                <div className="ws-blase-kopf">
                  {vomMenschen(e) ? wer(e.author) : t(`chat.kind.${e.kind}`)}
                  <time dateTime={e.at}>{uhr(e.at, i18n.language)}</time>
                </div>
                <div className="ws-blase-text">
                  <Markdown text={e.text} />
                </div>

                {/* Die offene Frage trägt ihre Antwort selbst. */}
                {e.kind === "question" && e.task_state === "blocked" && darfSchreiben && (
                  <div className="ws-antwort">
                    {antwortAuf === e.task_id ? (
                      <>
                        <textarea
                          autoFocus
                          rows={2}
                          value={antwort}
                          onChange={(ev) => setAntwort(ev.target.value)}
                          onKeyDown={(ev) => {
                            if (ev.key === "Enter" && !ev.shiftKey) {
                              ev.preventDefault();
                              if (antwort.trim()) beantworten.mutate({ taskId: e.task_id, text: antwort.trim() });
                            }
                            if (ev.key === "Escape") setAntwortAuf(null);
                          }}
                          aria-label={t("chat.placeholderAnswer")}
                          placeholder={t("chat.placeholderAnswer")}
                        />
                        <div className="ws-antwort-knoepfe">
                          <button
                            className="btn primary sm"
                            disabled={!antwort.trim() || beantworten.isPending}
                            onClick={() => beantworten.mutate({ taskId: e.task_id, text: antwort.trim() })}
                          >
                            {t("chat.answer")}
                          </button>
                          <button className="btn sm" onClick={() => setAntwortAuf(null)}>
                            {t("workspace.abbrechen")}
                          </button>
                        </div>
                      </>
                    ) : (
                      <button className="btn sm" onClick={() => setAntwortAuf(e.task_id)}>
                        {t("chat.answer")}
                      </button>
                    )}
                  </div>
                )}
              </article>
            </Fragment>
          );
        })}
        <div ref={ende} />
      </div>

      {/* Das Feld unten legt immer eine neue Aufgabe an — nie eine Antwort. */}
      <div className="ws-eingabe">
        <textarea
          rows={2}
          value={text}
          onChange={(ev) => setText(ev.target.value)}
          onKeyDown={(ev) => {
            if (ev.key === "Enter" && !ev.shiftKey) {
              ev.preventDefault();
              abschicken();
            }
          }}
          disabled={!darfSchreiben}
          placeholder={darfSchreiben ? t("chat.placeholder") : t("chat.readOnly")}
          aria-label={t("chat.placeholder")}
        />
        <button className="btn primary" onClick={abschicken} disabled={!darfSchreiben || !text.trim() || neu.isPending}>
          {t("chat.send")}
        </button>
      </div>
      {(neu.isError || beantworten.isError) && (
        <p className="ws-fehler">{String(neu.error ?? beantworten.error)}</p>
      )}
    </div>
  );
}
