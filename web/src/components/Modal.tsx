import { useEffect, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export function Modal({
  title,
  onClose,
  children,
  size = "md",
  footer,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  size?: "sm" | "md" | "lg";
  footer?: ReactNode;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    if (!ref.current?.contains(document.activeElement)) ref.current?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={ref}
        className={`modal ${size}`}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
      >
        <div className="modal-head">
          <h2>{title}</h2>
          <button className="icon-btn" onClick={onClose} aria-label={t("modal.close")} title={`${t("modal.close")} (Esc)`}>
            <svg viewBox="0 0 24 24" className="ic" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  );
}

export function ConfirmDialog({
  title,
  children,
  confirmLabel,
  onConfirm,
  onClose,
  pending = false,
  danger = true,
}: {
  title: string;
  children: ReactNode;
  confirmLabel?: string;
  onConfirm: () => void;
  onClose: () => void;
  pending?: boolean;
  /** Red confirm for what destroys; a change made on purpose (a lead, a
   *  move) takes the primary button. */
  danger?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <Modal
      title={title}
      onClose={onClose}
      size="sm"
      footer={
        <>
          <button className="btn sm" onClick={onClose}>{t("modal.cancel")}</button>
          <button className={`btn sm ${danger ? "danger" : "primary"}`} disabled={pending} onClick={onConfirm}>
            {confirmLabel ?? t("modal.delete")}
          </button>
        </>
      }
    >
      {children}
    </Modal>
  );
}
