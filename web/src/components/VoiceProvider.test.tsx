import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { SpeechForm } from "./SpeechForm";
import { VoiceProviderSettings } from "./VoiceProviderSettings";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

beforeEach(() => useGerman());

// The one voice source (#497): the organisation's voice provider, whose key
// is never shown and whose test says whether it speaks; and a voice's spoken
// voice there — a name, a style hint, a speed — with a preview through it.

describe("the spoken voice", () => {
  it("saves the voice name, the style and the speed", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: true } });
    const onSave = vi.fn();
    renderWithProviders(<SpeechForm value={null} name="Kollegial" editable saving={false} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Stimme beim Anbieter"), { target: { value: " af_bella " } });
    fireEvent.change(screen.getByLabelText("Stil (ein kurzer Hinweis auf Englisch)"), { target: { value: "calm and warm" } });
    fireEvent.change(screen.getByLabelText("Tempo 1.00×"), { target: { value: "1.1" } });
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    expect(onSave).toHaveBeenCalledWith({ voice: "af_bella", instructions: "calm and warm", speed: 1.1 });
  });

  it("clears to the provider's default", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: true } });
    const onSave = vi.fn();
    renderWithProviders(<SpeechForm value={{ voice: "alloy" }} name="Kollegial" editable saving={false} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Stimme beim Anbieter"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    expect(onSave).toHaveBeenCalledWith({ voice: undefined, instructions: undefined, speed: undefined });
  });

  it("plays a sample sentence through the provider with what is typed", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: true }, "POST /api/v1/speech/synthesize": "audio" });
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:x"), revokeObjectURL: vi.fn() }));
    const play = vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    renderWithProviders(<SpeechForm value={{ voice: "alloy", speed: 1.2 }} name="Kollegial" editable saving={false} onSave={() => {}} />);
    fireEvent.click(await screen.findByRole("button", { name: "Anhören" }));
    await waitFor(() => expect(play).toHaveBeenCalled());
    const [, init] = vi.mocked(fetch).mock.calls.find(([u]) => String(u).endsWith("/speech/synthesize"))!;
    expect(JSON.parse(String(init!.body))).toEqual({
      text: "Hallo, hier ist Kollegial. So klinge ich in einem Anruf.",
      voice: "alloy",
      speed: 1.2,
    });
    expect(screen.getByRole("button", { name: "Stopp" })).toBeInTheDocument();
  });

  it("says when there is no provider, and offers no preview", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: false } });
    renderWithProviders(<SpeechForm value={null} name="Kollegial" editable saving={false} onSave={() => {}} />);
    expect(await screen.findByText(/Kein Stimmen-Anbieter eingerichtet/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Anhören" })).not.toBeInTheDocument();
  });

  it("is locked for a role that may not change it", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: true } });
    renderWithProviders(<SpeechForm value={null} name="Kollegial" editable={false} saving={false} onSave={() => {}} />);
    expect(screen.getByLabelText("Stimme beim Anbieter")).toHaveAttribute("readonly");
    expect(screen.queryByRole("button", { name: "Speichern" })).not.toBeInTheDocument();
  });
});

