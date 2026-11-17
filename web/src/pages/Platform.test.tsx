import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Platform from "./Platform";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

// The platform panel manages the INSTALLATION. What these tests hold down is
// above all the boundary that has to stay visible here: the level on the
// right belongs to the instance, the role to the seat of an organisation —
// the same distinction that FR-003 finding F forced.

const KONTEN = [
  {
    id: "33333333-3333-3333-3333-333333333333",
    email: "betreiber@example.de",
    display_name: "Betreiberin",
    email_verified_at: "2026-01-01T00:00:00Z",
    platform_role: "system_admin",
    created_at: "2026-01-01T00:00:00Z",
    last_login_at: "2026-08-01T09:00:00Z",
    seats: [{ org_id: "22222222-2222-2222-2222-222222222222", org_name: "Northgate", role: "org_admin" }],
  },
  {
    id: "44444444-4444-4444-4444-444444444444",
    email: "neu@example.de",
    display_name: "",
    platform_role: "user",
    created_at: "2026-08-01T00:00:00Z",
    seats: [],
  },
];

const CODES = [
  {
    hash: "0123456789abcdef0123456789abcdef",
    label: "Konferenz X",
    max_uses: 3,
    used_count: 1,
    created_at: "2026-08-01T00:00:00Z",
  },
];

const EINSTELLUNGEN = [
  { key: "signup.mode", value: "off", default: "off" },
  { key: "site.name", value: "Northgate covey", default: "covey" },
];

const routen = {
  "/api/v1/platform/accounts": KONTEN,
  "/api/v1/platform/settings": EINSTELLUNGEN,
  "/api/v1/platform/waitlist-codes": CODES,
  "/api/v1/platform/orgs": [],
};

const systemadmin = testPrincipal("org_admin", "system_admin");

