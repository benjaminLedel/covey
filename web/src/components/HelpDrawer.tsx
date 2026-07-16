import { useEffect, useState } from "react";
import { useLocation } from "react-router-dom";
import { helpTopics, topicForPath } from "../help";

// Kontextsensitive Inline-Hilfe: öffnet als Slide-Over rechts, das Thema zur
// aktuellen Route ist vorausgeklappt. Schließen via ✕, Escape oder Backdrop.
export default function HelpDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const location = useLocation();
  const [active, setActive] = useState<string>(topicForPath(location.pathname));

  useEffect(() => {
    if (open) setActive(topicForPath(location.pathname));
  }, [open, location.pathname]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <>
      <div className="help-backdrop" onClick={onClose} />
      <aside className="help-drawer" role="dialog" aria-label="Hilfe">
        <div className="flex items-center gap-3 mb-4">
          <h2 className="text-[17px]">Hilfe</h2>
          <span className="muted text-xs">
            öffnen mit <kbd>?</kbd>
          </span>
          <button className="btn sm ml-auto" onClick={onClose} aria-label="Hilfe schließen">
            ✕
          </button>
        </div>
        {helpTopics.map((t) => {
          const isOpen = active === t.id;
          return (
            <section key={t.id} className="help-topic">
              <button
                className={`help-topic-head ${isOpen ? "open" : ""}`}
                onClick={() => setActive(isOpen ? "" : t.id)}
                aria-expanded={isOpen}
              >
                <span>{t.title}</span>
                <span className="muted">{isOpen ? "−" : "+"}</span>
              </button>
              {isOpen && <div className="help-body fade">{t.body}</div>}
            </section>
          );
        })}
        <p className="muted text-xs mt-4">
          Vertiefung: die Spezifikation im Repository unter <span className="mono">spec/</span>.
        </p>
      </aside>
    </>
  );
}
