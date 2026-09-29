import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import type { Voice } from "../../api";
import { Avatar } from "../../components/person";
import { PurposeIcon, StepMark } from "./VoiceFlow";
import { pendingDraft, type StepStatus } from "./flow";

/* What a voice is and where it stands, in one line (#464, #466): purpose,
   source, state as a drawn mark, and who carries it. The list row and the
   detail page's head show the same line, so a voice reads the same in both. */

export const canEditVoices = (role: string) => role === "org_admin" || role === "agent_owner";

export function VoiceMeta({ voice }: { voice: Voice }) {
  const { t } = useTranslation();
  const described = voice.source === "described";
  const pending = pendingDraft(voice);
  const state: StepStatus = voice.released_at && !pending ? "done" : pending ? "open" : "optional";
  const stateLabel =
    state === "done" ? t("voices.cardReleased") : state === "open" ? t("voices.flow.draftWaiting") : t("voices.flow.nothingYet");

  return (
    <ul className="vc-meta">
      <li>
        {voice.purpose ? (
          <>
            <PurposeIcon purpose={voice.purpose} />
            {t(`voices.flow.purpose.${voice.purpose}`, voice.purpose)}
          </>
        ) : (
          <span className="muted">{t("voices.flow.noPurpose")}</span>
        )}
      </li>
      <li>
        {described ? (
          <span className="pill">{t("voices.flow.describedPill")}</span>
        ) : voice.version > 0 ? (
          <span className="pill ok" title={t("voices.builtFrom", { documents: voice.documents, words: voice.words })}>
            {t("voices.flow.measured", { version: voice.version })}
          </span>
        ) : (
          <span className="pill mut">{t("voices.unbuilt")}</span>
        )}
      </li>
      <li>
        <StepMark status={state} />
        {stateLabel}
      </li>
      <li className="vc-carriers">
        <span className="muted">{t("voices.carriedBy")}</span>
        {voice.agents?.length ? (
          voice.agents.map((a) => (
            <Link key={a.id} to={`/agents/${a.id}`} className="vc-agent">
              <Avatar name={a.display_name} slug={a.slug} size={18} />
              {a.display_name}
            </Link>
          ))
        ) : (
          <span className="muted">{t("voices.nobody")}</span>
        )}
      </li>
      {voice.drafted_by && <li className="muted">{t("voices.flow.draftedByShort", { name: voice.drafted_by.display_name })}</li>}
    </ul>
  );
}

/* The menu behind the head's "more" button: the actions a voice has that are
   not what it waits for. */
export function MoreMenu({ items }: { items: { label: string; onSelect: () => void; danger?: boolean; disabled?: boolean }[] }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  return (
    <div className="vc-more" ref={ref}>
      <button
        type="button"
        className="vc-more-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("voices.flow.more")}
        onClick={() => setOpen(!open)}
      >
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <circle cx="3.5" cy="8" r="1.25" />
          <circle cx="8" cy="8" r="1.25" />
          <circle cx="12.5" cy="8" r="1.25" />
        </svg>
      </button>
      {open && (
        <div className="vc-menu" role="menu">
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              className={it.danger ? "danger" : ""}
              disabled={it.disabled}
              onClick={() => {
                setOpen(false);
                it.onSelect();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
