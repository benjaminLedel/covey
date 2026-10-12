/* How a workflow reaches a person in the preview (#560). A run that needs
   someone puts a card into that person's chat (#440, #441): an approval
   from the workflow, or a question from the colleague whose step it is.
   The preview shows the card where the run waits, so one can answer it
   there and watch the run go on. Nothing is sent anywhere. */
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { Out } from "./model";

export function DecisionCard({
  recipient,
  kind,
  question,
  context,
  options,
  decided,
  onDecide,
}: {
  recipient: string;
  kind: string;
  question: string;
  context?: string;
  options: Out[];
  decided?: string | null;
  onDecide?: (key: string) => void;
}) {
  const { t } = useTranslation();
  const chosen = options.find((o) => o.key === decided)?.label;
  return (
    <div className="wf-chatcard">
      <p className="wf-eyebrow">{t("workflows.chat.previewOf", { name: recipient })}</p>
      <div className="wf-card">
        <div className="wf-card-kind">{kind}</div>
        {chosen ? (
          <div className="wf-card-done">{t("workflows.chat.decidedAs", { choice: chosen })}</div>
        ) : (
          <>
            <div className="wf-card-q">{question}</div>
            {context && <div className="wf-card-ctx">{context}</div>}
            <div className="wf-row">
              {options.map((o) => (
                <button key={o.key} className="btn sm" disabled={!onDecide} onClick={() => onDecide?.(o.key)}>
                  {o.label}
                </button>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export function QuestionCard({
  recipient,
  colleague,
  question,
  answered,
  thanks,
  onAnswer,
}: {
  recipient: string;
  colleague: string;
  question: string;
  answered: string | null;
  thanks: string;
  onAnswer: (text: string) => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState("");
  return (
    <div className="wf-chatcard">
      <p className="wf-eyebrow">{t("workflows.chat.previewOf", { name: recipient })}</p>
      <div className="wf-card">
        <div className="wf-card-kind">
          {colleague} · {t("workflows.chat.kindQuestion")}
        </div>
        <div className="wf-card-q">{question}</div>
        {answered ? (
          <>
            <div className="wf-card-ctx">
              {t("workflows.chat.you")}: {answered}
            </div>
            <div className="wf-card-done">
              {colleague}: {thanks}
            </div>
          </>
        ) : (
          <form
            className="wf-row"
            onSubmit={(e) => {
              e.preventDefault();
              const text = draft.trim();
              if (text) onAnswer(text);
            }}
          >
            <input className="input wf-card-input" value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={t("workflows.chat.placeholderColleague", { name: colleague })} />
            <button className="btn sm primary" type="submit">
              {t("workflows.chat.send")}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}

/* Many runs must not mean many messages. What does not need a decision
   reaches the person once a day, bundled; an approval still comes on its
   own, because each one is a decision. */
export function DigestCard({ recipient, stuck, queue, mine }: { recipient: string; stuck: number; queue?: { n: number; role: string }; mine: number }) {
  const { t } = useTranslation();
  return (
    <div className="wf-chatcard">
      <p className="wf-eyebrow">{t("workflows.chat.previewOf", { name: recipient })}</p>
      <div className="wf-card">
        <div className="wf-card-kind">{t("workflows.chat.digestKind")}</div>
        <ul className="wf-digest">
          {stuck > 0 && <li>{t("workflows.chat.digestStuck", { count: stuck })}</li>}
          {queue && <li>{t("workflows.chat.digestQueue", { count: queue.n, role: queue.role })}</li>}
          {mine > 0 && <li>{t("workflows.chat.digestMine", { count: mine })}</li>}
        </ul>
        <div className="wf-hint">{t("workflows.chat.digestNote")}</div>
      </div>
    </div>
  );
}
