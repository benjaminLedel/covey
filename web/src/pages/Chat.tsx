import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, post, type Agent, type ChatEntry, type Principal } from "../api";
import { Markdown } from "../components/Markdown";
import { canManage } from "./agent/roles";

/* The chat: the door into the backlog for somebody who only wants to hand
   over work (#298).

   Everything else in this shell is the view of somebody who RUNS a workforce —
   agents, costs, guard rails, secrets. This one page is the view of somebody
   who WORKS WITH one. It therefore shows exactly two things: who is there, and
   what was said. No state machine, no priority, no board.

   That it is nevertheless the same backlog is the whole point: a message is a
   task, a reply is the resume input of a parked task. The server assembles the
   thread out of the objects that already carry it (internal/httpapi/chat.go) —
   the browser does not stitch tasks, notes and transitions together, it reads
   a conversation.

   The one moment that makes the difference visible is the question: the agent
   stands still and waits. Then the compose box does not write a new task, it
   answers the one that is waiting — and says so above the field. */

/** Who is speaking. Everything an agent produced stands on the left. */
const fromHuman = (e: ChatEntry) => e.kind === "message" || e.author.startsWith("human:");

/** The person behind an origin or a note author ("chat:a@b" → "a@b"). */
const whom = (author: string) => author.split(":").slice(1).join(":") || author;

