import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import {
  addConversationMember,
  api,
  conversationMessages,
  markConversationRead,
  postConversationMessage,
  removeConversationMember,
  setConversationMuted,
  type Conversation,
  type ConversationMember,
  type ConversationMessage,
  type OrgChart,
  type Principal,
} from "../api";
import { Markdown } from "../components/Markdown";
import { Avatar } from "../components/person";
import { NavIcon } from "../components/navicons";

/* A conversation with members (#440): a group, or a direct conversation
 * between two people. The direct conversation with an agent keeps its own
 * view (Thread.tsx), because it carries the agent's work around it — what
 * is running, the reply at a parked question, the reactions. Here it is
 * people talking, and agents where somebody addresses them.
 *
 * What the reader sees is the conversation and nothing else: the messages of
 * its members, and what a task opened here reported back. */

const uhr = (iso: string, lang: string) =>
  new Date(iso).toLocaleTimeString(lang, { hour: "2-digit", minute: "2-digit" });
const tag = (iso: string) => new Date(iso).toDateString();

/** The name a conversation goes by: its title, or the other member. */
export function gespraechName(c: Pick<Conversation, "kind" | "title" | "members">, meId: string) {
  if (c.kind === "group") return c.title || "…";
  const anderer = c.members.find((m) => !(m.kind === "human" && m.id === meId));
  return anderer?.name || "…";
}

