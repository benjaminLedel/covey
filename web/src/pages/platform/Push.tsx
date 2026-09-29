import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, patch, post, put, type PlatformPush } from "../../api";
import PlatformHeader from "./Header";

/** Push notifications of this installation (#431).
 *
 *  One sender for the iPhone and Android: Firebase Cloud Messaging with a
 *  service account of the app's Firebase project. Whoever has none hands the
 *  notifications to a relay that has. The page says which of the three
 *  applies, because until now nothing in the interface said whether push was
 *  on at all.
 *
 *  The service account is read in the browser and sent as text; the server
 *  checks it before it seals it, and never hands it back — what comes back
 *  is the project and the account it names. */

const MODES = ["direct", "relay", "off"] as const;

export default function Push() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const push = useQuery({ queryKey: ["platform", "push"], queryFn: () => api<PlatformPush>("/platform/push") });
  const cfg = push.data;

  const [mode, setMode] = useState<PlatformPush["mode"]>("relay");
  const [relayURL, setRelayURL] = useState("");
  const [accept, setAccept] = useState(false);
  // The server is the truth: after a save, and after a refused one, the form
  // shows what applies.
  useEffect(() => {
    if (!cfg) return;
    setMode(cfg.mode);
    setRelayURL(cfg.relay_url);
    setAccept(cfg.relay_accept);
  }, [cfg, push.dataUpdatedAt]);

  const done = (next: PlatformPush) => qc.setQueryData(["platform", "push"], next);
  const changed = !!cfg && (mode !== cfg.mode || relayURL !== cfg.relay_url || accept !== cfg.relay_accept);
  const save = useMutation({
    mutationFn: () => patch<PlatformPush>("/platform/push", { mode, relay_url: relayURL, relay_accept: accept }),
    onSuccess: done,
  });

  const file = useRef<HTMLInputElement>(null);
  const upload = useMutation({
    mutationFn: async (f: File) => put<PlatformPush>("/platform/push/credentials", { credentials: await f.text() }),
    onSuccess: done,
    onSettled: () => {
      if (file.current) file.current.value = "";
    },
  });
  const remove = useMutation({ mutationFn: () => del<PlatformPush>("/platform/push/credentials"), onSuccess: done });
  const test = useMutation({
    mutationFn: () => post<{ ok: boolean; project_id: string }>("/platform/push/test", {}),
    onSettled: () => qc.invalidateQueries({ queryKey: ["platform", "push"] }),
  });

  if (push.isError) {
    return (
      <div>
        <PlatformHeader />
        <p className="text-xs" style={{ color: "var(--text-danger)" }}>{(push.error as Error).message}</p>
      </div>
    );
  }
  if (!cfg) {
    return (
      <div>
        <PlatformHeader />
      </div>
    );
  }

  const creds = cfg.credentials;
  const status =
    cfg.mode === "off"
      ? t("platform.push.statusOff")
      : cfg.mode === "relay"
        ? t("platform.push.statusRelay", { url: cfg.relay_url })
        : !creds.set || creds.error
          ? t("platform.push.statusDirectMissing")
          : cfg.relay_accept
            ? t("platform.push.statusDirectRelaying", { project: creds.project_id })
            : t("platform.push.statusDirect", { project: creds.project_id });
  const warn = cfg.mode === "direct" && (!creds.set || !!creds.error);
  const mutationError = (save.error ?? upload.error ?? remove.error) as Error | null;

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>{t("platform.push.desc")}</p>

      <p
        className="text-sm mb-3"
        style={{ maxWidth: 640, color: warn ? "var(--text-warning)" : undefined }}
        data-testid="push-status"
      >
        {status}
      </p>

      <div className="card mb-3" style={{ padding: "15px 17px", maxWidth: 640 }}>
        <div className="text-sm font-medium mb-2">{t("platform.push.mode")}</div>
        {MODES.map((m) => (
          <label key={m} className="flex items-start gap-2 mb-2" style={{ cursor: "pointer" }}>
            <input type="radio" name="push-mode" value={m} checked={mode === m} onChange={() => setMode(m)} />
            <span>
              <span className="text-sm">{t(`platform.push.mode_${m}`)}</span>
              <span className="muted text-xs" style={{ display: "block" }}>{t(`platform.push.mode_${m}Desc`)}</span>
            </span>
          </label>
        ))}

        {mode === "relay" && (
          <div className="flex items-center gap-4 mt-3 mb-3 flex-wrap">
            <div className="flex-1 min-w-52">
              <div className="text-sm font-medium">{t("platform.push.relayURL")}</div>
              <div className="muted text-xs">{t("platform.push.relayURLDesc")}</div>
            </div>
            <input
              type="text"
              value={relayURL}
              placeholder="https://app.covey.work"
              onChange={(e) => setRelayURL(e.target.value)}
              style={{ width: 260 }}
            />
          </div>
        )}
        {mode === "direct" && (
          <label className="flex items-start gap-2 mt-3 mb-3" style={{ cursor: "pointer" }}>
            <input type="checkbox" checked={accept} onChange={(e) => setAccept(e.target.checked)} />
            <span>
              <span className="text-sm">{t("platform.push.relayAccept")}</span>
              <span className="muted text-xs" style={{ display: "block" }}>{t("platform.push.relayAcceptDesc")}</span>
            </span>
          </label>
        )}

        <div className="flex items-center gap-3 flex-wrap mt-2">
          <button className="btn sm primary" disabled={save.isPending || !changed} onClick={() => save.mutate()}>
            {t("platform.save")}
          </button>
        </div>
      </div>

      <div className="card mb-3" style={{ padding: "15px 17px", maxWidth: 640 }}>
        <div className="text-sm font-medium">{t("platform.push.account")}</div>
        <div className="muted text-xs mb-3">{t("platform.push.accountDesc")}</div>

        {creds.set ? (
          <div className="text-xs mb-3">
            <div className="mono">{creds.project_id}</div>
            <div className="mono muted">{creds.client_email}</div>
            <div className="muted">
              {creds.source === "environment" ? t("platform.push.fromEnvironment") : t("platform.push.fromSettings")}
            </div>
            {creds.error && (
              <div className="mono" style={{ color: "var(--text-danger)" }}>{creds.error}</div>
            )}
          </div>
        ) : (
          <p className="muted text-xs mb-3">{t("platform.push.noAccount")}</p>
        )}

        <div className="flex items-center gap-3 flex-wrap">
          <input
            ref={file}
            type="file"
            accept="application/json,.json"
            aria-label={t("platform.push.upload")}
            style={{ display: "none" }}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) upload.mutate(f);
            }}
          />
          <button className="btn sm" disabled={upload.isPending} onClick={() => file.current?.click()}>
            {creds.set && creds.source === "settings" ? t("platform.push.replace") : t("platform.push.upload")}
          </button>
          {creds.set && creds.source === "settings" && (
            <button className="btn sm danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
              {t("platform.push.remove")}
            </button>
          )}
          <button className="btn sm" disabled={test.isPending || !creds.set} onClick={() => test.mutate()}>
            {test.isPending ? t("platform.push.testing") : t("platform.push.test")}
          </button>
        </div>
        {mutationError && (
          <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{mutationError.message}</p>
        )}
        {/* Google's answer verbatim: invalid_grant sends somebody to a
            deleted key, a timeout to the egress. */}
        {test.isError && (
          <p className="text-xs mt-2 mono" style={{ color: "var(--text-danger)" }}>{(test.error as Error).message}</p>
        )}
        {test.isSuccess && (
          <p className="text-xs mt-2" style={{ color: "var(--text-success)" }}>
            {t("platform.push.testOk", { project: test.data?.project_id })}
          </p>
        )}
        <p className="muted text-xs mt-3">
          {cfg.last_test_at === ""
            ? t("platform.push.neverTested")
            : cfg.last_test_error === ""
              ? t("platform.push.lastOk", { date: new Date(cfg.last_test_at).toLocaleString() })
              : t("platform.push.lastFailed", {
                  date: new Date(cfg.last_test_at).toLocaleString(),
                  error: cfg.last_test_error,
                })}
        </p>
      </div>

      <p className="muted text-xs" style={{ maxWidth: 640 }}>{t("platform.push.apnsNote")}</p>
    </div>
  );
}
