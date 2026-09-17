import { useEffect, useState } from "react";
import { api } from "../api";

/* Whether this installation accepts registrations — the one question that both
   the signup page and the sign-in card must ask.

   Why it is asked at all: covey is self-hosted by third parties (README). On
   an internal installation there is no self-registration, and then the UI may
   not offer it either — a button that leads to a "closed" is an invitation
   that is none. The answer therefore comes from the server and not from the
   build (FR-002). */

export type SignupMode = "off" | "waitlist" | "open";

export type SignupState = {
  mode: SignupMode;
  /* What this installation calls itself — stands on the page and later in the
     mails. */
  site_name: string;
  /* The public source of this program (buildinfo.SourceURL). It comes without
     a session because the duty from the AGPL applies without one — and
     because the star on GitHub comes from people standing in front of the
     sign-in page. A fork shows its own address here. */
  source: string;
};

/* Closed is the safe answer: as long as the endpoint is missing (older
   installation) or does not answer, the UI offers nothing. */
const GESCHLOSSEN: SignupState = { mode: "off", site_name: "covey", source: "" };

export function useSignupState(): { state: SignupState; loading: boolean } {
  /* During prerendering (prerender.mjs) no effect runs, so the page is made
     in the loading state — and that is how the browser renders it first. If
     this already said "closed", the first paint would differ from the
     prerendered HTML and React would discard it (same as in LoginCard). */
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

/* Start a registration. The code is checked together with the data — there is
   deliberately no endpoint of its own that merely confirms that a code is
   valid (FR-002, D3): that would be an oracle for trying codes through. */
export type SignupInput = {
  code: string;
  email: string;
  display_name: string;
  password: string;
  /* The language of the signup page. It goes along because the mail needs it:
     the server does not otherwise know the browser's choice, and a
     confirmation in English to someone who has just registered in Polish is a
     break in the middle of the process. */
  lang: string;
};

export type SignupResult = {
  ok: boolean;
  /* Whether a confirmation mail is on its way. The server decides this, not
     the page: as long as no mail sending is set up, the address counts as
     confirmed at once — and then "we wrote to you" would be a statement about
     a mail that does not exist. */
  verification_sent: boolean;
};

export const signup = (input: SignupInput) =>
  api<SignupResult>("/public/signup", {
    method: "POST",
    body: JSON.stringify(input),
  });

/* Confirmation and password reset (#168).

   All four endpoints answer deliberately uniform: "accepted" does not mean
   "this address exists". Someone who wanted to read an answer to find out who
   has an account here learns nothing from it. */

export const verifyAddress = (token: string) =>
  api<{ ok: boolean; email: string }>("/public/verify", {
    method: "POST",
    body: JSON.stringify({ token }),
  });

export const resendVerification = (email: string, lang: string) =>
  api<{ ok: boolean }>("/public/verify/resend", {
    method: "POST",
    body: JSON.stringify({ email, lang }),
  });

export const requestPasswordReset = (email: string, lang: string) =>
  api<{ ok: boolean }>("/public/password-reset", {
    method: "POST",
    body: JSON.stringify({ email, lang }),
  });

export const confirmPasswordReset = (token: string, password: string) =>
  api<{ ok: boolean; email: string }>("/public/password-reset/confirm", {
    method: "POST",
    body: JSON.stringify({ token, password }),
  });
