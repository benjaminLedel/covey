import { Fragment, lazy, Suspense, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useSearchParams } from "react-router";
import { PEOPLE_SLUG, api, inbox, isDraft, markThreadRead, post, upload, type Agent, type ChatEntry, type ChatMark, type InboxEntry, type Laufend, type Principal, type Verlauf } from "../api";
import { Markdown } from "../components/Markdown";
import Dauer from "../components/Dauer";
import { useSucheOeffnen } from "../components/Suche";
import { canManage } from "../pages/agent/roles";
import Gesicht from "../components/Gesicht";
import { NavIcon } from "../components/navicons";

const EntryCard = lazy(() => import("../pages/Inbox").then((m) => ({ default: m.EntryCard })));

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

/* Wer spricht — und das entscheidet die HERKUNFT, nicht die Art des Eintrags.
   
   Vorher stand jede Aufgabe rechts, weil `message` als „von mir" galt. Ein
   Agent bekommt seine Arbeit aber auch aus einem Webhook, einem Takt oder von
   einem Kollegen; das alles stand damit auf der Seite des Lesers, der es nie
   geschrieben hatte — und ein Trainingslauf von vierzig Zeilen sah aus wie
   eine Nachricht, die man selbst getippt hat. */
/* Wohin ein Anhang im Arbeitsplatz des Agenten landet. Ein eigener Ordner,
   damit das, was von außen hereingereicht wurde, nicht zwischen dem liegt,
   was der Agent selbst angelegt hat. */
const ANHANG_ORDNER = "eingang";

const vonMir = (e: ChatEntry) =>
  e.author.startsWith("chat:") || e.author.startsWith("manual:") || e.author.startsWith("human:");

/* Ein AUFTRAG ist Arbeit, die von woanders kam: ein Webhook, ein Takt, ein
   Trainingslauf, ein Kollege. Er stand bis hierher auf der Seite des Agenten
   und sah damit aus, als hätte der Agent vierzig Zeilen Anweisung gesagt —
   dabei ist es das, was ihm gesagt wurde. Er bekommt deshalb keine Blase,
   sondern die Form einer Notiz am Rand: mittig, leise, mit der Quelle davor. */
const istAuftrag = (e: ChatEntry) => e.kind === "message" && !vonMir(e);

