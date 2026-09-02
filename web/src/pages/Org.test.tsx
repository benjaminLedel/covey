import { describe, it, expect, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import Org, { PlatformRepo } from "./Org";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

/* Die Karte „Quelltext dieser Plattform" ist eine Einstellung für genau einen
   Leser: covey Doctor. Was sie vorher nicht zeigte, war beides — ob es diesen
   Leser überhaupt gibt, und ob die Einstellung bei ihm ankommt. Das
   Stammdatum allein ist die halbe Einrichtung; die andere Hälfte ist eine
   Zeile in seiner ACCESS.md, und die stand nur als Satz im Formular. */

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
};

const system = (access: boolean, enabled = true) => [
  { name: "gitlab", label: "GitLab", kind: "builtin", enabled, access },
];

/* Die Voreinstellung kommt vom Server (buildinfo), nicht aus der Oberfläche —
   ein Fork trägt damit sein eigenes Projekt. */
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
    /* Eine 401 auf /org/chart (abgelaufene Sitzung) hat die Seite zerlegt: die
       Abfrage stand zwischen Fehlversuch und Wiederholung auf „pending, aber
       nicht unterwegs", und das `chart.data!` dahinter warf eine Ausnahme —
       React verwirft dann den ganzen Baum, und im Browser stand eine weisse
       Seite statt der Anmeldung. */
    mockFetch({}); // alles unbeantwortet → 404
    renderWithProviders(<Org />);

    expect(await screen.findByText("Org-Chart konnte nicht geladen werden.")).toBeInTheDocument();
  });
});

describe("Quelltext dieser Plattform", () => {
  it("sagt, wenn covey Doctor dort keinen Zugang hat", async () => {
    mockFetch(routen(true, false));
    renderWithProviders(<PlatformRepo />);

    expect(await screen.findByText(/Wirkt noch nicht/)).toBeInTheDocument();
    // Und den Weg dorthin, wo die fehlende Zeile hingehört.
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

  it("sagt, wenn das Zielsystem der Organisation nicht mehr freigeschaltet ist", async () => {
    mockFetch(routen(true, true, false));
    renderWithProviders(<PlatformRepo />);

    expect(await screen.findByText(/nicht freigeschaltet/)).toBeInTheDocument();
  });

  it("zeigt ohne eigenes Repository das Projekt, aus dem die Plattform stammt", async () => {
    /* Die Karte fragte nach etwas, das die Plattform über sich selbst weiß:
       Ihr Quelltext liegt da, wo sie herkommt (buildinfo.SourceURL). Ohne
       Eintrag steht deshalb die Voreinstellung da — nicht „nicht
       eingerichtet". */
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
    // Und der Zustand prüft gegen das voreingestellte System, nicht gegen ein
    // gespeichertes: hier hat covey Doctor Zugang zu github.
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