describe("Plattform-Panel", () => {
  beforeEach(() => useGerman());

  it("zeigt bei einem Konto beide Ebenen getrennt: Sitz-Rolle und Instanz-Ebene", async () => {
    mockFetch(routen);
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/accounts", path: "/platform/*" });

    expect(await screen.findByText("Betreiberin")).toBeInTheDocument();
    // The list only shows (#743): the seat with its role, the instance level
    // as a tag — and nothing that changes either on one click.
    expect(screen.getByText("Northgate · Org-Admin")).toBeInTheDocument();
    expect(screen.getByText("System-Admin")).toBeInTheDocument();
    expect(screen.queryAllByRole("combobox")).toHaveLength(0);

    // An account without a seat is not an error, but the state after someone
    // registered themselves — and it has to be readable as such.
    expect(screen.getByText("in keiner Organisation")).toBeInTheDocument();
    expect(screen.getByText("nie angemeldet")).toBeInTheDocument();

    // Opened, the two levels stand under headings of their own.
    await userEvent.click(screen.getByText("Betreiberin"));
    const detail = document.querySelector(".account-detail") as HTMLElement;
    expect(detail).toHaveTextContent("Organisationen");
    expect(detail).toHaveTextContent("Diese Installation");
  });

  it("ändert eine Rolle erst nach Ändern und Speichern", async () => {
    const { calls } = mockFetch({
      ...routen,
      "PATCH /api/v1/platform/orgs/22222222-2222-2222-2222-222222222222/members/33333333-3333-3333-3333-333333333333": {},
    });
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/accounts", path: "/platform/*" });

    await userEvent.click(await screen.findByText("Betreiberin"));
    await userEvent.click(screen.getAllByRole("button", { name: "Ändern" })[0]);
    await userEvent.click(screen.getByRole("radio", { name: /Auditor/ }));
    expect(calls.some((c) => c.startsWith("PATCH"))).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Speichern" }));
    expect(calls).toContain("PATCH /api/v1/platform/orgs/22222222-2222-2222-2222-222222222222/members/33333333-3333-3333-3333-333333333333");
  });

  it("gibt einem Konto einen Sitz in einer weiteren Organisation (#262)", async () => {
    const orgs = [
      { id: "22222222-2222-2222-2222-222222222222", name: "Northgate" },
      { id: "55555555-5555-5555-5555-555555555555", name: "Southfield" },
    ];
    const { calls } = mockFetch({
      ...routen,
      "/api/v1/platform/orgs": orgs,
      "POST /api/v1/platform/orgs/55555555-5555-5555-5555-555555555555/members": {},
    });
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/accounts", path: "/platform/*" });

    // Offered only where the account has no seat yet: the operator already
    // sits in Northgate.
    await userEvent.click(await screen.findByText("Betreiberin"));
    await userEvent.click(screen.getByRole("button", { name: "Zu einer Organisation hinzufügen" }));
    const auswahl = screen.getByDisplayValue("Einer Organisation hinzufügen …") as HTMLSelectElement;
    expect(Array.from(auswahl.options).map((o) => o.text)).not.toContain("Northgate");

    await userEvent.selectOptions(auswahl, "55555555-5555-5555-5555-555555555555");
    await userEvent.click(screen.getByRole("button", { name: "Hinzufügen" }));
    expect(calls).toContain("POST /api/v1/platform/orgs/55555555-5555-5555-5555-555555555555/members");
  });

  it("zeigt geänderte Schalter samt ihrer Vorgabe", async () => {
    mockFetch(routen);
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/settings", path: "/platform/*" });

    expect(await screen.findByText("signup.mode")).toBeInTheDocument();
    // Unchanged: only "default". Changed: the default it changed away from —
    // otherwise nobody knows where to go back to.
    expect(screen.getByText("Vorgabe: covey")).toBeInTheDocument();
  });

  it("hält den Klartext eines neuen Codes fest, bis er weggeklickt wird", async () => {
    mockFetch({ ...routen, "POST /api/v1/platform/waitlist-codes": { code: "COVEY-4K7MQ-P2D9X" } });
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/waitlist", path: "/platform/*" });

    expect(await screen.findByText("Konferenz X")).toBeInTheDocument();
    expect(screen.getByText("1 von 3 genutzt")).toBeInTheDocument();

    await userEvent.type(screen.getByPlaceholderText("Konferenz X, Pilotkunde Y"), "Pilot");
    await userEvent.click(screen.getByRole("button", { name: "Code erzeugen" }));

    // The plaintext exists exactly once. If it vanished on the next render,
    // the code would be lost.
    expect(await screen.findByText("COVEY-4K7MQ-P2D9X")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Verstanden" }));
    expect(screen.queryByText("COVEY-4K7MQ-P2D9X")).not.toBeInTheDocument();
  });

  it("sagt, was bei Push gilt, und lädt das Dienstkonto als Text hoch (#431)", async () => {
    const push = {
      mode: "direct",
      relay_url: "https://app.covey.work",
      relay_accept: false,
      credentials: { set: false },
      last_test_at: "",
      last_test_error: "",
    };
    const { calls } = mockFetch({
      ...routen,
      "PATCH /api/v1/platform/push": { ...push, mode: "off" },
      "PUT /api/v1/platform/push/credentials": {
        ...push,
        credentials: { set: true, source: "settings", project_id: "covey-test", client_email: "push@example.org" },
      },
      "/api/v1/platform/push": push,
    });
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/push", path: "/platform/*" });

    // Direct without an account sends nothing, and the page says so.
    expect(await screen.findByTestId("push-status")).toHaveTextContent("kein brauchbares Dienstkonto");
    expect(screen.getByRole("button", { name: "Prüfen" })).toBeDisabled();

    const json = '{"type":"service_account","project_id":"covey-test"}';
    await userEvent.upload(
      screen.getByLabelText("Dienstkonto hochladen"),
      new File([json], "service-account.json", { type: "application/json" }),
    );
    expect(await screen.findByText("covey-test")).toBeInTheDocument();
    const sent = vi.mocked(fetch).mock.calls.find(([, init]) => init?.method === "PUT");
    expect(JSON.parse(String(sent?.[1]?.body))).toEqual({ credentials: json });

    await userEvent.click(screen.getByLabelText(/^Aus/));
    await userEvent.click(screen.getByRole("button", { name: "Speichern" }));
    expect(calls).toContain("PATCH /api/v1/platform/push");
    expect(await screen.findByTestId("push-status")).toHaveTextContent("Push-Mitteilungen sind aus");
  });
});