export default function Chat({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [sel, setSel] = useState<string>("");
  const [text, setText] = useState("");
  const ende = useRef<HTMLDivElement>(null);

  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[] | null>("/agents"),
    staleTime: 60_000,
  });

  /* Hired agents only. An application is a draft of a colleague, not a
     colleague — writing to it would create work for somebody who does not
     work yet. */
  const liste = (agents.data ?? []).filter((a) => a.status !== "applicant");
  const aktiv = sel || liste[0]?.id || "";

  const thread = useQuery({
    queryKey: ["thread", aktiv],
    queryFn: () => api<{ entries: ChatEntry[] }>(`/agents/${aktiv}/thread`),
    enabled: !!aktiv,
    /* Ten seconds: the agent answers while one is looking at the page, and
       there is no push for it. Cheap enough — the query reads three tables
       for at most twenty tasks. */
    refetchInterval: 10_000,
  });

  const entries = thread.data?.entries ?? [];

  /* The open question, if there is one: the last entry of kind "question"
     whose task still stands blocked. It decides what the compose box does. */
  const offeneFrage = [...entries]
    .reverse()
    .find((e) => e.kind === "question" && e.task_state === "blocked");

  const senden = useMutation({
    mutationFn: async (nachricht: string) =>
      offeneFrage
        ? post(`/tasks/${offeneFrage.task_id}/reply`, { text: nachricht })
        : post(`/agents/${aktiv}/messages`, { text: nachricht }),
    onSuccess: () => {
      setText("");
      qc.invalidateQueries({ queryKey: ["thread", aktiv] });
    },
  });

  /* Scroll to the end on every new entry — a conversation is read from below.
     The optional call is not politeness: jsdom has no scrollIntoView, and a
     component that throws in the test harness cannot be tested there. */
  useEffect(() => {
    ende.current?.scrollIntoView?.({ block: "end" });
  }, [entries.length, aktiv]);

  const darfSchreiben = canManage(me.Role);
  const abschicken = () => {
    const n = text.trim();
    if (n && !senden.isPending) senden.mutate(n);
  };

  return (
    <div>
      <h1 className="text-[22px] mb-1">{t("chat.title")}</h1>
      <p className="muted mb-4">{t("chat.lead")}</p>

      <div className="grid grid-cols-[240px_minmax(0,1fr)] gap-4 items-start max-lg:grid-cols-1">
        {/* Wer da ist. */}
        {/* Die Liste scrollt in sich. Eine Organisation mit dreißig Agenten
            zöge die Seite sonst so weit auf, dass das Eingabefeld unter dem
            Falz verschwindet — und das ist das Einzige, worum es hier geht. */}
        <nav className="card p-2 max-h-[calc(100vh-240px)] overflow-y-auto" aria-label={t("chat.agents")}>
          {liste.length === 0 && <p className="muted p-2">{t("chat.noAgents")}</p>}
          {liste.map((a) => (
            <button
              key={a.id}
              onClick={() => setSel(a.id)}
              aria-current={a.id === aktiv}
              className={`w-full text-left px-3 py-2 rounded ${a.id === aktiv ? "bg-[var(--tint)]" : ""}`}
            >
              <span className="block truncate">{a.display_name}</span>
              <span className="muted text-[12px] block truncate">{a.job_title || a.slug}</span>
            </button>
          ))}
        </nav>

        {/* Was gesagt wurde. */}
        <section className="card p-4 flex flex-col h-[calc(100vh-240px)] min-h-[420px]">
          <div className="flex-1 overflow-y-auto flex flex-col gap-3 pr-1">
            {thread.isLoading && <p className="muted">{t("common.loading")}</p>}
            {!thread.isLoading && entries.length === 0 && (
              <p className="muted">{t("chat.empty")}</p>
            )}

            {entries.map((e, i) => (
              <article
                key={`${e.task_id}-${e.kind}-${i}`}
                className={`max-w-[75%] ${fromHuman(e) ? "self-end text-right" : "self-start"}`}
              >
                {/* Über der eigenen Nachricht steht nur, wer sie geschrieben
                    hat: Ihr Titel IST ihre erste Zeile, und er stünde sonst
                    unmittelbar über sich selbst. Wo der Agent spricht, trägt
                    die Zeile die Aufgabe — dort will man hinspringen. */}
                <div className="muted text-[12px] mb-1">
                  {fromHuman(e) ? (
                    whom(e.author)
                  ) : (
                    <>
                      {t(`chat.kind.${e.kind}`)}
                      {" · "}
                      <Link to={`/agents/${aktiv}?task=${e.task_id}`} className="underline">
                        {e.task_title}
                      </Link>
                    </>
                  )}
                </div>
                <div
                  className={`px-3 py-2 rounded-lg text-left ${
                    e.kind === "question"
                      ? "border border-[var(--border-strong)] bg-[var(--accent-tint)]"
                      : e.kind === "error"
                        ? "border border-[var(--border-danger)]"
                        : fromHuman(e)
                          ? "bg-[var(--tint)]"
                          : "border border-[var(--border)]"
                  }`}
                >
                  <Markdown text={e.text} />
                </div>
              </article>
            ))}
            <div ref={ende} />
          </div>

          {/* Der Eingang in den Backlog: er steht fest unter dem Verlauf und
              scrollt nicht mit ihm weg. */}
          <div className="shrink-0 pt-3 border-t border-[var(--border)]">
            {offeneFrage && (
              <p className="muted text-[12px] mb-1">
                {t("chat.answering", { title: offeneFrage.task_title })}
              </p>
            )}
            <div className="flex gap-2 items-end">
              <textarea
                value={text}
                onChange={(ev) => setText(ev.target.value)}
                onKeyDown={(ev) => {
                  // Enter sends, Shift+Enter breaks the line — the habit from
                  // every messenger. A task title is one line anyway.
                  if (ev.key === "Enter" && !ev.shiftKey) {
                    ev.preventDefault();
                    abschicken();
                  }
                }}
                rows={2}
                disabled={!aktiv || !darfSchreiben}
                placeholder={
                  darfSchreiben
                    ? offeneFrage
                      ? t("chat.placeholderAnswer")
                      : t("chat.placeholder")
                    : t("chat.readOnly")
                }
                aria-label={t("chat.placeholder")}
                className="flex-1 resize-y"
              />
              <button
                className="btn primary"
                onClick={abschicken}
                disabled={!aktiv || !darfSchreiben || !text.trim() || senden.isPending}
              >
                {offeneFrage ? t("chat.answer") : t("chat.send")}
              </button>
            </div>
            {senden.isError && <p className="danger-text mt-1">{String(senden.error)}</p>}
          </div>
        </section>
      </div>
    </div>
  );
}
