import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post } from "../api";

type ApiKey = {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
  expires_at: string | null;
};

type CreatedKey = ApiKey & { token: string };

// The key list of the signed-in account. The token appears exactly once — in
// the answer that created it — so the card holds it until it is dismissed
// rather than re-fetching it, because there is nothing to re-fetch.
export default function ApiKeys() {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const [name, setName] = useState("");
  const [days, setDays] = useState("");
  const [fresh, setFresh] = useState<CreatedKey | null>(null);
  const [copied, setCopied] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [error, setError] = useState("");

  const keys = useQuery({ queryKey: ["api-keys"], queryFn: () => api<ApiKey[]>("/auth/api-keys") });

  const create = useMutation({
    mutationFn: () =>
      post<CreatedKey>("/auth/api-keys", {
        name: name.trim(),
        expires_in_days: days.trim() === "" ? 0 : Number(days),
      }),
    onSuccess: (key) => {
      setFresh(key);
      setCopied(false);
      setName("");
      setDays("");
      setError("");
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (e) => setError(String(e)),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => del<{ revoked: boolean }>(`/auth/api-keys/${id}`),
    onSuccess: () => {
      setConfirming(null);
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (e) => setError(String(e)),
  });

  const fmt = (iso: string) => new Date(iso).toLocaleString(locale);
  const expired = (k: ApiKey) => k.expires_at !== null && new Date(k.expires_at) < new Date();

  return (
    <div className="card mt-4">
      <span className="text-sm font-medium">{t("account.apiKeys.title")}</span>
      <p className="muted text-xs mt-1 mb-3">{t("account.apiKeys.intro")}</p>

      {error && <p className="danger-text text-xs mb-2">{error}</p>}

      {fresh && (
        <div className="mb-3" style={{ border: "0.5px solid var(--border)", borderRadius: 6, padding: "8px 10px" }}>
          <p className="text-xs mt-0 mb-2">{t("account.apiKeys.created")}</p>
          <pre
            className="mono text-[12px]"
            style={{ background: "var(--surface-2, rgba(0,0,0,.05))", padding: "6px 10px", borderRadius: 6, overflowX: "auto", margin: "0 0 8px" }}
          >
{fresh.token}
          </pre>
          <div className="flex gap-2">
            <button
              className="btn sm"
              type="button"
              onClick={() => {
                navigator.clipboard?.writeText(fresh.token);
                setCopied(true);
              }}
            >
              {copied ? t("account.apiKeys.copied") : t("account.apiKeys.copy")}
            </button>
            <button className="btn sm primary" type="button" onClick={() => setFresh(null)}>
              {t("account.apiKeys.done")}
            </button>
          </div>
        </div>
      )}

      <form
        className="flex gap-3 items-end flex-wrap mb-3"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <div className="flex-1 min-w-44">
          <label>{t("account.apiKeys.name")}</label>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("account.apiKeys.namePlaceholder")} maxLength={80} required />
        </div>
        <div style={{ width: 150 }}>
          <label>{t("account.apiKeys.expiry")}</label>
          <input type="number" min={0} max={3650} value={days} onChange={(e) => setDays(e.target.value)} placeholder={t("account.apiKeys.expiryNever")} />
        </div>
        <button className="btn primary" disabled={create.isPending || name.trim() === ""}>
          {create.isPending ? "…" : t("account.apiKeys.create")}
        </button>
      </form>

      {keys.data?.length === 0 && <p className="muted text-xs m-0">{t("account.apiKeys.none")}</p>}
      {(keys.data ?? []).map((k, i) => (
        <div key={k.id} className="flex items-center gap-3 text-xs py-2" style={{ borderTop: i > 0 ? "0.5px solid var(--border)" : "none" }}>
          <span className="font-medium">{k.name}</span>
          <span className="mono muted">{k.prefix}…</span>
          <span className="muted">{t("account.apiKeys.createdAt", { date: fmt(k.created_at) })}</span>
          <span className="muted">
            {k.last_used_at ? t("account.apiKeys.lastUsed", { date: fmt(k.last_used_at) }) : t("account.apiKeys.neverUsed")}
          </span>
          {k.expires_at && (
            <span className={expired(k) ? "danger-text" : "muted"}>
              {expired(k) ? t("account.apiKeys.expired") : t("account.apiKeys.expiresOn", { date: fmt(k.expires_at) })}
            </span>
          )}
          <span className="spacer flex-1" />
          {confirming === k.id ? (
            <>
              <span className="muted">{t("account.apiKeys.revokeConfirm")}</span>
              <button className="btn sm danger" type="button" onClick={() => revoke.mutate(k.id)} disabled={revoke.isPending}>
                {t("account.apiKeys.revoke")}
              </button>
            </>
          ) : (
            <button className="btn sm" type="button" onClick={() => setConfirming(k.id)}>
              {t("account.apiKeys.revoke")}
            </button>
          )}
        </div>
      ))}

      <p className="muted text-xs mt-3 mb-0">{t("account.apiKeys.limits")}</p>
    </div>
  );
}
