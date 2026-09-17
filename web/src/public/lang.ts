/* On the two public pages the address decides the language; in the signed-in
   UI it is the personal setting. The reason outlived the website moving out
   (#130): /anmelden and /en/sign-in are two addresses that someone can share
   and link — and that the proxy forwards separately, ahead of the
   application. */

import { useLocation } from "react-router";
import { initialLang } from "../i18n";
import type { Lang } from "./routes";

/* The path has priority; where none carries a language (the redirect from `/`
   to the sign-in page), the stored choice counts and then the browser — the
   same order as at startup (i18n.ts). Before the ten languages there stood a
   fixed `"de"` here: with two catalogues only that was the one alternative to
   /en/…, with ten it would be a claim. */
export function usePublicLang(): Lang {
  const { pathname } = useLocation();
  return initialLang(pathname);
}
