import { describe, it, expect, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Platform from "./Platform";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

// Das Plattform-Panel verwaltet die INSTALLATION. Was diese Tests festhalten,
// ist vor allem die Grenze, die dabei sichtbar bleiben muss: die Ebene rechts
// gehört der Instanz, die Rolle am Sitz einer Organisation — dieselbe
// Unterscheidung, die FR-003 Befund F erzwungen hat.

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
    // Die Organisations-Rolle steht am Sitz …
    expect(screen.getByText(/Northgate \(Org-Admin\)/)).toBeInTheDocument();
    // … die Instanz-Ebene daneben, als eigenes Feld.
    const ebenen = screen.getAllByRole("combobox");
    expect((ebenen[0] as HTMLSelectElement).value).toBe("system_admin");

    // Ein Konto ohne Sitz ist kein Fehler, sondern der Zustand nach einer
    // Selbstregistrierung — und muss als solcher lesbar sein.
    expect(screen.getByText("in keiner Organisation")).toBeInTheDocument();
    expect(screen.getByText("nie angemeldet")).toBeInTheDocument();
  });

  it("zeigt geänderte Schalter samt ihrer Vorgabe", async () => {
    mockFetch(routen);
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/settings", path: "/platform/*" });

    expect(await screen.findByText("signup.mode")).toBeInTheDocument();
    // Unverändert: nur "Vorgabe". Geändert: die Vorgabe, gegen die es sich
    // geändert hat — sonst weiß niemand, wohin zurück.
    expect(screen.getByText("Vorgabe: covey")).toBeInTheDocument();
  });

  it("hält den Klartext eines neuen Codes fest, bis er weggeklickt wird", async () => {
    mockFetch({ ...routen, "POST /api/v1/platform/waitlist-codes": { code: "COVEY-4K7MQ-P2D9X" } });
    renderWithProviders(<Platform me={systemadmin} />, { route: "/platform/waitlist", path: "/platform/*" });

    expect(await screen.findByText("Konferenz X")).toBeInTheDocument();
    expect(screen.getByText("1 von 3 genutzt")).toBeInTheDocument();

    await userEvent.type(screen.getByPlaceholderText("Konferenz X, Pilotkunde Y"), "Pilot");
    await userEvent.click(screen.getByRole("button", { name: "Code erzeugen" }));

    // Der Klartext existiert genau einmal. Verschwände er beim nächsten
    // Rendern, wäre der Code verloren.
    expect(await screen.findByText("COVEY-4K7MQ-P2D9X")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Verstanden" }));
    expect(screen.queryByText("COVEY-4K7MQ-P2D9X")).not.toBeInTheDocument();
  });
});
