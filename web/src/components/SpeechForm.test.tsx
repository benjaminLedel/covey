import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { SpeechForm } from "./SpeechForm";
import { SpeechServerSettings } from "./SpeechServerSettings";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

beforeEach(() => useGerman());

// The spoken voice on a voice (#497): a voice the instance offers for the
// device, the organisation's speech server when it has one, or nothing —
// then the app picks. And the server itself, whose key is never shown.

const norman = {
  name: "piper-en-norman",
  size: 20987233,
  voice: { family: "vits", language: "en-US", speakers: 1, label: "Norman", licence: "public domain", source: "https://example.org", placeholder: true },
};

describe("the spoken voice", () => {
  it("offers the device's voices, marks a placeholder and saves the choice", async () => {
    mockFetch({ "/api/v1/speech/model": { voices: [norman], synthesize: false } });
    const onSave = vi.fn();
    renderWithProviders(<SpeechForm value={null} editable saving={false} onSave={onSave} />);
    const source = await screen.findByRole("combobox", { name: "Quelle" });
    await waitFor(() => expect(screen.queryByRole("option", { name: "Sprachserver der Organisation" })).not.toBeInTheDocument());
    fireEvent.change(source, { target: { value: "device" } });
    const voice = await screen.findByRole("combobox", { name: "Stimme" });
    expect(screen.getByRole("option", { name: "Norman · en-US · Platzhalter" })).toBeInTheDocument();
    fireEvent.change(voice, { target: { value: "piper-en-norman" } });
    expect(screen.getByText("public domain")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    expect(onSave).toHaveBeenCalledWith({ source: "device", model: "piper-en-norman", speaker: 0, rate: undefined });
  });

  it("names a model and voice of the organisation's server when there is one", async () => {
    mockFetch({ "/api/v1/speech/model": { voices: [], synthesize: true } });
    const onSave = vi.fn();
    renderWithProviders(<SpeechForm value={null} editable saving={false} onSave={onSave} />);
    await screen.findByRole("option", { name: "Sprachserver der Organisation" });
    fireEvent.change(screen.getByRole("combobox", { name: "Quelle" }), { target: { value: "server" } });
    const save = screen.getByRole("button", { name: "Speichern" });
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Modell auf dem Server"), { target: { value: " kokoro " } });
    const voice = screen.getByLabelText("Stimme auf dem Server (optional)");
    expect(voice).toHaveAttribute("placeholder", "DEFAULT_VOICE");
    fireEvent.change(voice, { target: { value: "af_bella" } });
    fireEvent.click(save);
    expect(onSave).toHaveBeenCalledWith({ source: "server", model: "kokoro", voice: "af_bella", speaker: 0, rate: undefined });
  });

  it("clears to unset", async () => {
    mockFetch({ "/api/v1/speech/model": { voices: [norman] } });
    const onSave = vi.fn();
    renderWithProviders(
      <SpeechForm value={{ source: "device", model: "piper-en-norman", speaker: 0 }} editable saving={false} onSave={onSave} />,
    );
    const source = await screen.findByRole("combobox", { name: "Quelle" });
    expect(source).toHaveValue("device");
    fireEvent.change(source, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({}));
  });

  it("is locked for a role that may not change it", async () => {
    mockFetch({ "/api/v1/speech/model": { voices: [norman] } });
    renderWithProviders(<SpeechForm value={null} editable={false} saving={false} onSave={() => {}} />);
    expect(await screen.findByRole("combobox", { name: "Quelle" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Speichern" })).not.toBeInTheDocument();
  });
});

describe("the organisation's speech server", () => {
  it("shows whether a key is stored, never the key, and sends a new one only when typed", async () => {
    const server = {
      base_url: "https://speech.example.org",
      model: "kokoro",
      voice: "",
      key_set: true,
      transcribe: false,
      transcribe_model: "whisper-1",
      effective: { source: "own", base_url: "https://speech.example.org" },
    };
    mockFetch({ "/api/v1/org/speech-server": server, "PATCH /api/v1/org/speech-server": server });
    renderWithProviders(<SpeechServerSettings me={{ Role: "org_admin" }} />);
    const key = await screen.findByPlaceholderText("gespeichert — neuen eingeben, um ihn zu ersetzen");
    expect(key).toHaveValue("");
    expect(screen.getByText("In Gebrauch: Ihr eigener Server, https://speech.example.org")).toBeInTheDocument();
    expect(screen.queryByLabelText("Erkennungsmodell")).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole("textbox", { name: "Standardstimme" }), { target: { value: "af_bella" } });
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    await waitFor(() => expect(vi.mocked(fetch).mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(true));
    const [, init] = vi.mocked(fetch).mock.calls.find(([, i]) => i?.method === "PATCH")!;
    expect(JSON.parse(String(init!.body))).toEqual({
      base_url: "https://speech.example.org",
      model: "kokoro",
      voice: "af_bella",
      transcribe: false,
      transcribe_model: "whisper-1",
    });
  });

  it("says when educa AI is in use, and that recognition sends the audio away", async () => {
    mockFetch({
      "/api/v1/org/speech-server": {
        base_url: "",
        model: "",
        voice: "",
        key_set: false,
          transcribe: false,
        transcribe_model: "whisper-1",
        effective: { source: "educa", base_url: "https://api.educaai.de" },
      },
    });
    renderWithProviders(<SpeechServerSettings me={{ Role: "org_admin" }} />);
    expect(await screen.findByText(/In Gebrauch: educa AI, https:\/\/api.educaai.de/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox"));
    expect(screen.getByText(/verlässt der Ton jedes Redebeitrags/)).toBeInTheDocument();
    expect(screen.getByLabelText("Erkennungsmodell")).toBeInTheDocument();
  });

  it("is not shown to a role that may not set it", () => {
    mockFetch({});
    const { container } = renderWithProviders(<SpeechServerSettings me={{ Role: "auditor" }} />);
    expect(container).toBeEmptyDOMElement();
  });
});
