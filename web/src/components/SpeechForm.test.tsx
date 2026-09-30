import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { SpeechForm } from "./SpeechForm";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

beforeEach(() => useGerman());

// The spoken voice on a voice (#497): the voices the instance offers, the
// device's own, or nothing — then the app picks.

const offered = {
  voices: [
    {
      name: "piper-en-norman",
      size: 20987233,
      voice: { family: "vits", language: "en-US", speakers: 1, label: "Norman", licence: "public domain", source: "https://example.org", placeholder: true },
    },
  ],
};

describe("the spoken voice", () => {
  it("lists the offered voices, marks a placeholder and saves the choice", async () => {
    mockFetch({ "/api/v1/speech/model": offered });
    const onSave = vi.fn();
    renderWithProviders(<SpeechForm value={null} editable saving={false} onSave={onSave} />);
    const select = await screen.findByRole("combobox", { name: "Stimme" });
    await screen.findByRole("option", { name: "Norman · en-US · Platzhalter" });
    fireEvent.change(select, { target: { value: "piper-en-norman" } });
    expect(screen.getByText("public domain")).toBeInTheDocument();
    expect(screen.queryByText("Sprecher")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    expect(onSave).toHaveBeenCalledWith({ engine: "sherpa-onnx", model: "piper-en-norman", speaker: 0, rate: undefined });
  });

  it("clears to unset, and says when the instance offers no voices", async () => {
    mockFetch({ "/api/v1/speech/model": { enabled: true } });
    const onSave = vi.fn();
    renderWithProviders(<SpeechForm value={{ engine: "system", speaker: 0 }} editable saving={false} onSave={onSave} />);
    expect(await screen.findByText(/keine eigenen Stimmen/)).toBeInTheDocument();
    const select = screen.getByRole("combobox", { name: "Stimme" });
    expect(select).toHaveValue("system");
    fireEvent.change(select, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({}));
  });

  it("is locked for a role that may not change it", async () => {
    mockFetch({ "/api/v1/speech/model": offered });
    renderWithProviders(<SpeechForm value={null} editable={false} saving={false} onSave={() => {}} />);
    expect(await screen.findByRole("combobox", { name: "Stimme" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Speichern" })).not.toBeInTheDocument();
  });
});
