import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, post, put, type Setting } from "../../api";
import PlatformHeader from "./Header";

/** Der Mailversand dieser Installation (#167).
 *
 *  Eine eigene Seite und nicht eine Zeile in der Schalterliste, wegen des
 *  Knopfes: eine falsche Mail-Einstellung merkt sonst als Erster, wessen
 *  Bestätigungslink nie ankommt — und genau diese Person kann es niemandem
 *  melden, denn dafür bräuchte sie ein Konto.
 *
 *  Die Testmail nimmt DENSELBEN Weg wie eine echte (derselbe Sender, dieselben
 *  gespeicherten Einstellungen), und sie sendet den GESPEICHERTEN Stand, nicht
 *  das ungespeicherte Formular: erst sichern, dann prüfen, damit das Bewiesene
 *  das Laufende ist. Deshalb ist der Knopf gesperrt, solange etwas offen ist. */

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
  // Der Server ist die Wahrheit: nach jedem Speichern und bei jedem Neuladen
  // gilt wieder, was dort steht. Ohne das zeigte das Formular nach einem
  // fehlgeschlagenen PUT weiter den Wunsch statt des Zustands.
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
      // Nacheinander, nicht parallel: schlägt ein Wert fehl, soll die Meldung
      // zu ihm gehören und nicht die einer Sammelanfrage sein.
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
        {/* Der SMTP-Fehler wortwörtlich: „535 5.7.8 authentication failed“
            schickt jemanden zum Passwort, „connection refused“ zum Port —
            jeder Satz, den wir an seine Stelle setzten, wäre ungenauer. */}
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
