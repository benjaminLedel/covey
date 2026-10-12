/* How a workflow would reach a person in the preview (#560): through the
   personal chat (#440, #441). Two conversations — the workflow itself, which
   writes only when someone has to decide or know something, and a colleague
   whose step has a question. Nothing is sent anywhere. */
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { BlockIcon } from "./canvas";
import type { Example } from "./model";

export type Decision = "retry" | "self" | null;

interface Props {
  ex: Example;
  focus: "workflow" | "colleague";
  onFocus: (f: "workflow" | "colleague") => void;
  decided: Decision;
  decisionNote: string;
  onDecide: (d: Exclude<Decision, null>, note: string) => void;
  replied: string | null;
  onReply: (text: string) => void;
  onRun: (no: string) => void;
}

export default function ChatPreview(p: Props) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState("");
  const colleague = p.ex.chat.colleague;
  const retryLabel = p.ex.blocks.h?.outs?.[0]?.label ?? "";
  const selfLabel = p.ex.blocks.h?.outs?.[1]?.label ?? "";

  const send = () => {
    const text = draft.trim();
    if (!text) return;
    setDraft("");
    if (p.focus === "colleague") {
      if (!p.replied) p.onReply(text);
      return;
    }
    if (p.decided) return;
    // Free text answers the card too: the run picks the branch it names.
    if (/runde|round|again|nochmal/i.test(text)) p.onDecide("retry", text);
    else if (/übernehm|selbst|take|myself/i.test(text)) p.onDecide("self", text);
  };

  const runChip = (no: string, step: string) => (
    <button className="wf-runchip" onClick={() => p.onRun(no)}>
      <BlockIcon type="trigger" small />
      {p.ex.name} · {no} · {step}
    </button>
  );

  return (
    <div className="wf-chat">
      <nav className="wf-chat-list" aria-label={t("workflows.chat.conversations")}>
        <button className={`wf-chat-item${p.focus === "workflow" ? " on" : ""}`} onClick={() => p.onFocus("workflow")}>
          <span className="wf-avatar wf">
            <BlockIcon type="condition" small />
          </span>
          <span className="wf-chat-item-text">
            <b>{p.ex.name}</b>
            <small>{p.decided ? t("workflows.chat.decided") : t("workflows.chat.waits", { no: "#384" })}</small>
          </span>
          {!p.decided && <span className="wf-unread">1</span>}
        </button>
        <button className={`wf-chat-item${p.focus === "colleague" ? " on" : ""}`} onClick={() => p.onFocus("colleague")}>
          <span className="wf-avatar">{colleague.slice(0, 2).toUpperCase()}</span>
          <span className="wf-chat-item-text">
            <b>{colleague}</b>
            <small>{p.replied ? p.ex.chat.thanks : t("workflows.chat.questionOn", { no: p.ex.chat.caseNo })}</small>
          </span>
          {!p.replied && <span className="wf-unread">1</span>}
        </button>
        <p className="wf-hint">{t("workflows.chat.note")}</p>
      </nav>

      <section className="wf-thread">
        {p.focus === "workflow" ? (
          <>
            <header className="wf-thread-head">
              <span className="wf-avatar wf">
                <BlockIcon type="condition" small />
              </span>
              <div>
                <h2>{p.ex.name}</h2>
                <p>{t("workflows.chat.workflowSub")}</p>
              </div>
            </header>
            <div className="wf-messages">
              <Bubble who={p.ex.name} kind={t("workflows.chat.kindNotice")} wf>
                {t("workflows.chat.noticeAccepted", { no: "#402", owner: p.ex.owner })}
                <div>{runChip("#402", p.ex.blocks.w.title)}</div>
              </Bubble>
              <Bubble who={p.ex.name} kind={t("workflows.chat.kindStuck")} wf>
                {t("workflows.chat.noticeStuck", { no: "#409", name: "Kim", step: p.ex.blocks.b.title })}
              </Bubble>
              <div className="wf-bubble">
                <span className="wf-avatar wf">
                  <BlockIcon type="condition" small />
                </span>
                <div className="wf-card">
                  <div className="wf-card-kind">
                    {t("workflows.chat.kindApproval")} · #384 {p.ex.live.h?.cases[0]?.title ?? p.ex.runs["#384"].title}
                  </div>
                  {p.decided ? (
                    <div className="wf-card-done">
                      {t("workflows.chat.decidedAs", { choice: p.decided === "retry" ? retryLabel : selfLabel })}
                    </div>
                  ) : (
                    <>
                      <div className="wf-card-q">{t("workflows.chat.cardQuestion")}</div>
                      <div className="wf-card-ctx">{t("workflows.chat.cardContext", { defects: p.ex.runs["#384"].steps.find((s) => s.out?.defects)?.out?.defects ?? "" })}</div>
                      <div className="wf-row">
                        <button className="btn sm" onClick={() => p.onDecide("retry", "")}>
                          {retryLabel}
                        </button>
                        <button className="btn sm" onClick={() => p.onDecide("self", "")}>
                          {selfLabel}
                        </button>
                        <button className="btn sm" onClick={() => p.onRun("#384")}>
                          {t("workflows.chat.viewRun")}
                        </button>
                      </div>
                    </>
                  )}
                </div>
              </div>
              {p.decided && p.decisionNote && <Bubble me>{p.decisionNote}</Bubble>}
              {p.decided === "retry" && (
                <Bubble who={p.ex.name} kind={t("workflows.chat.kindNotice")} wf>
                  {t("workflows.chat.retried", { name: "Kim" })}
                </Bubble>
              )}
            </div>
          </>
        ) : (
          <>
            <header className="wf-thread-head">
              <span className="wf-avatar">{colleague.slice(0, 2).toUpperCase()}</span>
              <div>
                <h2>{colleague}</h2>
                <p>{t("workflows.chat.colleagueSub", { workflow: p.ex.name })}</p>
              </div>
            </header>
            <div className="wf-messages">
              <Bubble me>{p.ex.chat.request}</Bubble>
              <Bubble who={colleague}>
                {p.ex.chat.answer}
                <div>{runChip(p.ex.chat.caseNo, p.ex.blocks.b.title)}</div>
              </Bubble>
              <Bubble who={colleague} kind={t("workflows.chat.kindQuestion")} question>
                {p.ex.chat.question}
                {!p.replied && <div className="wf-hint">{t("workflows.chat.runWaits")}</div>}
              </Bubble>
              {p.replied && (
                <>
                  <Bubble me>{p.replied}</Bubble>
                  <Bubble who={colleague}>{p.ex.chat.thanks}</Bubble>
                </>
              )}
            </div>
          </>
        )}
        <form
          className="wf-composer"
          onSubmit={(e) => {
            e.preventDefault();
            send();
          }}
        >
          <input
            className="input"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={p.focus === "workflow" ? t("workflows.chat.placeholderWorkflow") : t("workflows.chat.placeholderColleague", { name: colleague })}
          />
          <button className="btn primary sm" type="submit">
            {t("workflows.chat.send")}
          </button>
        </form>
      </section>
    </div>
  );
}

function Bubble({ who, kind, me, wf, question, children }: { who?: string; kind?: string; me?: boolean; wf?: boolean; question?: boolean; children: React.ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className={`wf-bubble${me ? " me" : ""}${question ? " q" : ""}`}>
      {!me && (
        <span className={`wf-avatar${wf ? " wf" : ""}`}>{wf ? <BlockIcon type="condition" small /> : (who ?? "").slice(0, 2).toUpperCase()}</span>
      )}
      <div className="wf-bubble-in">
        <div className="wf-bubble-who">
          {me ? t("workflows.chat.you") : who}
          {kind ? ` · ${kind}` : ""}
        </div>
        <div className="wf-bubble-text">{children}</div>
      </div>
    </div>
  );
}
