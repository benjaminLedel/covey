import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SignUp from "./SignUp";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

// Die Registrierungsseite hat drei Zustände, und der wichtigste ist der, in
// dem sie NICHTS anbietet: covey wird von Dritten selbst betrieben, und eine
// interne Installation nimmt keine Fremden auf. Ob registriert werden kann,
// beantwortet der Server (signup-state) — die Seite entscheidet das nicht
// selbst und rät es auch nicht (FR-002).

const STATE = "/api/v1/public/signup-state";
const SIGNUP = "POST /api/v1/public/signup";

beforeEach(useGerman);

describe("SignUp", () => {
  it("bietet ohne offene Registrierung kein Formular an", async () => {
    mockFetch({ [STATE]: { mode: "off", site_name: "covey" } });
    renderWithProviders(<SignUp />);

    expect(await screen.findByText("Registrierung geschlossen")).toBeInTheDocument();
    expect(screen.queryByLabelText("E-Mail")).not.toBeInTheDocument();
  });

  it("bleibt geschlossen, wenn es den Endpunkt gar nicht gibt", async () => {
    // Ältere Installation: /public/signup-state antwortet mit 404. Fail-closed
    // heißt hier, dass daraus kein Formular wird.
    mockFetch({});
    renderWithProviders(<SignUp />);

    expect(await screen.findByText("Registrierung geschlossen")).toBeInTheDocument();
  });

  it("verlangt im Wartelisten-Modus einen Code", async () => {
    mockFetch({ [STATE]: { mode: "waitlist", site_name: "covey" } });
    renderWithProviders(<SignUp />);

    expect(await screen.findByLabelText("Wartelisten-Code")).toBeRequired();
    expect(screen.getByLabelText("E-Mail")).toBeInTheDocument();
    expect(screen.getByLabelText("Passwort")).toHaveAttribute("minlength", "8");
  });

  it("fragt im offenen Modus nicht nach einem Code", async () => {
    mockFetch({ [STATE]: { mode: "open", site_name: "covey" } });
    renderWithProviders(<SignUp />);

    expect(await screen.findByLabelText("E-Mail")).toBeInTheDocument();
    expect(screen.queryByLabelText("Wartelisten-Code")).not.toBeInTheDocument();
  });

  it("schickt Code, Name, Adresse und Passwort — und bestätigt danach per Mail", async () => {
    const { calls } = mockFetch({
      [STATE]: { mode: "waitlist", site_name: "covey" },
      [SIGNUP]: { ok: true, verification_sent: true },
    });
    renderWithProviders(<SignUp />);

    await screen.findByLabelText("Wartelisten-Code");
    await userEvent.type(screen.getByLabelText("Wartelisten-Code"), "COVEY-1234");
    await userEvent.type(screen.getByLabelText("Name"), "Erika Musterfrau");
    await userEvent.type(screen.getByLabelText("E-Mail"), "erika@example.de");
    await userEvent.type(screen.getByLabelText("Passwort"), "hinreichend-lang");
    await userEvent.click(screen.getByRole("button", { name: "Konto anlegen" }));

    await waitFor(() => expect(calls).toContain("POST /api/v1/public/signup"));
    // Kein Sprung in die Anwendung: Das Konto ist erst nach der Bestätigung
    // eines, und die Seite sagt genau das, samt der Adresse, an die sie ging.
    expect(await screen.findByText("Fast geschafft")).toBeInTheDocument();
    expect(screen.getByText(/erika@example\.de/)).toBeInTheDocument();
  });

  it("verspricht keine Mail, wenn keine verschickt wurde", async () => {
    // Ohne eingerichteten Mailversand gilt die Adresse sofort als bestätigt.
    // Die Seite darf dann nicht auf eine Bestätigung verweisen, auf die
    // jemand sonst wartet, bis er aufgibt.
    mockFetch({
      [STATE]: { mode: "waitlist", site_name: "covey" },
      [SIGNUP]: { ok: true, verification_sent: false },
    });
    renderWithProviders(<SignUp />);

    await screen.findByLabelText("Wartelisten-Code");
    await userEvent.type(screen.getByLabelText("Wartelisten-Code"), "COVEY-1234");
    await userEvent.type(screen.getByLabelText("Name"), "Erika Musterfrau");
    await userEvent.type(screen.getByLabelText("E-Mail"), "erika@example.de");
    await userEvent.type(screen.getByLabelText("Passwort"), "hinreichend-lang");
    await userEvent.click(screen.getByRole("button", { name: "Konto anlegen" }));

    expect(await screen.findByText("Konto angelegt")).toBeInTheDocument();
    expect(screen.queryByText("Fast geschafft")).not.toBeInTheDocument();
  });

  it("zeigt die Begründung des Servers, statt sie zu verallgemeinern", async () => {
    // Ein verbrauchter Code und eine vergebene Adresse sind zwei verschiedene
    // Auskünfte; wer sie beide zu "hat nicht geklappt" einebnet, lässt den
    // Menschen raten.
    mockFetch({ [STATE]: { mode: "waitlist", site_name: "covey" } });
    renderWithProviders(<SignUp />);

    await screen.findByLabelText("Wartelisten-Code");
    await userEvent.type(screen.getByLabelText("Wartelisten-Code"), "VERBRAUCHT");
    await userEvent.type(screen.getByLabelText("Name"), "Erika Musterfrau");
    await userEvent.type(screen.getByLabelText("E-Mail"), "erika@example.de");
    await userEvent.type(screen.getByLabelText("Passwort"), "hinreichend-lang");
    await userEvent.click(screen.getByRole("button", { name: "Konto anlegen" }));

    expect(await screen.findByText("nicht gemockt")).toBeInTheDocument();
  });
});
