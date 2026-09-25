import type { ReactElement } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import App from "./App";
import { api } from "./api";
import { merkeSprache } from "./i18n";
import { useGerman, testPrincipal } from "./test/render";

/* What happens when the session ends.

   Two paths lead there and both ended in nothing before: whoever reloaded the
   page landed on the 404 of the public website under their app address (which
   does not know /agents/…); whoever clicked on stayed in a shell that filled
   with error messages. Both should end at the
   login, carrying the way back. */

// App renders its own routes; the scaffold from test/render.tsx hangs them
// under a splat route and would shift the paths. So directly here.
function renderApp(ui: ReactElement, route: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
}

/* A server that knows the session and then does not. Everything except
   /auth/me is beside the point, the shell asks for a number of things while it
   builds that carry nothing here. */
function serverMitSitzung(angemeldet: () => boolean, teamSurface = false) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      const abgelehnt = new Response(JSON.stringify({ error: "session expired" }), { status: 401 });
      if (!angemeldet()) return abgelehnt;
      if (url.includes("/auth/me")) {
        return new Response(JSON.stringify({ ...testPrincipal(), TeamSurface: teamSurface }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      // Two queries of the shell expect an object instead of a list: the setup
      // checklist and the build line in the footer.
      const leer = url.includes("/onboarding")
        ? { steps: [], done: true }
        : url.includes("/version")
          ? { version: "test", commit: "0000000", go: "", built_at: "", dirty: false }
          : [];
      return new Response(JSON.stringify(leer), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
}

beforeEach(() => {
  useGerman();
  merkeSprache("de");
  // The shell opens an event stream; jsdom does not know EventSource.
  vi.stubGlobal(
    "EventSource",
    class {
      close() {}
      addEventListener() {}
    },
  );
  /* The public website brings its own background (canvas, scroll reveals).
     jsdom has neither; without stand-ins the login page dies on its
     decoration before the test sees it. */
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  }));
});

describe("App ohne gültige Sitzung", () => {
  it("leitet von einer Adresse der Oberfläche auf die Anmeldung um", async () => {
    serverMitSitzung(() => false);
    renderApp(<App />, "/agents/1234");

    // Not the 404 of the public website, but the login form …
    expect(await screen.findByLabelText("Passwort")).toBeInTheDocument();
    // … and a sentence why one stands here again (the ?weiter= parameter from
    // the redirect carries it).
    expect(screen.getByText(/Sitzung ist abgelaufen/)).toBeInTheDocument();
  });

  it("lässt die Adressen aus einer Mail durch, statt sie zur Anmeldung zu schicken", async () => {
    /* The case /reset and /verify were built for was the one case where they
       did not work: someone who forgot their password is not signed in. The
       redirect reached before them and sent them to /anmelden?weiter=%2Freset,
       which is where they cannot get further, with the notice that their
       session had expired. The same path hit the confirmation link from the
       signup mail (#210). */
    serverMitSitzung(() => false);
    renderApp(<App />, "/reset");

    expect(await screen.findByText("Passwort zurücksetzen")).toBeInTheDocument();
    expect(screen.getByLabelText("E-Mail-Adresse")).toBeInTheDocument();
    expect(screen.queryByText(/Sitzung ist abgelaufen/)).not.toBeInTheDocument();
  });

  it("zeigt auf der Wurzel die Anmeldung, ohne von einer Sitzung zu reden", async () => {
    serverMitSitzung(() => false);
    renderApp(<App />, "/");

    /* Since the website moved out (#130), "/" here is the login and nothing
       else. Whoever was never signed in should therefore not read that their
       session expired. */
    expect(await screen.findByLabelText("Passwort")).toBeInTheDocument();
    expect(screen.queryByText(/Sitzung ist abgelaufen/)).not.toBeInTheDocument();
  });
});

describe("App bei ablaufender Sitzung", () => {
  it("schaltet auf die Anmeldung um, sobald der Server mit 401 antwortet", async () => {
    let angemeldet = true;
    serverMitSitzung(() => angemeldet);
    renderApp(<App />, "/inbox");

    // Signed in: the shell stands (the navigation is its most visible part).
    expect(await screen.findByText("Agenten")).toBeInTheDocument();

    // The session ends on the server side; the next request of the UI brings
    // it to light.
    angemeldet = false;
    await api("/agents").catch(() => {});

    await waitFor(() => expect(screen.getByLabelText("Passwort")).toBeInTheDocument());
    expect(screen.queryByText("Agenten")).not.toBeInTheDocument();
  });

  it("landet auch von der Übersicht aus auf der Anmeldung", async () => {
    /* Whoever loses the session on the dashboard should see the login and read
       why they stand in front of it again, also from the agent list, where a
       first visitor does not get the sentence just now. The list moved from
       "/" to "/agents" when the workspace took the root. */
    let angemeldet = true;
    serverMitSitzung(() => angemeldet);
    renderApp(<App />, "/agents");

    // On the dashboard `Agenten` stands twice: in the navigation and as a
    // heading.
    expect((await screen.findAllByText("Agenten")).length).toBeGreaterThan(0);

    angemeldet = false;
    await api("/agents").catch(() => {});

    await waitFor(() => expect(screen.getByLabelText("Passwort")).toBeInTheDocument());
    expect(screen.getByText(/Sitzung ist abgelaufen/)).toBeInTheDocument();
  });
});

/* The team surface is an opt-in per organisation (#328). Which shell the root
   gets is read from /auth/me, and off must mean the console at the root and no
   switch to a surface that is not there. */
describe("App und die Team-Oberfläche", () => {
  it("gibt die Wurzel der Verwaltung, solange die Organisation das Team nicht eingeschaltet hat", async () => {
    serverMitSitzung(() => true, false);
    renderApp(<App />, "/");

    expect((await screen.findAllByText("Agenten")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("navigation", { name: "Zwischen Team und Verwaltung wechseln" })).not.toBeInTheDocument();
  });

  it("gibt die Wurzel dem Team, wenn die Organisation es eingeschaltet hat", async () => {
    // The office measures its floor; jsdom does not.
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
    serverMitSitzung(() => true, true);
    renderApp(<App />, "/");

    expect(await screen.findByText("Büro")).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "Zwischen Team und Verwaltung wechseln" })).toBeInTheDocument();
  });
});

/* The pairing link lands here when no app took it (#333): the same page
   signed in or not — the code is the badge, the login has nothing to add. */
describe("App und der Kopplungslink", () => {
  for (const angemeldet of [false, true]) {
    it(`reicht /pair an die App weiter (${angemeldet ? "angemeldet" : "abgemeldet"})`, async () => {
      serverMitSitzung(() => angemeldet);
      renderApp(<App />, "/pair?code=coveypair_abc");

      const link = await screen.findByRole("link", { name: "In der App öffnen" });
      expect(link.getAttribute("href")).toBe(
        `covey://pair?instance=${encodeURIComponent(window.location.origin)}&code=coveypair_abc`,
      );
      expect(screen.queryByLabelText("Passwort")).not.toBeInTheDocument();
    });
  }
});
