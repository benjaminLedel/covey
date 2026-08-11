import type { ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { render } from "@testing-library/react";
import { vi } from "vitest";
import i18n from "../i18n";
import type { Principal } from "../api";

// Gemeinsames Gerüst für Komponenten-Tests: dieselben Provider wie die
// Anwendung (Query-Cache, Router, i18n), nur mit kurzen Zügeln — kein Retry,
// kein Cache über Testgrenzen hinweg. Ohne das wiederholt TanStack Query
// fehlgeschlagene Anfragen und ein Test wartet Sekunden auf einen Fehler, den
// er längst erwartet.
export function renderWithProviders(
  ui: ReactElement,
  opts: { route?: string; path?: string } = {},
) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  const route = opts.route ?? "/";
  const path = opts.path ?? "*";
  return {
    qc,
    ...render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[route]}>
          <Routes>
            <Route path={path} element={ui} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  };
}

// Die Oberfläche ist zweisprachig; Tests prüfen gegen die deutschen Texte.
// Ohne diese Festlegung hinge das Ergebnis daran, was zuletzt im localStorage
// stand — ein Test, der von der Sprache des Entwicklers abhängt, ist keiner.
export function useGerman() {
  i18n.changeLanguage("de");
}

export const testPrincipal = (role = "platform_admin"): Principal => ({
  ID: "11111111-1111-1111-1111-111111111111",
  OrgID: "22222222-2222-2222-2222-222222222222",
  Email: "test@covey.local",
  DisplayName: "Test Admin",
  Role: role,
});

// mockFetch beantwortet Anfragen nach Pfadmuster. Der Test sagt, was der
// Server liefert; alles Unbeantwortete wird laut (404 + gemerkt), damit ein
// vergessener Endpunkt auffällt, statt still als leere Liste durchzugehen.
export function mockFetch(routes: Record<string, unknown>) {
  const unmatched: string[] = [];
  const calls: string[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push(`${method} ${url}`);

    for (const [pattern, body] of Object.entries(routes)) {
      const [pMethod, pPath] = pattern.includes(" ") ? pattern.split(" ") : ["GET", pattern];
      if (method !== pMethod) continue;
      // Pfad ohne Query vergleichen. Ein Muster MIT "?" prüft die Query mit —
      // als Präfix, weil der Aufrufer weitere Parameter anhängt (Sortierung,
      // Seitengröße). Muster mit Query gehören deshalb VOR das ohne: die erste
      // Übereinstimmung gewinnt.
      const target = pPath.includes("?") ? url : url.split("?")[0];
      if (target === pPath || target.endsWith(pPath) || (pPath.includes("?") && url.includes(pPath))) {
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
    }
    unmatched.push(`${method} ${url}`);
    return new Response(JSON.stringify({ error: "nicht gemockt" }), { status: 404 });
  });
  vi.stubGlobal("fetch", fn);
  return { calls, unmatched };
}
