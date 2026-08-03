import { Link, type LinkProps } from "react-router";
import { usePublicLang } from "./lang";
import { pathOf } from "./seo";

/* Interner Link auf der öffentlichen Website: Er benennt die Seite, nicht den
   Pfad. So bleibt eine englische Seite beim Klick englisch, und die Pfade
   stehen an einer Stelle (seo.ts) statt in fünfzehn JSX-Zeilen. */
export default function PubLink({
  id,
  ...rest
}: { id: string } & Omit<LinkProps, "to">) {
  const lang = usePublicLang();
  return <Link to={pathOf(id, lang)} {...rest} />;
}
