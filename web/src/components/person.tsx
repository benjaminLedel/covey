import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import type { Human } from "../api";
import Gesicht, { type Zustand } from "./Gesicht";

export const initials = (name: string) =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .map((p) => p[0])
    .slice(0, 2)
    .join("")
    .toUpperCase() || "?";

/* Ein Agent bekommt sein Gesicht, ein Mensch sein Monogramm.
 *
 * Beides steht hier zusammen, weil beides dasselbe beantwortet: Wer ist das?
 * Und es steht NUR hier, damit ein Agent in der Konsole, im Organigramm und
 * im Team dasselbe Gesicht trägt — zwei Zeichnungen für denselben Kollegen
 * wären zwei Kollegen.
 *
 * Ein Mensch bekommt ausdrücklich keines: Das Gesicht sagt „das ist eine
 * Software, die für euch arbeitet", und dieser Satz gilt nur für die eine
 * Hälfte der Belegschaft. */
export function Avatar({
  name,
  size = 32,
  human,
  slug,
  zustand,
}: {
  name: string;
  size?: number;
  human?: boolean;
  /** Das Kürzel des Agenten — aus ihm entsteht das Gesicht. */
  slug?: string;
  zustand?: Zustand;
}) {
  if (!human && slug) {
    return (
      <span className="avatar-gesicht" style={{ width: size, height: size }}>
        <Gesicht schluessel={slug} zustand={zustand} groesse={size} />
      </span>
    );
  }
  return (
    <div className={`avatar${human ? " hum" : ""}`} style={{ width: size, height: size, fontSize: Math.round(size * 0.38) }}>
      {initials(name)}
    </div>
  );
}

export function PersonLink({ human }: { human: Pick<Human, "id" | "display_name"> }) {
  const { t } = useTranslation();
  return (
    <Link to={`/people/${human.id}`} style={{ color: "inherit" }} title={t("org.openProfile")}>
      {human.display_name}
    </Link>
  );
}
