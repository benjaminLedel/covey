import { Suspense, lazy, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, useLocation } from "react-router";
import { api, setUnauthorizedHandler, type Principal } from "./api";
import { initialLang } from "./i18n";
import { LANGS, MAIL_LINK_PATHS, PUBLIC_ROUTES, pathOf } from "./public/routes";
import SignedOut from "./public/SignedOut";

/* The signed-in UI comes from its own bundle — it is the larger part of the
   application and none of the business of someone who reads the public
   website (#122). No placeholder: nothing stood here anyway while
   /auth/me ran. */
const AppShell = lazy(() => import("./AppShell"));
const Team = lazy(() => import("./team/Team"));
const NoOrganization = lazy(() => import("./pages/NoOrganization"));

/* Zwei Schalen, und welche gilt, entscheidet die Adresse.
 *
 * Das TEAM ist die Oberfläche dessen, der MIT der Belegschaft arbeitet:
 * Verläufe, offene Fragen, ein Eingabefeld. Die Konsole ist die Oberfläche
 * dessen, der sie BAUT: Konfiguration, Zugänge, Guard-Rails, Kosten. Beide
 * teilen sich weder Navigation noch Grund — ein Reiter zwischen dreizehn
 * anderen hätte behauptet, das sei dieselbe Arbeit.
 *
 * Die Wurzel gehört dem Team, weil dort jeder landet. Die Agentenliste,
 * die früher hier stand, steht unter /agents. */
const imTeam = (pfad: string) => pfad === "/" || pfad.startsWith("/team/");

// useLiveEvents keeps the UI current over SSE: every server event invalidates
// the queries it touches, and TanStack Query refetches just those.
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
    /* A broken event stream can have two causes: the server was restarted
       (then the browser reconnects on its own) — or the session expired and
       the endpoint answers with 401. An EventSource does not say which of
       the two. So the UI asks: /auth/me answers it, and an expired session
       lands in its 401 without anyone having
       to click first. */
    es.onerror = () => void qc.refetchQueries({ queryKey: ["me"] });
    return () => es.close();
  }, [enabled, qc]);
}

/* The zero UUID is the server's answer to "no organisation yet":
   Principal.OrgID is a value, not a pointer, so it has no NULL. */
const LEERE_UUID = "00000000-0000-0000-0000-000000000000";

/* Not signed in (anymore): every address except the open ones leads to the
   login, carrying the destination along (?weiter=) so it returns to where
   someone was interrupted.

   Until #130 this was a case distinction, because "/" was two things at once:
   the dashboard of the UI and the landing page of the website. Whoever lost
   their session landed on the marketing page instead of the form, and the
   rule had to tell apart someone who had just fallen out from someone newly
   arriving. Since the website sits on its own host, "/" here is one thing
   only. */
function Abgemeldet({
  onLogin,
  ausDerOberflaeche = false,
}: {
  onLogin: () => void;
  ausDerOberflaeche?: boolean;
}) {
  const { pathname, search } = useLocation();
  /* Open are login and signup in every language, and the two addresses a mail
     points at (/verify, /reset).

     The second half was missing, so the case they were built for was the one
     case where they did not work: someone who forgot their password is not
     signed in. They landed on /anmelden?weiter=%2Freset, which is
     exactly where they cannot get further, with the notice that their
     session had expired. The confirmation link out of the
     signup mail hit the same.

     SignedOut knows both routes; they were
     never reached, because this redirected first (#210). */
  const offen =
    PUBLIC_ROUTES.some((r) => LANGS.some((l) => r.path[l] === pathname)) ||
    MAIL_LINK_PATHS.includes(pathname);
  if (!offen) {
    /* The language follows the stored choice, else the browser, not the path,
       which carries none for app addresses. */
    const lang = initialLang("/");
    const anmeldung = pathOf("anmelden", lang);
    /* ?weiter= carries two things: where to go back to after the login, and
       the sentence `Ihre Sitzung ist abgelaufen` (LoginCard reads the
       parameter). From the bare root the first does not exist — "/" is where
       the UI starts anyway. Whoever just had their session expire underneath
       still gets the sentence: that is what ausDerOberflaeche is for.
       Otherwise it would stand in front of every first visitor who calls /
       and has never been signed in. */
    const mitZiel = ausDerOberflaeche || pathname !== "/";
    const ziel = mitZiel
      ? `${anmeldung}?weiter=${encodeURIComponent(pathname + search)}`
      : anmeldung;
    return <Navigate to={ziel} replace />;
  }
  return <SignedOut onLogin={onLogin} />;
}

/* A safe ?weiter=: only a path of this installation. "//host" is a foreign
   address to the browser, and an open redirector at the login is one of the
   oldest phishing aids. */
function weiterZiel(search: string): string | null {
  const ziel = new URLSearchParams(search).get("weiter");
  if (!ziel || !ziel.startsWith("/") || ziel.startsWith("//")) return null;
  return ziel;
}

export default function App() {
  const qc = useQueryClient();
  const location = useLocation();
  /* Expired means: the server rejected a request of the running session with
     401. Without this state the shell stayed standing and filled up with
     errors — the ["me"] query did not rerun and knew
     nothing of it. */
  const [abgelaufen, setAbgelaufen] = useState(false);
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<Principal>("/auth/me"),
    retry: false,
    /* The common case is the tab that stays open overnight: on return the UI
       should check the session instead of walking into a 401 on
       the first click. */
    refetchOnWindowFocus: true,
  });

  useEffect(() => {
    setUnauthorizedHandler(() => setAbgelaufen(true));
    return () => setUnauthorizedHandler(null);
  }, []);

  const anmelden = () => {
    /* Drop the data of the ended session — else the UI would show the lists
       of the previous person for a moment
       after a login as someone else. */
    qc.clear();
    setAbgelaufen(false);
    void me.refetch();
  };

  useLiveEvents(me.isSuccess && !abgelaufen);

  if (abgelaufen) return <Abgemeldet onLogin={anmelden} ausDerOberflaeche />;
  /* Nothing renders while /auth/me runs: a form that the UI replaces a moment
     later is more restless than a short emptiness. Until #130 the pre-rendered
     website stood here, and that was already in the HTML, so there was nothing
     to show from here. */
  if (me.isLoading) return null;
  if (me.isError) return <Abgemeldet onLogin={anmelden} />;
  /* Signed in but without a seat: since the login hangs on the account
     (FR-002), an account can exist before an organisation knows it. The shell
     would run into nothing but 409s there, so it does not get that far. */
  if (me.data!.OrgID === LEERE_UUID) {
    return (
      <Suspense fallback={null}>
        <NoOrganization me={me.data!} onLogout={() => me.refetch()} />
      </Suspense>
    );
  }
  /* Signed in again: back to the place where the session broke off. */
  const weiter = weiterZiel(location.search);
  if (weiter) return <Navigate to={weiter} replace />;
  return (
    <Suspense fallback={null}>
      {imTeam(location.pathname) ? (
        <Team me={me.data!} onLogout={() => me.refetch()} />
      ) : (
        <AppShell me={me.data!} onLogout={() => me.refetch()} />
      )}
    </Suspense>
  );
}

