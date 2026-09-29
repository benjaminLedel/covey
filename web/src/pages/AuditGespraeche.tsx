import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type AuditConversation, type ConversationMessage, type LegacyThread } from "../api";
import { Modal } from "../components/Modal";

/* Supervision → Audit → Conversations (#440): every conversation of the
 * organisation, and the shared per-agent threads from before conversations
 * had members. Only org admin and auditor read them; nobody else reads a
 * conversation they are not in. Opening one here is itself a request the
 * audit trail records. */

type Offen = { kind: "conversation"; id: string; name: string } | { kind: "legacy"; id: string; name: string };

const pfad = (o: Offen) => (o.kind === "conversation" ? `/audit/conversations/${o.id}` : `/audit/legacy-threads/${o.id}`);

export default function AuditGespraeche({ daten }: { daten: { conversations: AuditConversation[]; legacy: LegacyThread[] } }) {
  const { t } = useTranslation();
  const [offen, setOffen] = useState<Offen | null>(null);
  const name = (c: AuditConversation) =>
    c.kind === "group" ? c.title || "…" : c.members.map((m) => m.name).join(" · ");

  return (
    <div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 720 }}>
        {t("audit.convLead")}
      </p>
      <div className="card" style={{ padding: 0 }}>
        {daten.conversations.length === 0 && daten.legacy.length === 0 && (
          <p className="muted text-sm" style={{ padding: "18px 14px" }}>
            {t("audit.convEmpty")}
          </p>
        )}
        {(daten.conversations.length > 0 || daten.legacy.length > 0) && (
          <table className="tbl">
            <thead>
              <tr>
                <th>{t("audit.colMembers")}</th>
                <th style={{ width: 90 }}>{t("audit.colMessages")}</th>
                <th style={{ width: 170 }}>{t("audit.colLast")}</th>
                <th style={{ width: 170 }} />
              </tr>
            </thead>
            <tbody>
              {daten.conversations.map((c) => {
                const o: Offen = { kind: "conversation", id: c.id, name: name(c) };
                return (
                  <tr key={c.id}>
                    <td className="text-xs">
                      <button className="linkish" onClick={() => setOffen(o)}>
                        {o.name}
                      </button>
                      <span className="muted"> · {c.kind === "group" ? t("audit.group") : t("audit.direct")}</span>
                    </td>
                    <td className="mono text-xs">{c.messages}</td>
                    <td className="muted text-xs">{new Date(c.last_message_at).toLocaleString()}</td>
                    <td className="text-xs">
                      <Export o={o} />
                    </td>
                  </tr>
                );
              })}
              {daten.legacy.map((l) => {
                const o: Offen = { kind: "legacy", id: l.agent_id, name: l.agent_name };
                return (
                  <tr key={`legacy-${l.agent_id}`}>
                    <td className="text-xs">
                      <button className="linkish" onClick={() => setOffen(o)}>
                        {l.agent_name || l.agent_id}
                      </button>
                      <span className="badge" style={{ marginLeft: 8 }}>
                        {t("audit.legacy")}
                      </span>
                    </td>
                    <td className="mono text-xs">{l.messages}</td>
                    <td className="muted text-xs">{new Date(l.last_at).toLocaleString()}</td>
                    <td className="text-xs">
                      <Export o={o} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
      {offen && <Lesen offen={offen} onClose={() => setOffen(null)} />}
    </div>
  );
}

function Export({ o }: { o: Offen }) {
  const { t } = useTranslation();
  return (
    <span className="flex gap-2">
      <a href={`/api/v1${pfad(o)}?format=csv`} download>
        {t("audit.exportCsv")}
      </a>
      <a href={`/api/v1${pfad(o)}?format=json`} download>
        {t("audit.exportJson")}
      </a>
    </span>
  );
}

type AltZeile = { id: string; author: string; text: string; created_at: string };

function Lesen({ offen, onClose }: { offen: Offen; onClose: () => void }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["audit-conversation", offen.kind, offen.id],
    queryFn: () => api<{ messages: (ConversationMessage | AltZeile)[] }>(pfad(offen)),
  });
  const wer = (m: ConversationMessage | AltZeile) =>
    "author_kind" in m ? m.author_name || m.author_kind : m.author.replace(/^chat:/, "");
  return (
    <Modal title={offen.name || "…"} onClose={onClose} size="lg">
      {q.isLoading && <p className="muted text-xs">{t("common.loading")}</p>}
      {q.isError && <p className="danger-text text-xs">{(q.error as Error).message}</p>}
      <table className="tbl">
        <tbody>
          {(q.data?.messages ?? []).map((m) => (
            <tr key={m.id}>
              <td className="muted text-xs" style={{ width: 150, verticalAlign: "top" }}>
                {new Date(m.created_at).toLocaleString()}
              </td>
              <td className="text-xs" style={{ width: 160, verticalAlign: "top" }}>
                {wer(m)}
              </td>
              <td className="text-xs" style={{ whiteSpace: "pre-wrap" }}>
                {m.text}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Modal>
  );
}
