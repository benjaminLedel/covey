import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { api, type OnboardingState, type Principal } from "../api";

// Die ersten Schritte zum ersten arbeitenden Agenten — als Checkliste über den
// echten Zustand der Organisation, nicht als geklickte Tour.
//
// Eine Tour erzählt, was zu tun ist, und erzählt es weiter, wenn man es längst
// getan hat; zeigt sie auf einen Knopf, den es nach dem nächsten Umbau nicht
// mehr gibt, wird sie zur Falle. Diese Liste fragt den Server, was tatsächlich
// da ist. Jeder Haken ist eine Tatsache, und ist alles erledigt, verschwindet
// sie von selbst — dauerhaft, ohne dass jemand sie wegklicken muss.
//
// Sie erscheint nur für Rollen, die die Schritte auch ausführen können: Wer
// weder Agenten anlegen noch Secrets hinterlegen darf, bekommt keine Liste mit
// Aufgaben, die er nicht erledigen kann.

const STEPS: Array<{ key: string; to: string }> = [
  { key: "credential", to: "/secrets" },
  { key: "agent", to: "/" },
  { key: "config", to: "/" },
  { key: "task", to: "/" },
  { key: "run", to: "/" },
];

const DISMISS_KEY = "covey.onboarding.dismissed";

export function Onboarding({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const mayAct = me.Role === "platform_admin" || me.Role === "agent_owner";
  const [dismissed, setDismissed] = useState(
    () => localStorage.getItem(DISMISS_KEY) === "1",
  );
  const state = useQuery({
    queryKey: ["onboarding"],
    queryFn: () => api<OnboardingState>("/onboarding"),
    enabled: mayAct && !dismissed,
    retry: false,
    // Der Zustand ändert sich, während man die Liste abarbeitet — beim
    // Zurückkommen auf die Übersicht soll der Haken da sein.
    staleTime: 0,
    refetchOnMount: "always",
  });

  if (!mayAct || dismissed || !state.data || state.data.done) return null;
  const done = state.data.steps.filter((s) => s.done).length;

  return (
    <div className="card mb-4 onboarding">
      <div className="flex items-center gap-3 mb-1 flex-wrap">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>
          {t("onboarding.title")}
        </h2>
        <span className="muted text-xs">
          {t("onboarding.progress", { done, total: state.data.steps.length })}
        </span>
        <span className="ml-auto" />
        <button
          className="btn sm"
          style={{ border: "none" }}
          onClick={() => {
            localStorage.setItem(DISMISS_KEY, "1");
            setDismissed(true);
          }}
        >
          {t("onboarding.hide")}
        </button>
      </div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("onboarding.lead")}
      </p>
      <ol className="onboarding-steps">
        {STEPS.map(({ key, to }) => {
          const step = state.data!.steps.find((s) => s.key === key);
          const isDone = step?.done ?? false;
          // Der offene Schritt ist der erste, der noch fehlt — nur er bekommt
          // den Link. Alles davor ist erledigt, alles danach setzt ihn voraus.
          const isNext = !isDone && state.data!.steps.find((s) => !s.done)?.key === key;
          return (
            <li key={key} className={isDone ? "done" : isNext ? "next" : ""}>
              <span className="mark" aria-hidden="true">
                {isDone ? (
                  <svg viewBox="0 0 24 24" className="file-ic">
                    <path d="M4 12.5l5 5L20 6.5" />
                  </svg>
                ) : (
                  <span className="dot" />
                )}
              </span>
              <span className="min-w-0">
                <span className="what">{t(`onboarding.steps.${key}.title`)}</span>{" "}
                <span className="muted text-xs">{t(`onboarding.steps.${key}.hint`)}</span>
              </span>
              {isNext && (
                <Link className="btn sm primary ml-auto" to={to}>
                  {t(`onboarding.steps.${key}.action`)}
                </Link>
              )}
            </li>
          );
        })}
      </ol>
    </div>
  );
}
