import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import Org, { PlatformRepo } from "./Org";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

/* Die Karte „Quelltext dieser Plattform" ist eine Einstellung für genau einen
   Leser: Covey Doctor. Was sie vorher nicht zeigte, war beides — ob es diesen
   Leser überhaupt gibt, und ob die Einstellung bei ihm ankommt. Das
   Stammdatum allein ist die halbe Einrichtung; die andere Hälfte ist eine
   Zeile in seiner ACCESS.md, und die stand nur als Satz im Formular. */

const DOCTOR = "dddddddd-dddd-dddd-dddd-dddddddddddd";

const chart = (mitDoctor: boolean) => ({
  humans: [],
  agents: mitDoctor
    ? [{ id: DOCTOR, slug: "covey-doctor", display_name: "Covey Doctor" }]
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

const routen = (mitDoctor: boolean, access: boolean, enabled = true) => ({
  "/api/v1/org/chart": chart(mitDoctor),
  "/api/v1/org": org,
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
  it("steht am Organigramm nur, wo es Covey Doctor gibt", async () => {
    mockFetch(routen(false, true));
    const { container } = renderWithProviders(<PlatformRepo nurMitDoctor />);

    // Nichts — kein Formular für einen Agenten, den diese Organisation nicht
    // hat. Auf den Stammdaten der Verwaltung steht dieselbe Karte weiterhin.
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it("sagt, wenn Covey Doctor dort keinen Zugang hat", async () => {
    mockFetch(routen(true, false));
    renderWithProviders(<PlatformRepo nurMitDoctor />);

    expect(await screen.findByText(/Wirkt noch nicht/)).toBeInTheDocument();
    // Und den Weg dorthin, wo die fehlende Zeile hingehört.
    expect(screen.getByRole("link", { name: "Covey Doctor" })).toHaveAttribute(
      "href",
      `/agents/${DOCTOR}?tab=config`,
    );
  });

  it("sagt, dass es wirkt, wenn der Zugang steht", async () => {
    mockFetch(routen(true, true));
    renderWithProviders(<PlatformRepo nurMitDoctor />);

    expect(await screen.findByText(/^Wirkt:/)).toBeInTheDocument();
    expect(screen.queryByText(/Wirkt noch nicht/)).not.toBeInTheDocument();
  });

  it("sagt, wenn das Zielsystem der Organisation nicht mehr freigeschaltet ist", async () => {
    mockFetch(routen(true, true, false));
    renderWithProviders(<PlatformRepo nurMitDoctor />);

    expect(await screen.findByText(/nicht mehr freigeschaltet/)).toBeInTheDocument();
  });
});
