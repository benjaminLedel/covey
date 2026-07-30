import { useEffect, useState } from "react";
import { useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { getHelpTopics, topicForPath } from "../help";

export default function HelpDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const location = useLocation();
  const { t, i18n } = useTranslation();
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

  const topics = getHelpTopics(i18n.language);

  return (
    <>
      <div className="help-backdrop" onClick={onClose} />
      <aside className="help-drawer" role="dialog" aria-label={t("help.title")}>
        <div className="flex items-center gap-3 mb-4">
          <h2 className="text-[17px]">{t("help.title")}</h2>
          <span className="muted text-xs">
            {t("help.openHint")} <kbd>?</kbd>
          </span>
          <button className="btn sm ml-auto" onClick={onClose} aria-label={t("help.close")}>
            ✕
          </button>
        </div>
        {topics.map((topic) => {
          const isOpen = active === topic.id;
          return (
            <section key={topic.id} className="help-topic">
              <button
                className={`help-topic-head ${isOpen ? "open" : ""}`}
                onClick={() => setActive(isOpen ? "" : topic.id)}
                aria-expanded={isOpen}
              >
                <span>{topic.title}</span>
                <span className="muted">{isOpen ? "−" : "+"}</span>
              </button>
              {isOpen && <div className="help-body fade">{topic.body}</div>}
            </section>
          );
        })}
        <p className="muted text-xs mt-4">{t("help.spec")}</p>
      </aside>
    </>
  );
}
