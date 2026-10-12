/* How a workflow reaches a person (#560, #565). A run that needs someone
   puts a card into that person's chat (#440, #441). Until the chat carries
   workflows, the card stands where the run waits, and is answered there. */
import { useTranslation } from "react-i18next";

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
  options: { key: string; label: string }[];
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
