import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Markdown } from "./Markdown";

/* The renderer carries two roles that have nothing to do with each other: it
   renders a model's answer WITHIN a page, and in the
   docs area it is the page itself. What is right in one role is
   wrong in the other — which is why both stand here side by side. */

describe("Überschriften-Ebene", () => {
  it("bleibt im Standardfall bei h4 — die umgebende Seite hat ihr h1 schon", () => {
    const { container } = render(<Markdown text={"# Titel\n\n## Zwischentitel"} />);
    expect(container.querySelector("h4")?.textContent).toBe("Titel");
    expect(container.querySelector("h5")?.textContent).toBe("Zwischentitel");
    expect(container.querySelector("h1")).toBeNull();
  });

  it("gibt dem Docs-Bereich eine echte Hierarchie", () => {
    const { container } = render(
      <Markdown baseLevel={1} text={"# Titel\n\n## Abschnitt\n\n### Unterpunkt"} />,
    );
    expect(container.querySelector("h1")?.textContent).toBe("Titel");
    expect(container.querySelector("h2")?.textContent).toBe("Abschnitt");
    expect(container.querySelector("h3")?.textContent).toBe("Unterpunkt");
  });

  it("läuft nicht über h6 hinaus", () => {
    const { container } = render(<Markdown baseLevel={6} text={"### Tief"} />);
    expect(container.querySelector("h6")?.textContent).toBe("Tief");
  });
});

describe("Links", () => {
  it("verlinkt interne Adressen im selben Fenster", () => {
    render(<Markdown text="Siehe [das Gedächtnis](/docs/gedaechtnis)." />);
    const a = screen.getByRole("link", { name: "das Gedächtnis" });
    expect(a.getAttribute("href")).toBe("/docs/gedaechtnis");
    expect(a.getAttribute("target")).toBeNull();
  });

  it("öffnet fremde Adressen in einem neuen Fenster, mit noopener", () => {
    render(<Markdown text="Siehe [GitHub](https://github.com/benjaminLedel/covey)." />);
    const a = screen.getByRole("link", { name: "GitHub" });
    expect(a.getAttribute("target")).toBe("_blank");
    expect(a.getAttribute("rel")).toContain("noopener");
  });

  it("verlinkt nichts, was kein sicheres Schema hat", () => {
    // javascript: as an address is the classic way to run code
    // out of a model answer. And //fremde.example looks relative, but leads
    // outward protocol-relative — both stay text.
    const { container } = render(
      <Markdown text={"[klick](javascript:alert(1)) [weg](//fremde.example/x)"} />,
    );
    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain("klick");
    expect(container.textContent).toContain("weg");
  });
});

/* Tables came in with #225: an agent that reports numbers writes a
   table, and without this branch it stood as a row of pipes in the
   running text. */
describe("Tabellen", () => {
  const tabelle = [
    "| Fenster | Klicks |",
    "|---|---:|",
    "| 08-11 … 08-24 | 1 |",
    "| 08-25 … 09-07 | 4 |",
  ].join("\n");

  it("macht aus Kopfzeile, Trennzeile und Datenzeilen eine Tabelle", () => {
    const { container } = render(<Markdown text={tabelle} />);
    expect(container.querySelectorAll("thead th")).toHaveLength(2);
    expect(container.querySelectorAll("tbody tr")).toHaveLength(2);
    expect(container.querySelectorAll("tbody tr")[1].textContent).toContain("08-25");
  });

  it("übernimmt die Ausrichtung aus der Trennzeile", () => {
    const { container } = render(<Markdown text={tabelle} />);
    const zelle = container.querySelectorAll("tbody td")[1] as HTMLElement;
    expect(zelle.style.textAlign).toBe("right");
  });

  it("lässt die Auszeichnung in den Zellen gelten", () => {
    const { container } = render(
      <Markdown text={"| a | b |\n|---|---|\n| **fett** | `code` |"} />,
    );
    expect(container.querySelector("tbody strong")?.textContent).toBe("fett");
    expect(container.querySelector("tbody code")?.textContent).toBe("code");
  });

  it("trennt den Absatz davor von der Tabelle", () => {
    // Without the boundary in the paragraph branch the paragraph swallows the header row,
    // and the separator row stays standing as a row of dashes.
    const { container } = render(<Markdown text={"Reichweite:\n" + tabelle} />);
    expect(container.querySelector("p.md-p")?.textContent).toBe("Reichweite:");
    expect(container.querySelector("table")).not.toBeNull();
  });

  it("lässt einen Absatz mit einem einzelnen Rohr in Ruhe", () => {
    const { container } = render(<Markdown text={"a | b ist kein Tabellenkopf"} />);
    expect(container.querySelector("table")).toBeNull();
    expect(container.querySelector("p.md-p")?.textContent).toContain("a | b");
  });
});
