import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Markdown } from "./Markdown";

/* Der Renderer trägt zwei Rollen, die nichts miteinander zu tun haben: Er
   stellt die Antwort eines Modells INNERHALB einer Seite dar, und er ist im
   Docs-Bereich die Seite selbst. Was in der einen Rolle richtig ist, ist in
   der anderen falsch — deshalb steht beides hier nebeneinander. */

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
    // javascript: als Adresse ist der klassische Weg, aus einer Modellantwort
    // heraus Code auszuführen. Und //fremde.example sieht relativ aus, führt
    // aber protokollrelativ nach außen — beides bleibt Text.
    const { container } = render(
      <Markdown text={"[klick](javascript:alert(1)) [weg](//fremde.example/x)"} />,
    );
    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain("klick");
    expect(container.textContent).toContain("weg");
  });
});
