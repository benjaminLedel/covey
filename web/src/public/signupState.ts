import { useEffect, useState } from "react";
import { api } from "../api";

/* Ob diese Installation Registrierungen annimmt — die eine Frage, die sowohl
   die Registrierungsseite als auch die Anmelde-Karte stellen müssen.

   Warum überhaupt gefragt wird: covey wird von Dritten selbst betrieben
   (README). Auf einer internen Installation gibt es keine Selbstregistrierung,
   und dann darf die Oberfläche sie auch nicht anbieten — ein Knopf, der zu
   einem "geschlossen" führt, ist eine Einladung, die keine ist. Die Antwort
   kommt deshalb vom Server und nicht aus dem Build (FR-002). */

export type SignupMode = "off" | "waitlist" | "open";

export type SignupState = {
  mode: SignupMode;
  /* Wie sich diese Installation nennt — steht auf der Seite und später in den
     Mails. */
  site_name: string;
  /* Die öffentliche Quelle dieses Programms (buildinfo.SourceURL). Sie kommt
     ohne Sitzung mit, weil die Pflicht aus der AGPL ohne Sitzung gilt — und
     weil der Stern auf GitHub von Leuten kommt, die vor der Anmeldung
     stehen. Ein Fork zeigt hier seine eigene Adresse. */
  source: string;
};

/* Geschlossen ist die sichere Antwort: Solange der Endpunkt fehlt (ältere
   Installation) oder nicht antwortet, bietet die Oberfläche nichts an. */
const GESCHLOSSEN: SignupState = { mode: "off", site_name: "covey", source: "" };

export function useSignupState(): { state: SignupState; loading: boolean } {
  /* Beim Vorrendern (prerender.mjs) läuft kein Effekt, die Seite entsteht also
     im Ladezustand — und genau so rendert der Browser sie zuerst. Würde hier
     schon "geschlossen" stehen, wiche die erste Darstellung vom vorgerenderten
     HTML ab und React verwürfe es (dieselbe Überlegung wie in LoginCard). */
  const [state, setState] = useState<SignupState>(GESCHLOSSEN);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let abgebrochen = false;
    api<SignupState>("/public/signup-state")
      .then((s) => !abgebrochen && setState(s))
      .catch(() => !abgebrochen && setState(GESCHLOSSEN))
      .finally(() => !abgebrochen && setLoading(false));
    return () => {
      abgebrochen = true;
    };
  }, []);

  return { state, loading };
}

/* Registrierung anstoßen. Der Code wird zusammen mit den Daten geprüft — es
   gibt bewusst keinen eigenen Endpunkt, der bloß die Gültigkeit eines Codes
   bestätigt (FR-002, D3): der wäre ein Orakel zum Durchprobieren. */
export type SignupInput = {
  code: string;
  email: string;
  display_name: string;
  password: string;
};

export type SignupResult = {
  ok: boolean;
  /* Ob eine Bestätigungsmail unterwegs ist. Das entscheidet der Server, nicht
     die Seite: solange kein Mailversand eingerichtet ist, gilt die Adresse
     sofort als bestätigt — und dann wäre "wir haben Ihnen geschrieben" eine
     Auskunft über eine Mail, die es nicht gibt. */
  verification_sent: boolean;
};

export const signup = (input: SignupInput) =>
  api<SignupResult>("/public/signup", {
    method: "POST",
    body: JSON.stringify(input),
  });
