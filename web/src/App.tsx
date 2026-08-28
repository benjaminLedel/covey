import { Suspense, lazy, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, useLocation } from "react-router";
import { api, setUnauthorizedHandler, type Principal } from "./api";
import { gespeicherteSprache, istVorgerendert } from "./i18n";
import { APP_ROUTE_PREFIXES, LANGS, pathOf } from "./public/seo";
import PublicSite from "./public/PublicSite";

/* Die angemeldete Oberfläche kommt aus einem eigenen Bündel — sie ist der
   größere Teil der Anwendung und geht keinen an, der die öffentliche Website
   liest (#122). Ohne Platzhalter: Bis hierher stand ohnehin nichts, solange
   /auth/me lief. */
const AppShell = lazy(() => import("./AppShell"));
const NoOrganization = lazy(() => import("./pages/NoOrganization"));

// useLiveEvents hält die UI über SSE aktuell: jedes Server-Event invalidiert
// die betroffenen Queries — TanStack Query lädt gezielt nach.
function useLiveEvents(enabled: boolean) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!enabled) return;
    const es = new EventSource("/api/v1/events");
    const invalidate = () => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      qc.invalidateQueries({ queryKey: ["agent"] });
      qc.invalidateQueries({ queryKey: ["backlog"] });
      qc.invalidateQueries({ queryKey: ["recording"] });
      qc.invalidateQueries({ queryKey: ["inbox"] });
      qc.invalidateQueries({ queryKey: ["cost"] });
      qc.invalidateQueries({ queryKey: ["memories"] });
    };
    for (const t of ["agent_status", "task", "recording", "approval", "guardrail"]) {
      es.addEventListener(t, invalidate);
    }
    /* Reißt der Ereignisstrom ab, kann das zweierlei heißen: der Server wurde
       neu gestartet (dann verbindet der Browser von selbst neu) — oder die
       Sitzung ist abgelaufen und der Endpunkt antwortet mit 401. Welches von
       beidem, sagt ein EventSource nicht. Deshalb fragt die Oberfläche nach:
       /auth/me beantwortet es, und eine abgelaufene Sitzung landet dort in
       der 401, ohne dass jemand erst klicken muss. */
    es.onerror = () => void qc.refetchQueries({ queryKey: ["me"] });
    return () => es.close();
  }, [enabled, qc]);
}

/* Wurde diese Seite beim Build vorgerendert? Dann steht ihr Inhalt schon im
   HTML, und der erste Rendervorgang im Browser muss ihn treffen — sonst
   verwirft React das vorgerenderte Markup. Deshalb zeigt App auf solchen
   Seiten die öffentliche Website bereits, während /auth/me noch läuft. Auf
   allen anderen Pfaden bleibt es beim bisherigen Verhalten. */
const prerendered = istVorgerendert();

/* Die leere UUID ist die Antwort des Servers auf "noch keine Organisation" —
   Principal.OrgID ist ein Wert, kein Zeiger, und hat deshalb keinen NULL. */
const LEERE_UUID = "00000000-0000-0000-0000-000000000000";

/* Nicht (mehr) angemeldet. Die öffentliche Website steht unter ihren eigenen
   Adressen — eine Adresse der Oberfläche (/agents/…, /inbox, …) kennt sie
   nicht und beantwortete sie mit „Seite nicht gefunden". Genau das passierte
   bisher, wenn eine Sitzung ablief: wer neu lud, landete auf einer 404 statt
   auf der Anmeldung.

   Deshalb hier die Weiterleitung — mit dem Ziel im Gepäck (?weiter=), damit
   die Anmeldung dorthin zurückführt, wo jemand unterbrochen wurde. */
