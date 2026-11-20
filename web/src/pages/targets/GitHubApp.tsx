import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post } from "../../api";

/* GitHub through a GitHub App: instead of a token someone stores, covey mints
 * installation tokens itself. Two ways in:
 *
 *  - "Connect with GitHub": the platform's shared App (only where the
 *    instance offers one). One click, then pick the repositories on GitHub.
 *  - "Create own GitHub App": GitHub's manifest flow creates an App of the
 *    organisation's own, with its own bot name. covey receives its key
 *    directly from GitHub; nobody copies one.
 *
 * Both leave covey for github.com and come back to /targets?github=…, where
 * the outcome is shown. */

export type GitHubAppStatus = {
  shared_available: boolean;
  shared_slug?: string;
  can_create: boolean;
  installed: boolean;
  bot_login?: string;
  error?: string;
  connection?: {
    source: "shared" | "own";
    app_slug: string;
    app_html_url?: string;
    installation_id?: number;
    account_login?: string;
  };
};

/** Posts GitHub's manifest form — the flow is a browser form POST by design. */
function submitManifest(action: string, manifest: string) {
  const form = document.createElement("form");
  form.method = "POST";
  form.action = action;
  const input = document.createElement("input");
  input.type = "hidden";
  input.name = "manifest";
  input.value = manifest;
  form.appendChild(input);
  document.body.appendChild(form);
  form.submit();
}

export function GitHubAppOutcome() {
  const { t } = useTranslation();
  const q = new URLSearchParams(window.location.search);
  const outcome = q.get("github");
  if (!outcome) return null;
  if (outcome === "connected") return <p className="text-sm mb-3" role="status">{t("targets.githubApp.outcomeConnected")}</p>;
  if (outcome === "requested") return <p className="text-sm mb-3" role="status">{t("targets.githubApp.outcomeRequested")}</p>;
  return (
    <p className="err text-sm mb-3" role="status">
      {t("targets.githubApp.outcomeError")} {q.get("detail")}
    </p>
  );
}

export function GitHubAppPanel({ onChanged }: { onChanged: () => void }) {
  const { t } = useTranslation();
  const status = useQuery({ queryKey: ["github-app"], queryFn: () => api<GitHubAppStatus>("/integrations/github") });
  const [own, setOwn] = useState(false);
  const [org, setOrg] = useState("");
  const [name, setName] = useState("");

  const connect = useMutation({
    mutationFn: (source: "shared" | "own") => post<{ url: string }>("/integrations/github/connect", { source }),
    onSuccess: (r) => {
      window.location.href = r.url;
    },
  });
  const create = useMutation({
    mutationFn: () =>
      post<{ action: string; manifest: string }>("/integrations/github/manifest", { github_org: org.trim(), name: name.trim() }),
    onSuccess: (r) => submitManifest(r.action, r.manifest),
  });
  const disconnect = useMutation({
    mutationFn: () => del("/integrations/github"),
    onSuccess: () => {
      status.refetch();
      onChanged();
    },
  });

  const s = status.data;
  if (!s) return null;
  const c = s.connection;
  const err = (connect.error || create.error || disconnect.error) as Error | null;

  return (
    <div className="tgt-cred" style={{ borderBottom: "1px solid var(--border)", paddingBottom: 12, marginBottom: 12 }}>
      <p className="text-sm" style={{ fontWeight: 600 }}>{t("targets.githubApp.title")}</p>
      <p className="text-xs secondary mb-2">{t("targets.githubApp.desc")}</p>

      {c && s.installed && (
        <div className="flex items-center gap-2 mb-2">
          <span className="ok-text text-sm">
            {t("targets.githubApp.connected", { bot: s.bot_login, account: c.account_login ?? "" })}
          </span>
          <button className="btn sm" disabled={disconnect.isPending} onClick={() => disconnect.mutate()}>
            {t("targets.githubApp.disconnect")}
          </button>
        </div>
      )}
      {c && !s.installed && c.source === "own" && (
        <div className="flex items-center gap-2 mb-2">
          <span className="text-sm">{t("targets.githubApp.notInstalled", { app: c.app_slug })}</span>
          <button className="btn sm primary" disabled={connect.isPending} onClick={() => connect.mutate("own")}>
            {t("targets.githubApp.install")}
          </button>
        </div>
      )}
      {s.installed && <p className="text-xs muted mb-2">{t("targets.githubApp.routingHint")}</p>}

      {!s.installed && !s.can_create && <p className="text-xs danger-text">{t("targets.githubApp.noSite")}</p>}
      {!s.installed && s.can_create && (
        <div className="flex gap-2 flex-wrap mb-2">
          {s.shared_available && (
            <button className="btn sm primary" disabled={connect.isPending} onClick={() => connect.mutate("shared")}>
              {t("targets.githubApp.connectShared")}
            </button>
          )}
          <button className="btn sm" onClick={() => setOwn((v) => !v)} aria-expanded={own}>
            {t("targets.githubApp.createOwn")}
          </button>
        </div>
      )}
      {!s.installed && s.can_create && s.shared_available && (
        <p className="text-xs muted">{t("targets.githubApp.connectSharedHint", { bot: `${s.shared_slug}[bot]` })}</p>
      )}
      {own && !s.installed && (
        <div className="mt-2">
          <p className="text-xs muted mb-1">{t("targets.githubApp.createOwnHint")}</p>
          <div className="flex gap-2 flex-wrap">
            <input
              aria-label={t("targets.githubApp.githubOrg")}
              placeholder={t("targets.githubApp.githubOrg")}
              value={org}
              onChange={(e) => setOrg(e.target.value)}
            />
            <input
              aria-label={t("targets.githubApp.appName")}
              placeholder={t("targets.githubApp.appName")}
              value={name}
              maxLength={34}
              onChange={(e) => setName(e.target.value)}
            />
            <button className="btn sm primary" disabled={!name.trim() || create.isPending} onClick={() => create.mutate()}>
              {t("targets.githubApp.create")}
            </button>
          </div>
        </div>
      )}
      {(s.error || err) && <p className="text-xs danger-text mt-1">{s.error ?? err?.message}</p>}
      {!s.installed && <p className="text-xs muted mt-2">{t("targets.githubApp.orToken")}</p>}
    </div>
  );
}