/** Die Person hinter einer Herkunft oder einem Verfasser ("chat:a@b" → "a@b"). */
/** A message of one to a few emoji and nothing else, shown large (#396). */
const nurEmoji = (text: string) => /^(?:\p{Extended_Pictographic}|\p{Emoji_Modifier}|\p{Regional_Indicator}|\u200d|\uFE0F|\s){1,12}$/u.test(text.trim()) && /\p{Extended_Pictographic}|\p{Regional_Indicator}/u.test(text);

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
  const [vorgaengeOffen, setVorgaengeOffen] = useState(false);
  const sucheOeffnen = useSucheOeffnen();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [anhaenge, setAnhaenge] = useState<File[]>([]);
  const dateiwahl = useRef<HTMLInputElement>(null);
  const ende = useRef<HTMLDivElement>(null);

  const agent = useQuery({
    queryKey: ["agent", agentId],
    queryFn: () => api<Agent>(`/agents/${agentId}`),
    /* While a start is under way the phase changes every few seconds and no
       event announces it — the runner reports bytes, not states. So the
       thread asks, and only then. */
    refetchInterval: (q) => (q.state.data?.phase || q.state.data?.wake_trouble ? 4_000 : false),
  });
  const thread = useQuery({
    queryKey: ["thread", agentId],
    queryFn: () => api<Verlauf>(`/agents/${agentId}/thread`),
    /* Der Push kommt über den Ereignisstrom (App.tsx, chat.go). Die halbe
       Minute daneben ist kein Takt mehr, sondern das Netz darunter: für die
       Verbindung, die abgerissen ist, ohne es zu melden. */
    refetchInterval: 30_000,
  });

  /* What waits for a person about this agent, and what was decided (#391):
     the inbox's entries, in the conversation they belong to. Open ones stand
     as cards above the input; decided ones as a line where they happened. */
  const offenBeiMir = useQuery({
    queryKey: ["inbox", "agent-offen", agentId],
    queryFn: () => inbox({ agent: agentId, status: "open", sort: "oldest", limit: 50 }),
    refetchInterval: 20_000,
  });
  const entschieden = useQuery({
    queryKey: ["inbox", "agent-entschieden", agentId],
    queryFn: () => inbox({ agent: agentId, status: "decided", sort: "newest", limit: 50 }),
    staleTime: 30_000,
  });
  // controlling decides costs, nothing else (as in the inbox).
  const sichtbar = (x: { type: string }) => x.type === "approval" || me.Role !== "controlling";
  const offeneEntscheidungen = (offenBeiMir.data?.items ?? []).filter(sichtbar);
  const protokoll = (entschieden.data?.items ?? [])
    .filter(sichtbar)
    .filter((d) => d.decided_at)
    .sort((a, b) => Date.parse(a.decided_at!) - Date.parse(b.decided_at!));

  /* What is on the screen has been read (#385), up to the newest entry
     shown. The sidebar's badge goes with it. */
  const neuestes = (thread.data?.entries ?? []).reduce((m, e) => (!m || Date.parse(e.at) > Date.parse(m) ? e.at : m), "");
  useEffect(() => {
    if (!neuestes) return;
    markThreadRead(agentId, neuestes)
      .then(() => qc.invalidateQueries({ queryKey: ["threads"] }))
      .catch(() => {});
  }, [agentId, neuestes, qc]);

  const alle = thread.data?.entries ?? [];
  const entries = alle;
  const marken = thread.data?.marks ?? {};
  const vorgaenge = thread.data?.tasks ?? [];
  /* Der Schritt kommt aus derselben Abfrage, die der Grundriss macht — die
     Schale hält sie ohnehin warm. Ihn ein zweites Mal vom Server zu holen
     hieße, ihn ein zweites Mal zu bezahlen. */
  const laufendeVorgaenge = useQuery({
    queryKey: ["org-running"],
    queryFn: () => api<Laufend[] | null>("/org/running"),
    refetchInterval: 10_000,
  });
  const schrittZu = new Map((laufendeVorgaenge.data ?? []).map((l) => [l.task_id, l]));
  const darfSchreiben = canManage(me.Role);
  /* The People colleague drafts new colleagues (#327): a message to her is a
     brief, and the thread says so before the first one — and again when
     somebody arrives through the "hire a colleague" door, whatever the
     history. Drafting is hers; hiring is a click on the draft's page. */
  const istPeople = agent.data?.slug === PEOPLE_SLUG;
  const leitfaden = istPeople && (params.get("einstellen") === "1" || alle.length === 0);
  /* „Denkt nach" hat zwei Quellen, und beide braucht es.
   
     Der Server weiß es: Eine angenommene Nachricht ohne Entscheidung steht
     als `pending` im Verlauf — das übersteht ein Neuladen und gilt auch für
     jemand anderen, der demselben Agenten zusieht.
   
     Der Browser weiß es früher: zwischen dem Klick auf Senden und der Antwort
     des Servers liegt noch ein Weg, und in dieser Zeit soll die Blase schon
     stehen. Eine Blase, die eine halbe Sekunde zu spät kommt, sieht aus wie
     eine Oberfläche, die nicht mitbekommen hat, dass man etwas getan hat.
   
     Dazu die alte Ableitung: Ein Lauf, der wirklich arbeitet, ist auch
     „tippen" — nur ist er es jetzt zusätzlich und nicht mehr ersatzweise. */
  const laeuft = alle.length > 0 && alle[alle.length - 1].task_state === "in_progress";
  const tippt = thread.data?.pending || laeuft;
  /* Between "sent" and "working" lies the start: a cold one takes a minute,
     the first on a host fetches an image and takes longer. Silence there
     reads as "nothing happened". So the thread says "on it" as soon as the
     message is a task, names the phase the runner reports while the
     workplace is prepared, and says why when the wake fails — the platform
     speaking about itself, in a status line and not in the agent's voice. */
  const letzte = alle.length > 0 ? alle[alle.length - 1] : null;
  const angenommen = !!letzte && letzte.kind === "message" && !!letzte.task_id && letzte.task_state === "open";
  const phase = agent.data?.phase;
  const sorge = agent.data?.wake_trouble;
  const startet = !tippt && (angenommen || !!phase);
  const phaseText = (() => {
    if (!phase) return t("team.startetAllgemein");
    if (phase.phase === "image") {
      const mb = phase.bytes ? ` (${Math.round(phase.bytes / 1_048_576)} MB)` : "";
      return t("team.startetImage", { mb });
    }
    if (phase.phase === "home") return t("team.startetHome");
    return t("team.startetAllgemein");
  })();

  /* Ein Anhang ist kein Bild neben der Nachricht, sondern eine Datei im
     Arbeitsplatz des Agenten: Dort kann er sie öffnen, und nur dort nützt sie
     ihm. Hochgeladen wird deshalb in sein Heimverzeichnis unter `eingang/`,
     und die Nachricht sagt, was wo liegt — ein Verweis, den auch der Lauf
     später noch findet. */
  const neu = useMutation({
    mutationFn: async (nachricht: string) => {
      let voll = nachricht;
      if (anhaenge.length > 0) {
        const form = new FormData();
        for (const f of anhaenge) form.append("file", f, f.name);
        await upload(`/agents/${agentId}/files/upload?path=${encodeURIComponent(ANHANG_ORDNER)}`, form);
        const pfade = anhaenge.map((f) => `${ANHANG_ORDNER}/${f.name}`).join(", ");
        voll = `${nachricht}\n\n(${t("team.anhangZeile", { pfade })})`;
      }
      return post(`/agents/${agentId}/messages`, { text: voll });
    },
    /* Die eigene Nachricht steht im Verlauf, bevor der Server sie bestätigt
       hat. Das ist keine Beschönigung: Sie IST abgeschickt, und ob ein Chat
       sich anfühlt wie ein Chat, entscheidet sich genau hier — an dem
       Augenblick zwischen dem Tippen und dem Sehen.
   
       Scheitert das Abschicken, wird der vorherige Stand zurückgelegt und der
       Text steht wieder im Feld: nichts verschwindet stillschweigend. */
    onMutate: async (nachricht: string) => {
      await qc.cancelQueries({ queryKey: ["thread", agentId] });
      const vorher = qc.getQueryData<Verlauf>(["thread", agentId]);
      const anhangZeile =
        anhaenge.length > 0
          ? `\n\n(${t("team.anhangZeile", { pfade: anhaenge.map((f) => `${ANHANG_ORDNER}/${f.name}`).join(", ") })})`
          : "";
      qc.setQueryData<Verlauf>(["thread", agentId], (alt) => ({
        entries: [
          ...(alt?.entries ?? []),
          {
            kind: "message",
            id: `unterwegs-${Date.now()}`,
            task_title: "",
            task_state: "",
            author: `chat:${me.Email}`,
            text: nachricht + anhangZeile,
            at: new Date().toISOString(),
            unterwegs: true,
          } as ChatEntry,
        ],
        marks: alt?.marks ?? {},
        pending: true,
      }));
      setText("");
      setAnhaenge([]);
      return { vorher, nachricht };
    },
    onError: (_fehler, _nachricht, ctx) => {
      if (ctx?.vorher) qc.setQueryData(["thread", agentId], ctx.vorher);
      if (ctx?.nachricht) setText(ctx.nachricht);
    },
    /* In jedem Fall neu holen: Die eingefügte Zeile ist eine Behauptung, der
       Verlauf vom Server ist die Wahrheit. */
    onSettled: () => qc.invalidateQueries({ queryKey: ["thread", agentId] }),
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

  /* Ans Ende — außer, es wurde an eine Stelle gesprungen. Zwei Effekte, die
     gleichzeitig scrollen, enden dort, wo der zweite aufhört, und das wäre
     verlässlich der falsche Ort. */
  const springt = useRef(false);
  useEffect(() => {
    if (springt.current) return;
    ende.current?.scrollIntoView?.({ block: "end" });
  }, [entries.length]);

  const [blitz, setBlitz] = useState<string | null>(null);
  /* Hinspringen: an den Vorgang, nicht an die Zeile. Die Zeilen eines
     Vorgangs gehören zusammen, und wer eine Notiz sucht, will sehen, wozu sie
     gehört.
   
     Was nicht im Fenster liegt, gibt es hier nicht: Die Suche reicht über
     zweihundert Vorgänge, der Verlauf zeigt zwanzig. Dann führt der Sprung in
     die Verwaltung, wo auch der älteste Vorgang noch steht — eine Sackgasse
     wäre die einzige Antwort, die nicht in Frage kommt. */
  const hinspringen = (gruppe: string) => {
    const el = document.querySelector(`[data-gruppe="${CSS.escape(gruppe)}"]`);
    if (!el) {
      navigate(`/agents/${agentId}?task=${gruppe}`);
      return;
    }
    springt.current = true;
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    setBlitz(gruppe);
    setTimeout(() => {
      setBlitz(null);
      springt.current = false;
    }, 2400);
  };

  /* ?zu= kommt aus der Suche. Einmal ausgeführt, verschwindet es aus der
     Adresse: Ein Neuladen soll nicht wieder springen, und die Adresse eines
     Gesprächs ist das Gespräch, nicht die Stelle, über die man hineinkam. */
  useEffect(() => {
    const zu = params.get("zu");
    if (!zu || thread.isLoading) return;
    hinspringen(zu);
    const rest = new URLSearchParams(params);
    rest.delete("zu");
    setParams(rest, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params, thread.isLoading]);

  /* Reagieren. Die Antwort interessiert nicht — der Verlauf wird ohnehin neu
     geholt, und er ist die Wahrheit. */
  const reagieren = useMutation({
    mutationFn: ({ taskId, emoji }: { taskId: string; emoji: string }) =>
      post(`/tasks/${taskId}/reactions`, { emoji }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["thread", agentId] }),
  });

  const abschicken = () => {
    const n = text.trim();
    if ((n || anhaenge.length > 0) && !neu.isPending) neu.mutate(n);
  };

  return (
    <div className="tm-thread">
      <header className="tm-thread-kopf">
        {/* Who one talks to (#396): the face, the role and the state in
            words — not the slug, which is the address, not the colleague. */}
        <div className="tm-thread-wer">
          {agent.data && (
            <Gesicht
              schluessel={agent.data.slug}
              zustand={agent.data.killed ? "killed" : agent.data.status === "sleeping" ? "sleeping" : "working"}
              groesse={36}
            />
          )}
          <div>
            <h1>{agent.data?.display_name ?? "…"}</h1>
            <p>
              {[agent.data?.job_title || agent.data?.slug, agent.data && t(`status.${agent.data.killed ? "killed" : agent.data.status}`, "")]
                .filter(Boolean)
                .join(" · ")}
            </p>
          </div>
        </div>
        {/* Zwei Symbole statt eines Feldes und eines Satzes: Die Kopfzeile
            eines Verlaufs gehört dem, mit dem man spricht, und nicht den
            Werkzeugen. Das Feld klappt erst auf, wenn jemand sucht. */}
        <div className="tm-thread-werkzeuge">
          {/* Die Hintergrundvorgänge. Sie sind das, was nach einer Nachricht
              weitergeht, ohne dass etwas gesagt wird — und genau deshalb
              waren sie unsichtbar: Der Verlauf zeigt, was gesagt wurde, und
              ein Lauf, der seit einer Stunde arbeitet, hat seit einer Stunde
              nichts gesagt (#308). */}
          <button
            className={`tm-werkzeug tm-vorgaenge${vorgaenge.length > 0 ? " hat" : ""}${vorgaengeOffen ? " auf" : ""}`}
            onClick={() => setVorgaengeOffen((v) => !v)}
            title={t("team.hintergrund")}
            aria-label={t("team.hintergrund")}
            aria-expanded={vorgaengeOffen}
          >
            <NavIcon name="checklist" />
            {vorgaenge.length > 0 && <span className="tm-vorgaenge-zahl">{vorgaenge.length}</span>}
          </button>
          {/* Eine Lupe, nicht zwei: Sie macht die große Suche auf, schon auf
              diesen Kollegen markiert. Vorher hatte der Verlauf sein eigenes
              Feld, und dieselbe Frage hatte zwei Orte. */}
          <button
            className="tm-werkzeug"
            onClick={() => agent.data && sucheOeffnen(agent.data)}
            title={t("team.imVerlaufSuchen")}
            aria-label={t("team.imVerlaufSuchen")}
          >
            <NavIcon name="search" />
          </button>
          {/* Der eine Weg von hier in die Konsole: an dem Agenten, den man
              gerade vor sich hat. */}
          <Link
            to={`/agents/${agentId}`}
            className="tm-werkzeug"
            title={t("team.imAdmin")}
            aria-label={t("team.imAdmin")}
          >
            <NavIcon name="cog" />
          </Link>
        </div>
      </header>

      {vorgaengeOffen && (
        <section className="tm-vorgaenge-flaeche" aria-label={t("team.hintergrund")}>
          {vorgaenge.length === 0 ? (
            <p className="tm-leise">{t("team.hintergrundLeer")}</p>
          ) : (
            <ul>
              {vorgaenge.map((v) => {
                const l = schrittZu.get(v.id);
                return (
                  <li key={v.id}>
                    <button
                      className="tm-hg"
                      onClick={() => {
                        setVorgaengeOffen(false);
                        hinspringen(v.id);
                      }}
                    >
                      <span className={`tm-art z-${v.state}`}>{t(`status.${v.state}`, v.state)}</span>
                      <span className="tm-hg-titel">{v.title}</span>
                      <span className="tm-hg-unten">
                        {l?.step && <>{t(`team.schritt.${l.step}`, l.step)} · </>}
                        {t("team.quelle", { quelle: v.origin })}
                      </span>
                      {l ? (
                        <Dauer seit={l.since} className="tm-hg-dauer" />
                      ) : (
                        <time className="tm-hg-dauer" dateTime={v.updated_at}>
                          {new Date(v.updated_at).toLocaleDateString()}
                        </time>
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      )}

      <div className="tm-verlauf">
        {thread.isLoading && <p className="tm-leise">{t("common.loading")}</p>}
        {!thread.isLoading && leitfaden && (
          <div className="tm-leitfaden" role="note">
            <p className="tm-leitfaden-titel">{t("team.einstellenLeerTitel", { name: agent.data?.display_name ?? "" })}</p>
            <p className="tm-leise">{t("team.einstellenLeerText", { name: agent.data?.display_name ?? "" })}</p>
            {/* She may be a draft herself: then nothing she is told runs
                until somebody hires her — say so where the brief is typed,
                not after it was sent. */}
            {agent.data && isDraft(agent.data) && (
              <p className="tm-leitfaden-warnung">
                {t("brief.waitingForHire", { name: agent.data.display_name })}{" "}
                <Link to={`/agents/${agent.data.id}`}>{t("team.entwurfPruefen")}</Link>
              </p>
            )}
          </div>
        )}
        {!thread.isLoading && entries.length === 0 && !istPeople && (
          <div className="tm-leer">
            <p>{t("team.leerTitel", { name: agent.data?.display_name ?? "" })}</p>
            <p className="tm-leise">{t("team.leerText")}</p>
          </div>
        )}

        {entries.map((e, i) => {
          /* Die Nachricht und die Aufgabe, die aus ihr wurde, gehören
             zusammen — sonst zählt dieselbe Bitte als zwei Gruppen und der
             Trenner fällt zwischen sie. */
          const gruppe = (x: ChatEntry) => x.task_id ?? x.id;
          const neuerVorgang = i === 0 || gruppe(entries[i - 1]) !== gruppe(e);
          /* Erster Eintrag einer Folge desselben Sprechers: nur er trägt das
             Gesicht. */
          /* A run (#396): the same speaker again within five minutes, on
             the same day, in plain conversation. Only its first entry carries
             the face and the head; a question, a result or an error always
             starts one of its own. */
          const vorige = i > 0 ? entries[i - 1] : null;
          const schlicht = (x: ChatEntry) => x.kind === "message" || x.kind === "answer" || x.kind === "note";
          const folgt =
            !!vorige &&
            !istAuftrag(e) &&
            !istAuftrag(vorige) &&
            schlicht(e) &&
            schlicht(vorige) &&
            vorige.author === e.author &&
            tag(vorige.at) === tag(e.at) &&
            Date.parse(e.at) - Date.parse(vorige.at) < 5 * 60_000 &&
            !(neuerVorgang && e.task_id && e.kind !== "message");
          const erstesDerFolge = !folgt;
          /* Kam die Arbeit nicht aus dem Chat, sagt die Zeile, woher: „aus
             zammad" ist eine Auskunft, „Nachricht" wäre eine Behauptung. */
          const herkunftName = herkunft(e.author) || t("chat.kind.message", "");
          const neuerTag = i === 0 || tag(entries[i - 1].at) !== tag(e.at);
          const datum = neuerTag ? tagName(e.at, i18n.language) : null;
          const quelle = e.kind === "message" ? herkunft(e.author) : "";
          const davor = protokoll.filter(
            (d) => (i === 0 || Date.parse(d.decided_at!) > Date.parse(entries[i - 1].at)) && Date.parse(d.decided_at!) <= Date.parse(e.at),
          );
          return (
            <Fragment key={`${e.task_id}-${e.kind}-${i}`}>
              {davor.map((d) => (
                <Entschieden key={`${d.type}:${d.id}`} entry={d} />
              ))}
              {datum && <div className="tm-tag">{datum === "gestern" ? t("team.gestern") : datum}</div>}
              {/* Der Anker: die Stelle, an die ein Treffer der Suche und ein
                  Klick in der Vorgangsleiste springen. Er ist unsichtbar, bis
                  er getroffen wird — dann blitzt er einmal auf, damit man
                  sieht, wo man gelandet ist, statt es zu suchen. */}
              {neuerVorgang && (
                <span
                  className={`tm-anker${blitz === gruppe(e) ? " blitzt" : ""}`}
                  data-gruppe={gruppe(e)}
                  aria-hidden="true"
                />
              )}
              {/* Der Trenner führt einen Vorgang ein, dessen Kopf man sonst
                  nicht sähe. Eine Nachricht ist ihr eigener Kopf, ein Auftrag
                  auch — dort wiederholte die Linie nur, was direkt darunter
                  steht. Und eine beantwortete Nachricht hat gar keinen
                  Vorgang; eine Linie darüber wäre die Behauptung, es sei
                  Arbeit gewesen. */}
              {neuerVorgang && e.task_id && e.kind !== "message" && (
                <div className="tm-vorgang">
                  <span className="tm-vorgang-linie" aria-hidden="true" />
                  <Link to={`/agents/${agentId}?task=${e.task_id}`} className="tm-vorgang-titel">
                    {e.task_title}
                  </Link>
                  <span className="tm-vorgang-linie" aria-hidden="true" />
                </div>
              )}

              {istAuftrag(e) ? (
                <details className="tm-auftrag">
                  <summary>
                    <span className="tm-auftrag-quelle">{quelle || t("team.auftrag")}</span>
                    <span className="tm-auftrag-titel">{e.task_title}</span>
                    <time dateTime={e.at}>{uhr(e.at, i18n.language)}</time>
                  </summary>
                  <div className="tm-auftrag-text">
                    <Markdown text={e.text} />
                  </div>
                </details>
              ) : (
              <article
                className={`tm-blase ${vonMir(e) ? "ich" : "er"} k-${e.kind}${e.unterwegs ? " unterwegs" : ""}${folgt ? " folgt" : ""}${nurEmoji(e.text) ? " emoji" : ""}`}
                title={folgt ? uhr(e.at, i18n.language) : undefined}
              >
                {/* Auf der Seite des Agenten steht sein Gesicht, und zwar nur
                    beim ersten Eintrag einer Folge: Fünf Gesichter
                    untereinander sind eine Bilderreihe, keine Unterhaltung. */}
                {!vonMir(e) && (
                  <span className="tm-blase-wer" aria-hidden="true">
                    {erstesDerFolge && <Gesicht schluessel={agent.data?.slug ?? "?"} groesse={26} />}
                  </span>
                )}
                <div className="tm-blase-inhalt">
                {/* The head names who speaks (#396): the agent, and the kind
                    only where it means something; one's own entries need
                    only the time. */}
                {!folgt && (
                  <div className="tm-blase-kopf">
                    {vonMir(e) ? null : e.kind === "message" ? (
                      herkunftName
                    ) : (
                      <>
                        <span className="tm-blase-name">{agent.data?.display_name ?? ""}</span>
                        {(e.kind === "question" || e.kind === "result" || e.kind === "error") && (
                          <span className={`tm-blase-art a-${e.kind}`}>{t(`chat.kind.${e.kind}`)}</span>
                        )}
                      </>
                    )}
                    <time dateTime={e.at}>{uhr(e.at, i18n.language)}</time>
                  </div>
                )}
                <div className="tm-blase-text">
                  <Markdown text={e.text} />
                </div>

                {/* The drafts a hiring task produced: the colleague as a card,
                    and the way to the page where hiring is (#327). Read off
                    the recording, not off her report. */}
                {e.kind === "result" && e.drafts && e.drafts.length > 0 && (
                  <ul className="tm-entwuerfe">
                    {e.drafts.map((d) => (
                      <li key={d.id} className={`tm-entwurf${d.hired_at ? " eingestellt" : ""}`}>
                        <span className="tm-entwurf-zeichen" aria-hidden="true">
                          <Gesicht schluessel={d.slug} zustand={d.hired_at ? "working" : "sleeping"} groesse={26} />
                        </span>
                        <span className="tm-entwurf-text">
                          <span className="tm-entwurf-name">{d.display_name}</span>
                          <span className="tm-entwurf-rolle">{d.job_title || d.slug}</span>
                        </span>
                        {d.hired_at ? (
                          <Link to={`/team/${d.id}`} className="tm-entwurf-hin">
                            {t("team.eingestellt")}
                          </Link>
                        ) : (
                          <Link to={`/agents/${d.id}`} className="tm-entwurf-hin">
                            {t("team.entwurfPruefen")}
                          </Link>
                        )}
                      </li>
                    ))}
                  </ul>
                )}

                {/* Die offene Frage trägt ihre Antwort selbst. */}
                {e.kind === "question" && e.task_state === "blocked" && darfSchreiben && e.task_id && (
                  <div className="tm-antwort">
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
                              if (antwort.trim()) beantworten.mutate({ taskId: e.task_id!, text: antwort.trim() });
                            }
                            if (ev.key === "Escape") setAntwortAuf(null);
                          }}
                          aria-label={t("chat.placeholderAnswer")}
                          placeholder={t("chat.placeholderAnswer")}
                        />
                        <div className="tm-antwort-knoepfe">
                          <button
                            className="btn primary sm"
                            disabled={!antwort.trim() || beantworten.isPending}
                            onClick={() => beantworten.mutate({ taskId: e.task_id!, text: antwort.trim() })}
                          >
                            {t("chat.answer")}
                          </button>
                          <button className="btn sm" onClick={() => setAntwortAuf(null)}>
                            {t("team.abbrechen")}
                          </button>
                        </div>
                      </>
                    ) : (
                      <button className="btn sm" onClick={() => setAntwortAuf(e.task_id!)}>
                        {t("chat.answer")}
                      </button>
                    )}
                  </div>
                )}
                {/* Die Reaktionen hängen an der AUFGABE und stehen deshalb
                    nur am Kopf ihrer Gruppe — an jedem Eintrag gezeigt,
                    stünde dasselbe Zeichen drei-, viermal untereinander und
                    sähe aus wie vier Reaktionen. */}
                {neuerVorgang && e.task_id && marken[e.task_id] && (
                  <Marken
                    marken={marken[e.task_id]}
                    darf={darfSchreiben}
                    aufKlick={(emoji) => reagieren.mutate({ taskId: e.task_id!, emoji })}
                  />
                )}
                </div>
              </article>
              )}
            </Fragment>
          );
        })}
        {protokoll
          .filter((d) => entries.length === 0 || Date.parse(d.decided_at!) > Date.parse(entries[entries.length - 1].at))
          .map((d) => (
            <Entschieden key={`${d.type}:${d.id}`} entry={d} />
          ))}
        {/* What waits for this person's decision, with its buttons (#391). */}
        {offeneEntscheidungen.length > 0 && (
          <section className="tm-entscheiden" data-tour="entscheiden" aria-label={t("team.wartet")}>
            <h3 className="tm-entscheiden-kopf">{t("team.wartet")}</h3>
            <Suspense fallback={null}>
              {offeneEntscheidungen.map((d) => (
                <div key={`${d.type}:${d.id}`} className="tm-entscheiden-karte">
                  <EntryCard entry={d} me={me} />
                </div>
              ))}
            </Suspense>
          </section>
        )}
        {/* Der Agent arbeitet gerade an dem, was zuletzt im Verlauf steht —
            das sagt die Aufgabe selbst, es ist keine Vermutung. Drei Punkte
            sind dafür die Form, die jeder schon kennt. */}
        {!tippt && angenommen && sorge && (
          <p className="tm-status warn" role="status">
            {t("team.kannNichtStarten", { error: sorge.error ?? "" })}
          </p>
        )}
        {startet && !sorge && (
          <p className="tm-status" role="status">
            <span className="tm-status-punkt" aria-hidden="true" />
            <strong>{t("team.binDran")}</strong> {phaseText}
          </p>
        )}
        {tippt && (
          <article className="tm-blase er tm-tippt">
            <span className="tm-blase-wer" aria-hidden="true">
              <Gesicht schluessel={agent.data?.slug ?? "?"} groesse={26} />
            </span>
            <div className="tm-blase-inhalt">
              <div className="tm-tippt-blase" aria-label={t("team.arbeitetGerade")}>
                <span /> <span /> <span />
              </div>
            </div>
          </article>
        )}
        <div ref={ende} />
      </div>

      {/* Das Feld unten richtet sich immer an den Agenten — nie an eine
          wartende Frage; die beantwortet man an ihr selbst. */}
      <div className="tm-eingabe" data-tour="eingabe">
        {anhaenge.length > 0 && (
          <div className="tm-anhaenge">
            {anhaenge.map((f, i) => (
              <span key={`${f.name}-${i}`} className="tm-anhang">
                <NavIcon name="clip" />
                <span className="tm-anhang-name">{f.name}</span>
                <button
                  onClick={() => setAnhaenge((a) => a.filter((_, j) => j !== i))}
                  aria-label={t("team.anhangEntfernen", { name: f.name })}
                  title={t("team.anhangEntfernen", { name: f.name })}
                >
                  ×
                </button>
              </span>
            ))}
          </div>
        )}
        <div className="tm-eingabe-reihe">
          <input
            ref={dateiwahl}
            type="file"
            multiple
            hidden
            onChange={(ev) => {
              setAnhaenge((a) => [...a, ...Array.from(ev.target.files ?? [])]);
              ev.target.value = "";
            }}
          />
          <button
            className="tm-eingabe-knopf"
            onClick={() => dateiwahl.current?.click()}
            disabled={!darfSchreiben}
            title={t("team.anhaengen")}
            aria-label={t("team.anhaengen")}
          >
            <NavIcon name="clip" />
          </button>
          <textarea
            rows={1}
            value={text}
            onChange={(ev) => setText(ev.target.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" && !ev.shiftKey) {
                ev.preventDefault();
                abschicken();
              }
            }}
            disabled={!darfSchreiben}
            placeholder={!darfSchreiben ? t("chat.readOnly") : istPeople ? t("brief.placeholder") : t("chat.placeholder")}
            aria-label={t("chat.placeholder")}
          />
          <button
            className="tm-eingabe-senden"
            onClick={abschicken}
            disabled={!darfSchreiben || (!text.trim() && anhaenge.length === 0) || neu.isPending}
            title={t("chat.send")}
            aria-label={t("chat.send")}
          >
            <NavIcon name="arrowUp" />
          </button>
        </div>
      </div>
      {(neu.isError || beantworten.isError) && (
        <p className="tm-fehler">{String(neu.error ?? beantworten.error)}</p>
      )}
    </div>
  );
}

/* Die Reaktionen eines Vorgangs — und der eine Knopf, der eine hinzufügt.
 *
 * Sechs Zeichen, nicht die ganze Tastatur: Eine Auswahl, die alles anbietet,
 * verlangt eine Entscheidung; sechs verlangen einen Klick. Die sechs sind
 * die, die in einem Arbeitsverlauf etwas bedeuten — gesehen, verstanden,
 * gut, dringend, unklar, danke — und nicht die, die am häufigsten benutzt
 * werden.
 */
const ZEICHEN = ["\u{1F440}", "\u{1F44D}", "\u{1F389}", "\u{1F525}", "\u{1F914}", "\u{1F64F}"];

function Marken({
  marken,
  darf,
  aufKlick,
}: {
  marken: ChatMark[];
  darf: boolean;
  aufKlick: (emoji: string) => void;
}) {
  const { t } = useTranslation();
  const [offen, setOffen] = useState(false);
  if (marken.length === 0 && !darf) return null;
  return (
    <div className="tm-marken">
      {marken.map((m) => (
        <button
          key={m.emoji}
          className={`tm-marke ${m.mine ? "meine" : ""}`}
          onClick={() => darf && aufKlick(m.emoji)}
          disabled={!darf}
          title={m.who.map((w) => w.split(":").slice(1).join(":") || w).join(", ")}
        >
          <span aria-hidden="true">{m.emoji}</span>
          {m.count > 1 && <span className="tm-marke-zahl">{m.count}</span>}
        </button>
      ))}
      {darf && (
        <div className="tm-marke-wahl">
          <button
            className="tm-marke tm-marke-plus"
            onClick={() => setOffen((v) => !v)}
            aria-expanded={offen}
            aria-label={t("team.reagieren")}
            title={t("team.reagieren")}
          >
            +
          </button>
          {offen && (
            <>
              <div className="tm-marke-hinter" onClick={() => setOffen(false)} />
              <div className="tm-marke-liste" role="menu">
                {ZEICHEN.map((z) => (
                  <button
                    key={z}
                    role="menuitem"
                    onClick={() => {
                      setOffen(false);
                      aufKlick(z);
                    }}
                  >
                    {z}
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

/* A decided entry as one line of the conversation (#391): what it was,
   how it ended, when. The conversation is the record. */
function Entschieden({ entry }: { entry: InboxEntry }) {
  const { t, i18n } = useTranslation();
  return (
    <div className={`tm-entschieden st-${entry.status}`}>
      <span className="tm-entschieden-art">{t(`inbox.type.${entry.type}`)}</span>
      <span className="tm-entschieden-titel">{entry.title}</span>
      <span className="tm-entschieden-wie">{t(`inbox.status.${entry.status}`, entry.status)}</span>
      <time dateTime={entry.decided_at}>{uhr(entry.decided_at!, i18n.language)}</time>
    </div>
  );
}
