import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import MobilePairing, { pairingPayload } from "./MobilePairing";
import { mockFetch, useGerman } from "../test/render";

beforeEach(() => useGerman());

const renderCard = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      <MobilePairing />
    </QueryClientProvider>,
  );

describe("MobilePairing (#330)", () => {
  it("trägt Adresse und Code in der Form, die die App liest", () => {
    expect(pairingPayload("https://app.covey.work", "coveypair_abc-_")).toBe(
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