describe("the organisation's voice provider", () => {
  const own = {
    base_url: "https://speech.example.org",
    model: "kokoro",
    voice: "",
    key_set: true,
    transcribe: false,
    transcribe_model: "whisper-1",
    effective: { source: "own", base_url: "https://speech.example.org" },
  };

  it("shows whether a key is stored, never the key, and sends a new one only when typed", async () => {
    mockFetch({ "/api/v1/org/voice-provider": own, "PATCH /api/v1/org/voice-provider": own });
    renderWithProviders(<VoiceProviderSettings me={{ Role: "org_admin" }} />);
    expect(await screen.findByRole("heading", { name: "Stimmen-Anbieter" })).toBeInTheDocument();
    const key = screen.getByPlaceholderText("gespeichert — neuen eingeben, um ihn zu ersetzen");
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

  it("tests the saved settings and says the outcome", async () => {
    mockFetch({
      "/api/v1/org/voice-provider": own,
      "POST /api/v1/org/voice-provider/test": { ok: false, error: "the voice provider answered HTTP 401" },
    });
    renderWithProviders(<VoiceProviderSettings me={{ Role: "org_admin" }} />);
    fireEvent.click(await screen.findByRole("button", { name: "Testen" }));
    expect(await screen.findByText("Funktioniert nicht: the voice provider answered HTTP 401")).toBeInTheDocument();
  });

  it("says when educa AI is in use, and that recognition sends the audio away", async () => {
    mockFetch({
      "/api/v1/org/voice-provider": {
        ...own,
        base_url: "",
        model: "",
        key_set: false,
        effective: { source: "educa", base_url: "https://api.educaai.de" },
      },
      "POST /api/v1/org/voice-provider/test": { ok: true, ms: 420, bytes: 1000 },
    });
    renderWithProviders(<VoiceProviderSettings me={{ Role: "org_admin" }} />);
    expect(await screen.findByText(/In Gebrauch: educa AI, https:\/\/api.educaai.de/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Testen" }));
    expect(await screen.findByText("Funktioniert: ein Satz wurde in 420 ms erzeugt.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox"));
    expect(screen.getByText(/verlässt der Ton jedes Redebeitrags/)).toBeInTheDocument();
    expect(screen.getByLabelText("Erkennungsmodell")).toBeInTheDocument();
  });

  it("offers no test while no provider is in effect", async () => {
    mockFetch({ "/api/v1/org/voice-provider": { ...own, base_url: "", key_set: false, effective: { source: "none", base_url: "" } } });
    renderWithProviders(<VoiceProviderSettings me={{ Role: "org_admin" }} />);
    expect(await screen.findByText("Kein Stimmen-Anbieter: Anrufe sprechen mit der Stimme des Macs.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Testen" })).not.toBeInTheDocument();
  });

  it("is not shown to a role that may not set it", () => {
    mockFetch({});
    const { container } = renderWithProviders(<VoiceProviderSettings me={{ Role: "auditor" }} />);
    expect(container).toBeEmptyDOMElement();
  });
});

// The provider's named voices (#518): a choice grouped by language with
// the names a person reads, a Listen button for the chosen one, and the
// free text field where the provider lists none.
describe("the provider's voice list", () => {
  const list = {
    listed: true,
    default: { name: "thorsten", display_name: "Thorsten", language: "de" },
    voices: [
      { name: "katja", display_name: "Katja", language: "de" },
      { name: "thorsten", display_name: "Thorsten", language: "de" },
      { name: "linda", display_name: "Linda", language: "en" },
    ],
  };
  const own = {
    base_url: "",
    model: "",
    voice: "",
    key_set: false,
    transcribe: false,
    transcribe_model: "whisper-1",
    effective: { source: "educa", base_url: "https://api.educaai.de" },
  };

  it("offers the spoken voice as a choice grouped by language", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: true }, "/api/v1/org/voice-provider/voices": list });
    const onSave = vi.fn();
    renderWithProviders(<SpeechForm value={null} name="Kollegial" editable saving={false} onSave={onSave} />);
    const select = await screen.findByRole("combobox", { name: "Stimme beim Anbieter" });
    expect(screen.getByRole("group", { name: "Deutsch" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Englisch" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "automatisch zugewiesen" })).toBeInTheDocument();
    fireEvent.change(select, { target: { value: "katja" } });
    fireEvent.click(screen.getByRole("button", { name: "Speichern" }));
    expect(onSave).toHaveBeenCalledWith({ voice: "katja", instructions: undefined, speed: undefined });
  });

  it("keeps a name the list no longer has", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: true }, "/api/v1/org/voice-provider/voices": list });
    renderWithProviders(<SpeechForm value={{ voice: "af_bella" }} name="Kollegial" editable saving={false} onSave={() => {}} />);
    const select = await screen.findByRole("combobox", { name: "Stimme beim Anbieter" });
    expect(select).toHaveValue("af_bella");
    expect(screen.getByRole("option", { name: "af_bella (nicht in der Liste des Anbieters)" })).toBeInTheDocument();
  });

  it("lets the default voice be chosen and heard in the settings", async () => {
    mockFetch({
      "/api/v1/org/voice-provider/voices": list,
      "/api/v1/org/voice-provider": own,
      "POST /api/v1/speech/synthesize": "audio",
    });
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:x"), revokeObjectURL: vi.fn() }));
    const play = vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    renderWithProviders(<VoiceProviderSettings me={{ Role: "org_admin" }} />);
    const select = await screen.findByRole("combobox", { name: "Standardstimme" });
    fireEvent.change(select, { target: { value: "linda" } });
    fireEvent.click(screen.getByRole("button", { name: "Anhören" }));
    await waitFor(() => expect(play).toHaveBeenCalled());
    const [, init] = vi.mocked(fetch).mock.calls.find(([u]) => String(u).endsWith("/speech/synthesize"))!;
    const body = JSON.parse(String(init!.body));
    expect(body.voice).toBe("linda");
    expect(body.language).toBe("en");
  });

  it("keeps free text for a provider without a list", async () => {
    mockFetch({ "/api/v1/speech/model": { synthesize: true }, "/api/v1/org/voice-provider/voices": { listed: false, default: null, voices: [] } });
    renderWithProviders(<SpeechForm value={null} name="Kollegial" editable saving={false} onSave={() => {}} />);
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Stimme beim Anbieter" })).toBeInTheDocument());
    expect(screen.queryByRole("combobox", { name: "Stimme beim Anbieter" })).not.toBeInTheDocument();
  });
});
