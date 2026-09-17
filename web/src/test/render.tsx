import type { ReactElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { render } from "@testing-library/react";
import { vi } from "vitest";
import i18n from "../i18n";
import type { Principal } from "../api";

// Shared scaffolding for component tests: the same providers as the app
// (query cache, router, i18n), only on a short leash — no retries, no cache
// across test boundaries. Without this TanStack Query retries failed
// requests, and a test waits seconds for an error it has long been
// expecting.
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

// The UI is bilingual; tests assert against the German texts. Without this
// pin the result would depend on what localStorage held last — a test that
// depends on the developer's language is not one.
export function useGerman() {
  i18n.changeLanguage("de");
}

export const testPrincipal = (role = "org_admin", platformRole = "user"): Principal => ({
  ID: "11111111-1111-1111-1111-111111111111",
  OrgID: "22222222-2222-2222-2222-222222222222",
  Email: "test@covey.local",
  DisplayName: "Test Admin",
  Role: role,
  AccountID: "33333333-3333-3333-3333-333333333333",
  // Default is deliberately "user": the instance level is the exception, not
  // the norm — a test that needs it says so (FR-003).
  PlatformRole: platformRole,
});

// mockFetch answers requests by path pattern. The test says what the server
// returns; anything unanswered is loud (404 + recorded), so a forgotten
// endpoint stands out instead of passing quietly as an empty list.
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
      // Compare the path without its query. A pattern WITH "?" checks the query
      // too — as a prefix, because the caller appends more parameters
      // (sorting, page size). Patterns with a query therefore belong BEFORE the
      // one without: the first match wins.
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