function Abgemeldet({ onLogin, ausDerOberflaeche = false }: { onLogin: () => void; ausDerOberflaeche?: boolean }) {
  const { pathname, search } = useLocation();
  /* Wer gerade noch angemeldet war, gehört immer auf die Anmeldung — auch von
     „/" aus. Die Adresse der Übersicht ist zugleich die der öffentlichen
     Startseite; ohne diese Unterscheidung landete jemand, dem die Sitzung
     unter den Händen wegläuft, auf der Werbeseite statt am Anmeldeformular.
     Beim ersten Aufruf entscheidet dagegen der Pfad: dort ist „/" wirklich
     die Startseite. */
  const schonAufDerAnmeldung = LANGS.some((l) => pathOf("anmelden", l) === pathname);
  const zurAnmeldung =
    !schonAufDerAnmeldung &&
    (ausDerOberflaeche ||
      APP_ROUTE_PREFIXES.some((p) => pathname === p || pathname.startsWith(p + "/")));
  if (zurAnmeldung) {
    /* Die Sprache der Anmeldeseite folgt der Oberfläche, aus der jemand
       herausgefallen ist (Shell-Voreinstellung: englisch) — nicht dem Pfad,
       der bei App-Adressen gar keine Sprache trägt. */
    const lang = gespeicherteSprache() === "de" ? "de" : "en";
    const ziel = `${pathOf("anmelden", lang)}?weiter=${encodeURIComponent(pathname + search)}`;
    return <Navigate to={ziel} replace />;
  }
  return <PublicSite onLogin={onLogin} />;
}

/* Ein sicheres ?weiter=: nur ein Pfad dieser Installation. „//host" wäre für
   den Browser eine fremde Adresse — ein offener Weiterleiter in der
   Anmeldung ist eine der ältesten Phishing-Hilfen. */
function weiterZiel(search: string): string | null {
  const ziel = new URLSearchParams(search).get("weiter");
  if (!ziel || !ziel.startsWith("/") || ziel.startsWith("//")) return null;
  return ziel;
}

export default function App() {
  const qc = useQueryClient();
  const location = useLocation();
  /* Abgelaufen heißt: der Server hat eine Anfrage der laufenden Sitzung mit
     401 abgewiesen. Ohne diesen Zustand blieb die Hülle stehen und füllte
     sich mit Fehlern — die ["me"]-Abfrage lief ja nicht neu und wusste von
     nichts. */
  const [abgelaufen, setAbgelaufen] = useState(false);
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<Principal>("/auth/me"),
    retry: false,
    /* Der häufigste Fall ist der Tab, der über Nacht offen bleibt: beim
       Zurückkommen soll die Oberfläche die Sitzung prüfen, statt beim ersten
       Klick in eine 401 zu laufen. */
    refetchOnWindowFocus: true,
  });

  useEffect(() => {
    setUnauthorizedHandler(() => setAbgelaufen(true));
    return () => setUnauthorizedHandler(null);
  }, []);

  const anmelden = () => {
    /* Die Daten der beendeten Sitzung verwerfen — sonst zeigte die Oberfläche
       nach einer Anmeldung als jemand anderes für einen Moment noch die
       Listen der vorigen. */
    qc.clear();
    setAbgelaufen(false);
    void me.refetch();
  };

  useLiveEvents(me.isSuccess && !abgelaufen);

  if (abgelaufen) return <Abgemeldet onLogin={anmelden} ausDerOberflaeche />;
  if (me.isLoading) {
    return prerendered ? <PublicSite onLogin={anmelden} /> : null;
  }
  if (me.isError) return <Abgemeldet onLogin={anmelden} />;
  /* Angemeldet, aber ohne Sitz: seit die Anmeldung am Konto hängt (FR-002),
     kann ein Konto existieren, bevor eine Organisation es kennt. Die Shell
     liefe dort in lauter 409er, deshalb kommt sie gar nicht erst dran. */
  if (me.data!.OrgID === LEERE_UUID) {
    return (
      <Suspense fallback={null}>
        <NoOrganization me={me.data!} onLogout={() => me.refetch()} />
      </Suspense>
    );
  }
  /* Wieder angemeldet: zurück an die Stelle, an der die Sitzung abriss. */
  const weiter = weiterZiel(location.search);
  if (weiter) return <Navigate to={weiter} replace />;
  return (
    <Suspense fallback={null}>
      <AppShell me={me.data!} onLogout={() => me.refetch()} />
    </Suspense>
  );
}

