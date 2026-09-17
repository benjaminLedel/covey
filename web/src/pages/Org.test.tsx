import { describe, it, expect, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import Org, { PlatformRepo } from "./Org";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

/* The card "source code of this platform" is a setting for exactly one
   reader: covey Doctor. What it did not show before was both things — whether
   this reader exists at all, and whether the setting reaches him. The master
   data alone is half of the setup; the other half is a line in his ACCESS.md,
   and that stood only as a sentence in the form. */

const DOCTOR = "dddddddd-dddd-dddd-dddd-dddddddddddd";

const chart = (mitDoctor: boolean) => ({
  humans: [],
  agents: mitDoctor
    ? [{ id: DOCTOR, slug: "covey-doctor", display_name: "covey Doctor" }]
    : [{ id: "1111", slug: "alice", display_name: "Alice Beispiel" }],
  departments: [],
});

const org = {
  id: "oooo",
  name: "Digital Learning GmbH",
  description: "",
  platform_repo_system: "gitlab",
  platform_repo_project: "gruppe/covey",
  /* Since the platform files itself (#200): whether an account is on file is
     computed by the server. Without the field nothing gets reported — and that
     is exactly what the test further below checks. */
  platform_repo_can_file: true,
};

const system = (access: boolean, enabled = true) => [
  { name: "gitlab", label: "GitLab", kind: "builtin", enabled, access },
];

/* The default comes from the server (buildinfo), not from the interface —
   a fork therefore carries its own project with it. */
const build = {
  version: "v0.4.0",
  commit: "abc1234",
  built_at: "",
  dirty: false,
  go: "",
  source: "https://github.com/benjaminLedel/covey",
  source_system: "github",
  source_project: "benjaminLedel/covey",
};

const routen = (mitDoctor: boolean, access: boolean, enabled = true) => ({
  "/api/v1/org/chart": chart(mitDoctor),
  "/api/v1/org": org,
  "/api/v1/version": build,
  "/api/v1/targets": [{ name: "gitlab", label: "GitLab", enabled: true }],
  [`/api/v1/agents/${DOCTOR}/systems`]: system(access, enabled),
});

beforeEach(useGerman);

describe("Organigramm", () => {
  it("bleibt stehen, wenn das Chart nicht geladen werden kann", async () => {
    /* A 401 on /org/chart (expired session) took the page apart: the query
       stood between failed attempt and retry at "pending, but not in flight",
       and the `chart.data!` behind it threw an exception — React then discards
       the whole tree, and the browser showed a blank page instead of the
       sign-in. */
    mockFetch({}); // nothing answered → 404
    renderWithProviders(<Org />);

    expect(await screen.findByText("Org-Chart konnte nicht geladen werden.")).toBeInTheDocument();
  });
});

describe("Quelltext dieser Plattform", () => {
  it("sagt, wenn covey Doctor dort keinen Lesezugang hat", async () => {
    mockFetch(routen(true, false));
    renderWithProviders(<PlatformRepo />);

    /* Filing still happens — that goes over the platform. The missing line
       only costs the look into the source code. */
    expect(await screen.findByText(/Wirkt halb/)).toBeInTheDocument();
    // And the way there, where the missing line belongs.
    expect(screen.getByRole("link", { name: "covey Doctor" })).toHaveAttribute(
      "href",
      `/agents/${DOCTOR}?tab=config`,
    );
  });

  it("sagt, dass es wirkt, wenn der Zugang steht", async () => {
    mockFetch(routen(true, true));
    renderWithProviders(<PlatformRepo />);

    expect(await screen.findByText(/^Wirkt:/)).toBeInTheDocument();
    expect(screen.queryByText(/Wirkt noch nicht/)).not.toBeInTheDocument();
  });

  it("sagt, wenn kein Konto zum Einreichen hinterlegt ist", async () => {
    /* The state that looked for eleven days like a finished setup: the address
       stands there, and nothing still gets filed (#200). */
    mockFetch({
      ...routen(true, true),
      "/api/v1/org": { ...org, platform_repo_can_file: false },
    });
    renderWithProviders(<PlatformRepo />);

    expect(await screen.findByText(/Es wird nichts eingereicht/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Secrets" })).toHaveAttribute("href", "/secrets");
  });

  it("sagt, wenn das Zielsystem der Organisation nicht mehr freigeschaltet ist", async () => {
    mockFetch(routen(true, true, false));
    renderWithProviders(<PlatformRepo />);

    expect(await screen.findByText(/nicht freigeschaltet/)).toBeInTheDocument();
  });

  it("zeigt ohne eigenes Repository das Projekt, aus dem die Plattform stammt", async () => {
    /* The card asked about something the platform knows about itself: its
       source code lies where it comes from (buildinfo.SourceURL). Without an
       entry the default therefore stands there — not "not
       set up". */
    mockFetch({
      ...routen(true, true),
      "/api/v1/org": { ...org, platform_repo_system: "", platform_repo_project: "" },
      [`/api/v1/agents/${DOCTOR}/systems`]: [
        { name: "github", label: "GitHub", kind: "builtin", enabled: true, access: true },
      ],
    });
    renderWithProviders(<PlatformRepo />);

    expect(await screen.findByText("benjaminLedel/covey")).toBeInTheDocument();
    expect(screen.getByText(/Voreinstellung/)).toBeInTheDocument();
    // And the state checks against the preset system, not against a
    // stored one: here covey Doctor has access to github.
    expect(await screen.findByText(/^Wirkt:/)).toBeInTheDocument();
  });

  it("lässt sich ganz abschalten", async () => {
    mockFetch({
      ...routen(true, true),
      "/api/v1/org": { ...org, platform_repo_system: "-", platform_repo_project: "" },
    });
    renderWithProviders(<PlatformRepo />);

    expect(await screen.findByText(/Abgeschaltet/)).toBeInTheDocument();
    expect(screen.queryByText(/Wirkt/)).not.toBeInTheDocument();
  });
});
