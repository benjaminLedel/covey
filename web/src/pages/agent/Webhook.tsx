import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  post,
  del,
  type AgentWebhook,
} from "../../api";

export function WebhookTrigger({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [copied, setCopied] = useState(false);
  const wh = useQuery({
    queryKey: ["webhook", agentId],
    queryFn: () => api<AgentWebhook>(`/agents/${agentId}/webhook`),
  });
  const refresh = () => qc.invalidateQueries({ queryKey: ["webhook", agentId] });
  const enable = useMutation({ mutationFn: () => post<AgentWebhook>(`/agents/${agentId}/webhook`), onSuccess: refresh });
  const disable = useMutation({ mutationFn: () => del(`/agents/${agentId}/webhook`), onSuccess: refresh });

  if (wh.isLoading) return null;
  const data = wh.data;
  const url = data?.url?.startsWith("/") ? window.location.origin + data.url : data?.url;

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("agent.webhook.desc")}
      </p>
      {!data?.enabled && (
        <div className="kc-empty">
          <p className="mb-3">{t("agent.webhook.noWebhook")}</p>
          <button className="btn primary sm" disabled={enable.isPending} onClick={() => enable.mutate()}>
            {t("agent.webhook.activate")}
          </button>
        </div>
      )}
      {data?.enabled && url && (
        <div>
          <label>{t("agent.webhook.triggerUrl")}</label>
          <div className="flex items-center gap-2 mb-3 flex-wrap">
            <span className="mono text-xs" style={{ wordBreak: "break-all" }}>
              {url}
            </span>
            <button
              className="btn sm"
              onClick={() => {
                navigator.clipboard.writeText(url).then(() => {
                  setCopied(true);
                  setTimeout(() => setCopied(false), 1500);
                });
              }}
            >
              {copied ? t("agent.webhook.copied") : t("agent.webhook.copy")}
            </button>
          </div>
          <label>{t("agent.webhook.example")}</label>
          <pre className="code text-xs mb-3" style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
            {`curl -X POST ${url} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"title": "Build fehlgeschlagen", "body": "Pipeline #123 ist rot.", "dedup_key": "pipeline-123"}'`}
          </pre>
          <div className="flex items-center gap-3">
            <button className="btn sm" disabled={enable.isPending} onClick={() => enable.mutate()} title="Neues Token, alte URL wird ungültig">
              {t("agent.webhook.rotate")}
            </button>
            <button className="btn sm danger" disabled={disable.isPending} onClick={() => disable.mutate()}>
              {t("agent.webhook.deactivate")}
            </button>
          </div>
        </div>
      )}
      {(enable.isError || disable.isError) && (
        <p className="danger-text text-xs mt-3">{((enable.error ?? disable.error) as Error).message}</p>
      )}
    </div>
  );
}
