import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import MobilePairing, { appLink, macAppLink, pairingPayload } from "./MobilePairing";
import { mockFetch, useGerman } from "../test/render";

beforeEach(() => useGerman());

const renderCard = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      <MobilePairing />
    </QueryClientProvider>,
  );

describe("MobilePairing (#330)", () => {
  it("bietet die Mac-App aus dem Release der eigenen Version an (#408)", () => {
    expect(macAppLink("https://github.com/benjaminLedel/covey", "v0.9.0")).toBe(
      "https://github.com/benjaminLedel/covey/releases/download/v0.9.0/covey-app_v0.9.0_macos.zip",
    );
    // Between tags there is no release of its own: the latest one.
    expect(macAppLink("https://github.com/benjaminLedel/covey/", "v0.8.9-154-g7ca91cd8")).toBe(
      "https://github.com/benjaminLedel/covey/releases/latest",
    );
    // A fork elsewhere gets no link rather than a wrong one.
    expect(macAppLink("https://git.example.org/team/covey", "v0.9.0")).toBeNull();
  });

  it("zeigt den Download auf der Karte", async () => {
    mockFetch({ "/api/v1/version": { version: "v0.9.0", source: "https://github.com/benjaminLedel/covey" } });
    renderCard();
    const link = await screen.findByText("Für den Mac herunterladen");
    expect(link.getAttribute("href")).toContain("/releases/download/v0.9.0/covey-app_v0.9.0_macos.zip");
  });

  it("trägt einen https-Link, den die Kamera des Telefons öffnet (#333)", () => {
    expect(pairingPayload("https://app.covey.work", "coveypair_abc-_")).toBe(
      "https://app.covey.work/pair?code=coveypair_abc-_",
    );
  });

  it("gibt der Desktop-App denselben Code über covey://", () => {
    expect(appLink("https://app.covey.work", "coveypair_abc-_")).toBe(
      "covey://pair?instance=https%3A%2F%2Fapp.covey.work&code=coveypair_abc-_",
    );
  });

  it("zeigt den QR-Code und sagt, mit welchem Gerät gekoppelt wurde", async () => {
    const id = "11111111-2222-3333-4444-555555555555";
    const expires = new Date(Date.now() + 5 * 60_000).toISOString();
    mockFetch({
      "POST /api/v1/auth/pairings": { id, code: "coveypair_test", expires_at: expires },
      [`/api/v1/auth/pairings/${id}`]: { id, expires_at: expires, redeemed_at: new Date().toISOString(), device: "Adas iPhone" },
    });
    renderCard();

    fireEvent.click(screen.getByText("Mobile App koppeln"));
    expect(await screen.findByText(/Gekoppelt mit Adas iPhone/)).toBeInTheDocument();
  });
});
