import { describe, expect, it } from "vitest";
import { upstreamIssueURL } from "./upstream";

const gh = { system: "github", project: "benjaminLedel/covey" };

describe("vorbefüllter Issue-Link", () => {
  it("trägt Titel und Rumpf ins Formular des Zielsystems", () => {
    const url = upstreamIssueURL({ repo: gh, title: "checkout räumt fremde Bäume", body: "Schritt 1\nSchritt 2" })!;
    expect(url.startsWith("https://github.com/benjaminLedel/covey/issues/new?")).toBe(true);
    const q = new URL(url).searchParams;
    expect(q.get("title")).toBe("checkout räumt fremde Bäume");
    expect(q.get("body")).toBe("Schritt 1\nSchritt 2");
  });

  it("kennt die anderen Parameternamen von GitLab", () => {
    const url = upstreamIssueURL({ repo: { system: "gitlab", project: "gruppe/projekt" }, title: "T", body: "B" })!;
    expect(url.startsWith("https://gitlab.com/gruppe/projekt/-/issues/new?")).toBe(true);
    const q = new URL(url).searchParams;
    expect(q.get("issue[title]")).toBe("T");
    expect(q.get("issue[description]")).toBe("B");
  });

  // Lieber kein Knopf als einer, der ins Leere führt.
  it("liefert nichts, wo es kein Ziel gibt", () => {
    expect(upstreamIssueURL({ repo: {}, title: "T", body: "B" })).toBeNull();
    expect(upstreamIssueURL({ repo: { system: "github" }, title: "T", body: "B" })).toBeNull();
    // "-" ist in der Plattform das ausdrückliche „meldet nirgendwohin".
    expect(upstreamIssueURL({ repo: { system: "github", project: "-" }, title: "T", body: "B" })).toBeNull();
    // Ein System, dessen Formular wir nicht kennen.
    expect(upstreamIssueURL({ repo: { system: "jira", project: "X" }, title: "T", body: "B" })).toBeNull();
  });

  it("hält die URL unter der Grenze und sagt, dass gekürzt wurde", () => {
    const url = upstreamIssueURL({
      repo: gh,
      title: "Langer Befund",
      body: "Zeile mit Umlauten äöü\n".repeat(2000),
      truncationNote: "\n\n… gekürzt, vollständig im Wiki.",
    })!;
    expect(url.length).toBeLessThanOrEqual(6000);
    expect(new URL(url).searchParams.get("body")).toContain("gekürzt");
  });

  it("lässt einen kurzen Rumpf unangetastet", () => {
    const body = "Kurz und vollständig.";
    const url = upstreamIssueURL({ repo: gh, title: "T", body, truncationNote: "\n\n… gekürzt" })!;
    expect(new URL(url).searchParams.get("body")).toBe(body);
  });

  it("kürzt einen überlangen Titel, statt die URL zu sprengen", () => {
    const url = upstreamIssueURL({ repo: gh, title: "x".repeat(500), body: "B" })!;
    expect(new URL(url).searchParams.get("title")!.length).toBe(200);
  });
});