export default function Gespraech({ id, me }: { id: string; me: Principal }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const [antwortAuf, setAntwortAuf] = useState<ConversationMessage | null>(null);
  const [mitgliederOffen, setMitgliederOffen] = useState(false);
  const ende = useRef<HTMLDivElement>(null);

  const conv = useQuery({
    queryKey: ["conversation", id, "kopf"],
    queryFn: () => api<Conversation>(`/conversations/${id}`),
  });
  /* Pages backwards: the first page is the newest, "older" adds the one
     before it. The event stream (App.tsx) invalidates, the half minute is
     the net under it. */
  const seiten = useInfiniteQuery({
    queryKey: ["conversation", id, "nachrichten"],
    queryFn: ({ pageParam }) => conversationMessages(id, pageParam),
    initialPageParam: undefined as ConversationMessage | undefined,
    getNextPageParam: (letzte) => (letzte.more ? letzte.messages[0] : undefined),
    refetchInterval: 30_000,
  });
  const nachrichten = useMemo(
    () => [...(seiten.data?.pages ?? [])].reverse().flatMap((p) => p.messages),
    [seiten.data],
  );
  const pending = seiten.data?.pages[0]?.pending ?? false;

  const neueste = nachrichten.length > 0 ? nachrichten[nachrichten.length - 1].created_at : "";
  useEffect(() => {
    if (!neueste) return;
    markConversationRead(id, neueste)
      .then(() => qc.invalidateQueries({ queryKey: ["conversations"] }))
      .catch(() => {});
  }, [id, neueste, qc]);
  useEffect(() => {
    ende.current?.scrollIntoView?.({ block: "end" });
  }, [nachrichten.length]);

  const c = conv.data;
  const aktive = (c?.members ?? []).filter((m) => !m.left_at);
  const ich = aktive.find((m) => m.kind === "human" && m.id === me.ID);
  const gruppe = c?.kind === "group";
  const wer = (m: ConversationMessage) => aktive.find((x) => x.kind === m.author_kind && x.id === m.author_id);
  const vonMir = (m: ConversationMessage) => m.author_kind === "human" && m.author_id === me.ID;

  const senden = useMutation({
    mutationFn: () => postConversationMessage(id, text.trim(), antwortAuf?.id),
    onSuccess: () => {
      setText("");
      setAntwortAuf(null);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["conversation", id] });
      qc.invalidateQueries({ queryKey: ["conversations"] });
    },
  });
  const stumm = useMutation({
    mutationFn: (muted: boolean) => setConversationMuted(id, muted),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["conversation", id] });
      qc.invalidateQueries({ queryKey: ["conversations"] });
    },
  });
  const gehen = useMutation({
    mutationFn: () => removeConversationMember(id, { kind: "human", id: me.ID }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["conversations"] });
      navigate("/team");
    },
  });

  const abschicken = () => {
    if (text.trim() && !senden.isPending) senden.mutate();
  };

  return (
    <div className="tm-thread">
      <header className="tm-thread-kopf">
        <div className="tm-thread-wer">
          {c && !gruppe && (() => {
            const anderer = aktive.find((m) => m !== ich);
            return anderer ? <Avatar name={anderer.name} human={anderer.kind === "human"} slug={anderer.slug} size={36} /> : null;
          })()}
          {gruppe && (
            <span className="tm-gruppe-zeichen" aria-hidden="true">
              <NavIcon name="chat" />
            </span>
          )}
          <div>
            <h1>{c ? gespraechName(c, me.ID) : "…"}</h1>
            <p>{gruppe ? t("conversation.membersCount", { count: aktive.length }) : aktive.find((m) => m !== ich)?.email ?? ""}</p>
          </div>
        </div>
        <div className="tm-thread-werkzeuge">
          <button
            className={`tm-werkzeug${ich?.muted ? " auf" : ""}`}
            onClick={() => stumm.mutate(!ich?.muted)}
            title={ich?.muted ? t("conversation.unmute") : t("conversation.mute")}
            aria-label={ich?.muted ? t("conversation.unmute") : t("conversation.mute")}
            aria-pressed={!!ich?.muted}
          >
            <NavIcon name="bell" />
          </button>
          {gruppe && (
            <button
              className={`tm-werkzeug${mitgliederOffen ? " auf" : ""}`}
              onClick={() => setMitgliederOffen((v) => !v)}
              title={t("conversation.members")}
              aria-label={t("conversation.members")}
              aria-expanded={mitgliederOffen}
            >
              <NavIcon name="user" />
            </button>
          )}
        </div>
      </header>

      {gruppe && mitgliederOffen && c && (
        <Mitglieder conv={c} me={me} onLeave={() => gehen.mutate()} />
      )}

      <div className="tm-verlauf">
        {seiten.hasNextPage && (
          <button className="btn sm tm-aelter" onClick={() => seiten.fetchNextPage()} disabled={seiten.isFetchingNextPage}>
            {t("conversation.older")}
          </button>
        )}
        {seiten.isLoading && <p className="tm-leise">{t("common.loading")}</p>}
        {!seiten.isLoading && nachrichten.length === 0 && (
          <div className="tm-leer">
            <p>{t("conversation.emptyThread")}</p>
            {gruppe && aktive.some((m) => m.kind === "agent") && <p className="tm-leise">{t("conversation.agentHint")}</p>}
          </div>
        )}
        {nachrichten.map((m, i) => {
          const vorige = i > 0 ? nachrichten[i - 1] : null;
          const folgt =
            !!vorige &&
            vorige.author_kind === m.author_kind &&
            vorige.author_id === m.author_id &&
            tag(vorige.created_at) === tag(m.created_at) &&
            Date.parse(m.created_at) - Date.parse(vorige.created_at) < 5 * 60_000 &&
            m.kind === "text";
          const neuerTag = i === 0 || tag(nachrichten[i - 1].created_at) !== tag(m.created_at);
          const autor = wer(m);
          const bezug = m.reply_to ? nachrichten.find((x) => x.id === m.reply_to) : undefined;
          return (
            <Fragment key={m.id}>
              {neuerTag && <div className="tm-tag">{new Date(m.created_at).toLocaleDateString(i18n.language)}</div>}
              <article className={`tm-blase ${vonMir(m) ? "ich" : "er"} k-${m.kind === "text" ? "message" : m.kind}${folgt ? " folgt" : ""}`}>
                {!vonMir(m) && (
                  <span className="tm-blase-wer" aria-hidden="true">
                    {!folgt && <Avatar name={m.author_name || "?"} human={m.author_kind === "human"} slug={autor?.slug} size={26} />}
                  </span>
                )}
                <div className="tm-blase-inhalt">
                  {!folgt && (
                    <div className="tm-blase-kopf">
                      {!vonMir(m) && <span className="tm-blase-name">{m.author_name}</span>}
                      {(m.kind === "question" || m.kind === "error") && (
                        <span className={`tm-blase-art a-${m.kind}`}>{t(`chat.kind.${m.kind}`)}</span>
                      )}
                      <time dateTime={m.created_at}>{uhr(m.created_at, i18n.language)}</time>
                    </div>
                  )}
                  {bezug && (
                    <p className="tm-bezug">
                      {t("conversation.replyingTo", { name: bezug.author_name || "" })}: {bezug.text.slice(0, 80)}
                    </p>
                  )}
                  <div className="tm-blase-text">
                    <Markdown text={m.text} />
                  </div>
                  {m.report && m.report !== m.text && (
                    <details className="tm-bericht">
                      <summary>{t("chat.bericht")}</summary>
                      <div className="tm-blase-text">
                        <Markdown text={m.report} />
                      </div>
                    </details>
                  )}
                  {gruppe && m.author_kind === "agent" && (
                    <button className="tm-antworten" onClick={() => setAntwortAuf(m)}>
                      {t("conversation.reply")}
                    </button>
                  )}
                </div>
              </article>
            </Fragment>
          );
        })}
        {pending && (
          <article className="tm-blase er tm-tippt">
            <span className="tm-blase-wer" aria-hidden="true" />
            <div className="tm-blase-inhalt">
              <div className="tm-tippt-blase" aria-label={t("team.arbeitetGerade")}>
                <span /> <span /> <span />
              </div>
            </div>
          </article>
        )}
        <div ref={ende} />
      </div>

      <div className="tm-eingabe">
        {antwortAuf && (
          <div className="tm-anhaenge">
            <span className="tm-anhang">
              <span className="tm-anhang-name">{t("conversation.replyingTo", { name: antwortAuf.author_name || "" })}</span>
              <button onClick={() => setAntwortAuf(null)} aria-label={t("team.abbrechen")} title={t("team.abbrechen")}>
                ×
              </button>
            </span>
          </div>
        )}
        <div className="tm-eingabe-reihe">
          <textarea
            rows={1}
            value={text}
            onChange={(ev) => setText(ev.target.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" && !ev.shiftKey) {
                ev.preventDefault();
                abschicken();
              }
            }}
            placeholder={gruppe && aktive.some((x) => x.kind === "agent") ? t("conversation.placeholderGroup") : t("conversation.placeholder")}
            aria-label={t("conversation.placeholder")}
          />
          <button
            className="tm-eingabe-senden"
            onClick={abschicken}
            disabled={!text.trim() || senden.isPending}
            title={t("chat.send")}
            aria-label={t("chat.send")}
          >
            <NavIcon name="arrowUp" />
          </button>
        </div>
      </div>
      {senden.isError && <p className="tm-fehler">{String((senden.error as Error)?.message ?? senden.error)}</p>}
    </div>
  );
}

