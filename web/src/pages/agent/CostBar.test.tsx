import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { CostBar } from "./CostBar";
import { mockFetch, renderWithProviders, useGerman } from "../../test/render";

/* The most read line of numbers in the interface stands above every agent — and
   it was the only one writing its numbers itself. On a productive instance it
   said `2817.0738 $` there and next to it `2,499,833,356 / 22,246,900`: four
   decimal places on a four-figure amount, and tokens with English
   commas that nobody reads as two and a half billion. */

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

    // A four-figure amount needs no tenth of a cent, but thousands separators.
    expect(await screen.findByText("2.817 $")).toBeInTheDocument();
    // And a budget the two digits it carries.
    expect(screen.getByText("540,00 $")).toBeInTheDocument();
  });

  it("kürzt die Tokenzahlen und hält die genauen im Tooltip bereit", async () => {
    useGerman();
    mockFetch(kosten);
    renderWithProviders(<CostBar agentId="a1" budget={0} />);

    // 178.000 input + 2,4 bn cache read + 99,7 M cache created.
    // `2500 M` would stand there without the billion step — and that is counted
    // out digit by digit again.
    expect(await screen.findByText("2,5 Mrd / 22,2 M")).toBeInTheDocument();
    // The exact number stays reachable, for whoever checks an invoice.
    expect(screen.getByTitle("2.499.833.401 / 22.246.900")).toBeInTheDocument();
  });
});
