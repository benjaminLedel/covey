import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SignUp from "./SignUp";
import { mockFetch, renderWithProviders, useGerman } from "../test/render";

// The sign-up page has three states, and the most important is the one where
// it offers NOTHING: covey is self-hosted by third parties, and an internal
// installation takes in no strangers. Whether sign-up is possible is
// answered by the server (signup-state) — the page does not decide that
// itself and does not guess it either (FR-002).

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
    // Older installation: /public/signup-state answers with 404. Fail-closed
    // here means that no form comes out of it.
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
    // No jump into the application: the account is one only after the
    // confirmation, and the page says that, with the address the mail went to.
    expect(await screen.findByText("Fast geschafft")).toBeInTheDocument();
    expect(screen.getByText(/erika@example\.de/)).toBeInTheDocument();
  });

  it("verspricht keine Mail, wenn keine verschickt wurde", async () => {
    // Without mail delivery set up the address counts as confirmed right away.
    // The page must not then point at a confirmation that someone would
    // otherwise wait for until they give up.
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
    // A used-up code and a taken address are two different pieces of
    // information; whoever flattens both into "did not work" leaves the
    // person guessing.
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
