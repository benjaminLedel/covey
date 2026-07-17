import { useEffect, useRef, type ReactNode } from "react";

// Modal: zentrierter Dialog mit Backdrop in vernünftigen Maßen — Breite nach
// size, nie breiter als der Viewport, Inhalt scrollt ab 85vh. Schließt per
// Escape und Klick auf den Backdrop; der Fokus startet im Dialog.
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
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    // Fokus in den Dialog holen, damit Escape/Tab dort landen.
    ref.current?.focus();
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
          <button className="icon-btn" onClick={onClose} aria-label="Schließen" title="Schließen (Esc)">
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

// ConfirmDialog: Bestätigung für destruktive Aktionen — ersetzt window.confirm
// durch einen Dialog in der Design-Sprache der App.
export function ConfirmDialog({
  title,
  children,
  confirmLabel = "Löschen",
  onConfirm,
  onClose,
  pending = false,
}: {
  title: string;
  children: ReactNode;
  confirmLabel?: string;
  onConfirm: () => void;
  onClose: () => void;
  pending?: boolean;
}) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      size="sm"
      footer={
        <>
          <button className="btn sm" onClick={onClose}>Abbrechen</button>
          <button className="btn sm danger" disabled={pending} onClick={onConfirm}>
            {confirmLabel}
          </button>
        </>
      }
    >
      {children}
    </Modal>
  );
}
