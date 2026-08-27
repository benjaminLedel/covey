import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { CostBar } from "./CostBar";
import { mockFetch, renderWithProviders, useGerman } from "../../test/render";

/* Die meistgelesene Zahlenzeile der Oberfläche steht über jedem Agenten — und
   schrieb ihre Zahlen als einzige selbst. Auf einer produktiven Instanz stand
   dort „2817.0738 $" und daneben „2,499,833,356 / 22,246,900": vier
   Nachkommastellen auf einem vierstelligen Betrag, und Tokens mit englischen
   Kommas, die niemand als zweieinhalb Milliarden liest. */

const kosten = {
  "/api/v1/agents/a1/cost": {
    total_usd: 2817.0738,
    input_tokens: 178_000,
    output_tokens: 22_246_900,
    cache_read_tokens: 2_400_000_000,
    cache_creation_tokens: 99_655_401,
    entries: 805,
  },
};

describe("CostBar", () => {
  it("schreibt Betrag und Anzahlen in der Form der übrigen Oberfläche", async () => {
    useGerman();
    mockFetch(kosten);
    renderWithProviders(<CostBar agentId="a1" budget={540} />);

    // Ein vierstelliger Betrag braucht keine Zehntelcent, aber Tausenderpunkte.
    expect(await screen.findByText("2.817 $")).toBeInTheDocument();
    // Und ein Budget die zwei Stellen, die es trägt.
    expect(screen.getByText("540,00 $")).toBeInTheDocument();
  });

  it("kürzt die Tokenzahlen und hält die genauen im Tooltip bereit", async () => {
    useGerman();
    mockFetch(kosten);
    renderWithProviders(<CostBar agentId="a1" budget={0} />);

    // 178.000 Eingabe + 2,4 Mrd gelesener Cache + 99,7 Mio erzeugter Cache.
    // „2500 M" stünde da ohne die Milliarden-Stufe — und das zählt man wieder
    // ziffernweise nach.
    expect(await screen.findByText("2,5 Mrd / 22,2 M")).toBeInTheDocument();
    // Die genaue Zahl bleibt erreichbar, für den, der eine Rechnung prüft.
    expect(screen.getByTitle("2.499.833.401 / 22.246.900")).toBeInTheDocument();
  });
});
