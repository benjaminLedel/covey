import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, post, put, type Setting } from "../../api";
import PlatformHeader from "./Header";

/** Mail delivery of this installation (#167).
 *
 *  Its own page and not one row in the switch list, because of the button:
 *  a wrong mail setting is noticed first by whoever never gets their
 *  confirmation link — and that very person can tell no one, since for that
 *  they would need an account.
 *
 *  The test mail takes the SAME route as a real one (same sender, the same
 *  stored settings), and it sends the STORED state, not the unsaved form:
 *  save first, then check, so that what is proven is what is current. That is
 *  why the button stays locked while anything is still open. */

const FIELDS = [
  { key: "mail.smtp_host", type: "text", placeholder: "mail.example.com" },
  { key: "mail.smtp_port", type: "text", placeholder: "587" },
  { key: "mail.security", type: "choice", choices: ["starttls", "tls", "none"] },
  { key: "mail.smtp_user", type: "text", placeholder: "no-reply@example.com" },
  { key: "mail.smtp_password", type: "secret" },
  { key: "mail.from", type: "text", placeholder: "no-reply@example.com" },
  { key: "mail.from_name", type: "text", placeholder: "covey" },
] as const;

export default function Mail() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const settings = useQuery({
    queryKey: ["platform", "settings"],
    queryFn: () => api<Setting[]>("/platform/settings"),
  });
  const byKey = Object.fromEntries((settings.data ?? []).map((s) => [s.key, s]));

  const [draft, setDraft] = useState<Record<string, string>>({});
  // The server is the truth: after every save and on every reload what stands
  // there applies again. Without this the form kept showing the wish instead
  // of the state after a failed PUT.
  useEffect(() => {
    if (!settings.data) return;
    const next: Record<string, string> = {};
    for (const f of FIELDS) next[f.key] = f.type === "secret" ? "" : byKey[f.key]?.value ?? "";
    setDraft(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settings.dataUpdatedAt]);

  const changed = FIELDS.filter((f) =>
    f.type === "secret" ? draft[f.key] !== "" : (draft[f.key] ?? "") !== (byKey[f.key]?.value ?? ""),
  );

  const save = useMutation({
    mutationFn: async () => {
      // One after another, not in parallel: if a value fails, the message
      // should belong to it and not to one bulk request.
      for (const f of changed) await put(`/platform/settings/${f.key}`, { value: draft[f.key] ?? "" });
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform", "settings"] }),
  });

  const test = useMutation({
    mutationFn: () => post<{ ok: boolean; to: string }>("/platform/mail/test", {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform", "settings"] }),
  });

  const lastAt = byKey["mail.last_test_at"]?.value ?? "";
  const lastError = byKey["mail.last_test_error"]?.value ?? "";

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>{t("platform.mailDesc")}</p>

      <div className="card mb-3" style={{ padding: "15px 17px", maxWidth: 640 }}>
        {FIELDS.map((f) => (
          <div key={f.key} className="flex items-center gap-4 mb-3 flex-wrap">
            <div className="flex-1 min-w-52">
              <div className="text-sm font-medium mono">{f.key}</div>
              <div className="muted text-xs">{t(`platform.setting.${f.key}`, "")}</div>
            </div>
            {f.type === "choice" ? (
              <select
                value={draft[f.key] ?? ""}
                onChange={(e) => setDraft({ ...draft, [f.key]: e.target.value })}
                style={{ width: 220 }}
              >
                {f.choices.map((c) => (
                  <option key={c} value={c}>
                    {t(`platform.choice.mail.security.${c}`, c)}
                  </option>
                ))}
              </select>
            ) : (
              <input
                type={f.type === "secret" ? "password" : "text"}
                value={draft[f.key] ?? ""}
                placeholder={
                  f.type === "secret"
                    ? byKey[f.key]?.set
                      ? t("platform.mailPasswordSet")
                      : t("platform.mailPasswordUnset")
                    : f.placeholder
                }
                onChange={(e) => setDraft({ ...draft, [f.key]: e.target.value })}
                style={{ width: 220 }}
              />
            )}
          </div>
        ))}

        <div className="flex items-center gap-3 flex-wrap">
          <button
            className="btn sm primary"
            disabled={save.isPending || changed.length === 0}
            onClick={() => save.mutate()}
          >
            {t("platform.save")}
          </button>
          <button
            className="btn sm"
            disabled={test.isPending || changed.length > 0 || !byKey["mail.smtp_host"]?.value}
            onClick={() => test.mutate()}
          >
            {test.isPending ? t("platform.mailTesting") : t("platform.mailTest")}
          </button>
          {changed.length > 0 && <span className="muted text-xs">{t("platform.mailSaveFirst")}</span>}
        </div>
        {save.isError && (
          <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(save.error as Error).message}</p>
        )}
        {/* The SMTP error verbatim: `535 5.7.8 authentication failed` sends
            someone to the password, `connection refused` to the port — every
            sentence we put in its place would be less exact. */}
        {test.isError && (
          <p className="text-xs mt-2 mono" style={{ color: "var(--text-danger)" }}>{(test.error as Error).message}</p>
        )}
        {test.isSuccess && (
          <p className="text-xs mt-2" style={{ color: "var(--text-success)" }}>
            {t("platform.mailTestSent", { to: test.data?.to })}
          </p>
        )}
      </div>

      <p className="muted text-xs" style={{ maxWidth: 640 }}>
        {lastAt === ""
          ? t("platform.mailNeverTested")
          : lastError === ""
            ? t("platform.mailLastOk", { date: new Date(lastAt).toLocaleString() })
            : t("platform.mailLastFailed", { date: new Date(lastAt).toLocaleString(), error: lastError })}
      </p>
    </div>
  );
}
