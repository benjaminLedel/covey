import type { ReactElement } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import App from "./App";
import { api } from "./api";
import { merkeSprache } from "./i18n";
import { useGerman, testPrincipal } from "./test/render";

/* Was passiert, wenn die Sitzung endet.

   Zwei Wege führen dorthin, und beide endeten vorher im Nichts: Wer die Seite
   neu lud, landete unter seiner App-Adresse auf der 404 der öffentlichen
   Website (die /agents/… nicht kennt); wer weiterklickte, blieb in einer
   Hülle sitzen, die sich mit Fehlermeldungen füllte. Beide sollen auf der
   Anmeldung enden — mit dem Weg zurück im Gepäck. */

// App rendert eigene Routen; das Gerüst aus test/render.tsx hängt sie unter
// eine Splat-Route und verschöbe damit die Pfade. Deshalb hier direkt.
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

/* Ein Server, der die Sitzung erst kennt und dann nicht mehr. Alles außer
   /auth/me ist Beiwerk — die Shell fragt beim Aufbau einiges ab, was für
   diese Prüfung nichts trägt. */
function serverMitSitzung(angemeldet: () => boolean) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();
      const abgelehnt = new Response(JSON.stringify({ error: "session expired" }), { status: 401 });
      if (!angemeldet()) return abgelehnt;
      if (url.includes("/auth/me")) {
        return new Response(JSON.stringify(testPrincipal()), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      // Zwei Abfragen der Hülle rechnen mit einem Objekt statt einer Liste:
      // die Einrichtungs-Checkliste und die Bauzeile im Fuß.
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
  // Die Shell öffnet einen Ereignisstrom; jsdom kennt EventSource nicht.
  vi.stubGlobal(
    "EventSource",
    class {
      close() {}
      addEventListener() {}
    },
  );
  /* Die öffentliche Website bringt ihren eigenen Hintergrund mit (Canvas,
     Scroll-Reveals). In jsdom gibt es beides nicht — ohne Attrappen stirbt
     die Anmeldeseite an ihrer Dekoration, bevor der Test sie sieht. */
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

    // Nicht die 404 der öffentlichen Website, sondern die Anmeldemaske …
    expect(await screen.findByLabelText("Passwort")).toBeInTheDocument();
    // … und ein Satz dazu, warum man wieder hier steht (der ?weiter=-Parameter
    // aus der Weiterleitung trägt ihn).
    expect(screen.getByText(/Sitzung ist abgelaufen/)).toBeInTheDocument();
  });

  it("lässt eine öffentliche Adresse in Ruhe", async () => {
    serverMitSitzung(() => false);
    renderApp(<App />, "/");

    // Die Startseite bleibt die Startseite — keine Weiterleitung, und damit
    // auch kein Hinweis auf eine Sitzung, aus der niemand gefallen ist.
    expect(await screen.findByRole("link", { name: "Docs" })).toBeInTheDocument();
    expect(screen.queryByText(/Sitzung ist abgelaufen/)).not.toBeInTheDocument();
  });
});

describe("App bei ablaufender Sitzung", () => {
  it("schaltet auf die Anmeldung um, sobald der Server mit 401 antwortet", async () => {
    let angemeldet = true;
    serverMitSitzung(() => angemeldet);
    renderApp(<App />, "/inbox");

    // Angemeldet: die Hülle steht (die Navigation ist ihr sichtbarster Teil).
    expect(await screen.findByText("Agenten")).toBeInTheDocument();

    // Die Sitzung endet serverseitig — die nächste Anfrage der Oberfläche
    // bringt es ans Licht.
    angemeldet = false;
    await api("/agents").catch(() => {});

    await waitFor(() => expect(screen.getByLabelText("Passwort")).toBeInTheDocument());
    expect(screen.queryByText("Agenten")).not.toBeInTheDocument();
  });

  it("landet auch von der Übersicht aus auf der Anmeldung", async () => {
    /* „/" ist zweierlei: die Übersicht der Oberfläche und die öffentliche
       Startseite. Wem die Sitzung dort wegläuft, der soll die Anmeldung
       sehen — nicht die Werbeseite. */
    let angemeldet = true;
    serverMitSitzung(() => angemeldet);
    renderApp(<App />, "/");

    // Auf der Übersicht steht „Agenten" zweimal: in der Navigation und als
    // Überschrift.
    expect((await screen.findAllByText("Agenten")).length).toBeGreaterThan(0);

    angemeldet = false;
    await api("/agents").catch(() => {});

    await waitFor(() => expect(screen.getByLabelText("Passwort")).toBeInTheDocument());
    expect(screen.getByText(/Sitzung ist abgelaufen/)).toBeInTheDocument();
  });
});
