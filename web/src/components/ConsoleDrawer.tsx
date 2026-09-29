import { useEffect, useRef, useSyncExternalStore, type RefObject } from "react";

/* The console at phone width (#463). Beside the rail the console's
 * navigation takes 240 px; on a narrow window that left the page a column
 * one word wide. Below this width the navigation becomes a drawer behind a
 * button in a bar above the page, the same break at which the team shell lets
 * list and conversation take turns (the team uses 720 px; the console gives
 * up earlier because its pages are tables and boards, not a thread).
 *
 * The width lives here and in app.css (`.konsole-*` under the same media
 * query); the two must agree. */
export const CONSOLE_NARROW = "(max-width: 900px)";

/** Whether the window is below the console's break. */
export function useNarrow(query = CONSOLE_NARROW): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const m = window.matchMedia?.(query);
      m?.addEventListener?.("change", onChange);
      return () => m?.removeEventListener?.("change", onChange);
    },
    () => window.matchMedia?.(query).matches ?? false,
    () => false,
  );
}

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/* A drawer that behaves like a dialog while it is open: focus moves into it,
 * Tab stays inside it, Escape closes it, the page behind it does not scroll,
 * and on closing focus returns to what opened it. */
export function useDrawer(open: boolean, onClose: () => void, panel: RefObject<HTMLElement | null>, opener: RefObject<HTMLElement | null>) {
  const close = useRef(onClose);
  close.current = onClose;

  useEffect(() => {
    if (!open) return;
    const el = panel.current;
    const returnTo = opener.current;
    const first = el?.querySelector<HTMLElement>('[aria-current="page"]') ?? el?.querySelector<HTMLElement>(FOCUSABLE);
    first?.focus();

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close.current();
        return;
      }
      if (e.key !== "Tab" || !el) return;
      const items = [...el.querySelectorAll<HTMLElement>(FOCUSABLE)];
      if (items.length === 0) return;
      const head = items[0];
      const tail = items[items.length - 1];
      const active = document.activeElement;
      if (e.shiftKey && (active === head || !el.contains(active))) {
        e.preventDefault();
        tail.focus();
      } else if (!e.shiftKey && (active === tail || !el.contains(active))) {
        e.preventDefault();
        head.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
      /* Back to the button while focus is still in the drawer (it is about
         to turn inert) or was lost; not if someone clicked elsewhere, the
         search or the person's menu in the rail, which took it on purpose. */
      if (!document.activeElement || document.activeElement === document.body || el?.contains(document.activeElement)) {
        returnTo?.focus();
      }
    };
  }, [open, panel, opener]);
}