/* The members of a group: who is in it, and the one way to take somebody in
   — from the org chart, people and colleagues alike. */
function Mitglieder({ conv, me, onLeave }: { conv: Conversation; me: Principal; onLeave: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [wahl, setWahl] = useState("");
  const chart = useQuery({ queryKey: ["org-chart"], queryFn: () => api<OrgChart>("/org/chart") });
  const aktive = conv.members.filter((m) => !m.left_at);
  const drin = (kind: string, id: string) => aktive.some((m) => m.kind === kind && m.id === id);
  const binOwner = aktive.some((m) => m.kind === "human" && m.id === me.ID && m.role === "owner");
  const neu = useMutation({
    mutationFn: (v: string) => {
      const [kind, id] = v.split(":");
      return addConversationMember(conv.id, { kind: kind as "human" | "agent", id });
    },
    onSuccess: () => {
      setWahl("");
      qc.invalidateQueries({ queryKey: ["conversation", conv.id] });
    },
  });
  const raus = useMutation({
    mutationFn: (m: ConversationMember) => removeConversationMember(conv.id, { kind: m.kind, id: m.id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["conversation", conv.id] }),
  });
  const kandidaten = [
    ...(chart.data?.humans ?? []).filter((h) => !drin("human", h.id)).map((h) => ({ v: `human:${h.id}`, name: h.display_name })),
    ...(chart.data?.agents ?? []).filter((a) => a.hired_at && !drin("agent", a.id)).map((a) => ({ v: `agent:${a.id}`, name: a.display_name })),
  ];
  return (
    <section className="tm-vorgaenge-flaeche tm-mitglieder" aria-label={t("conversation.members")}>
      <ul>
        {aktive.map((m) => (
          <li key={`${m.kind}:${m.id}`} className="tm-mitglied">
            <Avatar name={m.name} human={m.kind === "human"} slug={m.slug} size={22} />
            <span className="tm-mitglied-name">{m.name}</span>
            {m.role === "owner" && <span className="tm-leise">{t("conversation.owner")}</span>}
            {binOwner && !(m.kind === "human" && m.id === me.ID) && (
              <button className="btn sm" onClick={() => raus.mutate(m)}>
                {t("conversation.remove")}
              </button>
            )}
          </li>
        ))}
      </ul>
      <div className="flex gap-2 items-center" style={{ marginTop: 8 }}>
        <select value={wahl} onChange={(e) => setWahl(e.target.value)} aria-label={t("conversation.addMember")}>
          <option value="">{t("conversation.addMember")}</option>
          {kandidaten.map((k) => (
            <option key={k.v} value={k.v}>
              {k.name}
            </option>
          ))}
        </select>
        <button className="btn sm" disabled={!wahl || neu.isPending} onClick={() => neu.mutate(wahl)}>
          {t("conversation.add")}
        </button>
        <button className="btn sm" style={{ marginLeft: "auto" }} onClick={onLeave}>
          {t("conversation.leave")}
        </button>
      </div>
      {(neu.isError || raus.isError) && <p className="tm-fehler">{String(neu.error ?? raus.error)}</p>}
    </section>
  );
}
