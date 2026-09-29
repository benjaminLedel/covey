import { useEffect, useState, type ReactNode, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { NavIcon } from "../components/navicons";

/* The head of a conversation (#440): who one talks to, and what one can do
 * there — in the same column as the messages, not stretched to the edges of
 * the window.
 *
 * It stands over the messages, not above them: they scroll under a surface
 * that lets them show through, and the line that separates the two appears
 * only once something is under it. At rest the head is part of the page.
 *
 * The actions are few and named: the frequent ones as buttons with a label
 * that shows on hover and focus, the rest in one menu, the way the user
 * menu groups them (#432). On a narrow window the list of conversations is
 * not beside the conversation, so the head carries the way back to it. */

export type Aktion = {
  icon: string;
  label: string;
  onClick?: () => void;
  to?: string;
  /** A toggle that is on (muted, a panel open). */
  an?: boolean;
  /** A small number on the button (tasks in the background). */
  zahl?: number;
  danger?: boolean;
};

export default function Kopf({
  zeichen,
  titel,
  zeile,
  aktionen,
  menue,
  verlauf,
  unter,
}: {
  zeichen: ReactNode;
  titel: ReactNode;
  zeile?: ReactNode;
  aktionen: Aktion[];
  menue: Aktion[];
  /** The scrolling list of messages — the head watches whether anything is under it. */
  verlauf: RefObject<HTMLElement | null>;
  /** A panel that opens from the head (members, background tasks). */
  unter?: ReactNode;
}) {
  const { t } = useTranslation();
  const [darunter, setDarunter] = useState(false);
  const [menueOffen, setMenueOffen] = useState(false);

  useEffect(() => {
    const el = verlauf.current;
    if (!el) return;
    const pruefen = () => setDarunter(el.scrollTop > 2);
    pruefen();
    el.addEventListener("scroll", pruefen, { passive: true });
    return () => el.removeEventListener("scroll", pruefen);
  }, [verlauf]);

  return (
    <header className={`tm-kopf${darunter ? " darunter" : ""}`}>
      <div className="tm-kopf-reihe">
        <Link to="/team" className="tm-knopf tm-kopf-zurueck" aria-label={t("conversation.back")} data-tip={t("conversation.back")}>
          <NavIcon name="chevron" />
        </Link>
        <div className="tm-kopf-wer">
          {zeichen}
          <div className="tm-kopf-text">
            <h1>{titel}</h1>
            {zeile && <p className="tm-kopf-zeile">{zeile}</p>}
          </div>
        </div>
        <div className="tm-kopf-aktionen">
          {aktionen.map((a) => (
            <Knopf key={a.label} a={a} />
          ))}
          {menue.length > 0 && (
            <div className="tm-kopf-mehr">
              <button
                className={`tm-knopf${menueOffen ? " an" : ""}`}
                onClick={() => setMenueOffen((v) => !v)}
                aria-label={t("conversation.more")}
                aria-expanded={menueOffen}
                aria-haspopup="menu"
                data-tip={menueOffen ? undefined : t("conversation.more")}
              >
                <NavIcon name="dots" />
              </button>
              {menueOffen && (
                <>
                  <div className="foot-menu-backdrop" onClick={() => setMenueOffen(false)} />
                  <div className="foot-menu" role="menu">
                    {menue.map((a, i) => {
                      const inhalt = (
                        <>
                          <NavIcon name={a.icon} />
                          <span>{a.label}</span>
                        </>
                      );
                      return (
                        <span key={a.label} style={{ display: "contents" }}>
                          {a.danger && i > 0 && <div className="sep" />}
                          {a.to ? (
                            <Link to={a.to} role="menuitem" className="tm-menue-link" onClick={() => setMenueOffen(false)}>
                              {inhalt}
                            </Link>
                          ) : (
                            <button
                              role="menuitem"
                              className={a.danger ? "danger" : a.an ? "on" : undefined}
                              onClick={() => {
                                setMenueOffen(false);
                                a.onClick?.();
                              }}
                            >
                              {inhalt}
                            </button>
                          )}
                        </span>
                      );
                    })}
                  </div>
                </>
              )}
            </div>
          )}
        </div>
      </div>
      {unter}
    </header>
  );
}

function Knopf({ a }: { a: Aktion }) {
  const inhalt = (
    <>
      <NavIcon name={a.icon} />
      {a.zahl ? <span className="tm-knopf-zahl">{a.zahl}</span> : null}
    </>
  );
  if (a.to) {
    return (
      <Link to={a.to} className="tm-knopf" aria-label={a.label} data-tip={a.label}>
        {inhalt}
      </Link>
    );
  }
  return (
    <button
      className={`tm-knopf${a.an ? " an" : ""}`}
      onClick={a.onClick}
      aria-label={a.label}
      aria-pressed={a.an === undefined ? undefined : a.an}
      data-tip={a.label}
    >
      {inhalt}
    </button>
  );
}

/* The quiet line under a colleague's name: role, department, and the state
   as the product draws it everywhere — a mark whose shape says it, and the
   word. */
export function Zustand({ zustand, text }: { zustand: "working" | "sleeping" | "killed"; text: string }) {
  return <span className={`tm-zustand state st-${zustand}`}>{text}</span>;
}

/* Faces stacked, for a group: the first few members and how many. */
export function Stapel({ children, zahl }: { children: ReactNode; zahl: number }) {
  return (
    <span className="tm-stapel">
      {children}
      <span className="tm-stapel-zahl">{zahl}</span>
    </span>
  );
}
