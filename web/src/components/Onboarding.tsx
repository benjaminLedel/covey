import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { api, type OnboardingState, type Principal } from "../api";

// The first steps to the first working agent — as a checklist over the real
// state of the organisation, not as a clicked tour.
//
// A tour tells what to do and keeps telling it when one has long done it; if it
// points at a button the next rebuild dropped, it becomes a trap. This list
// asks the server what is really there. Each tick is a fact, and when all is
// done it disappears by itself — permanently, with nobody having to click it
// away.
//
// It shows only for roles that can carry out the steps: whoever may neither
// create agents nor store secrets gets no list of tasks that they cannot
// finish.

const STEPS: Array<{ key: string; to: string }> = [
  // The entry leads to the setup, no longer to the secrets page: the value is
  // checked there, the workspace around it comes with it, and the next two
  // questions stand right beside it (spec/20).
  { key: "credential", to: "/setup" },
  { key: "agent", to: "/" },
  { key: "config", to: "/" },
  { key: "task", to: "/" },
  { key: "run", to: "/" },
];

const DISMISS_KEY = "covey.onboarding.dismissed";

export function Onboarding({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const mayAct = me.Role === "org_admin" || me.Role === "agent_owner";
  const [dismissed, setDismissed] = useState(
    () => localStorage.getItem(DISMISS_KEY) === "1",
  );
  const state = useQuery({
    queryKey: ["onboarding"],
    queryFn: () => api<OnboardingState>("/onboarding"),
    enabled: mayAct && !dismissed,
    retry: false,
    // The state changes while one works through the list — coming back to
    // the overview the tick should be there.
    staleTime: 0,
    refetchOnMount: "always",
  });

  // A broken data plane keeps the card visible, even when the list is
  // through: whoever has every tick and still gets no run going should find
  // the reason where they started the way.
  const problems = state.data?.data_plane?.problems ?? [];
  if (!mayAct || dismissed || !state.data) return null;
  if (state.data.done && problems.length === 0) return null;
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
      {problems.length > 0 && (
        <div className="onboarding-warn mb-3" style={{ maxWidth: 640 }}>
          <strong className="text-xs">{t("onboarding.dataPlane.title")}</strong>
          <ul className="text-xs mt-1">
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </div>
      )}
      <ol className="onboarding-steps">
        {STEPS.map(({ key, to }) => {
          const step = state.data!.steps.find((s) => s.key === key);
          const isDone = step?.done ?? false;
          // The open step is the first one still missing — only it gets the
          // link. Everything before is done, everything after assumes it.
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
