import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, patch, type Principal } from "../api";

type Session = { created_at: string; expires_at: string; current: boolean };

export default function AccountSettings({ me }: { me: Principal }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const [name, setName] = useState(me.DisplayName);
  const [currentPw, setCurrentPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [note, setNote] = useState("");

  const sessions = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api<Session[]>("/auth/sessions"),
  });

  const saveName = useMutation({
    mutationFn: () => patch("/auth/me", { display_name: name.trim() }),
    onSuccess: () => {
      setNote(t("account.nameSaved"));
      for (const key of ["me", "users", "orgchart", "human"]) qc.invalidateQueries({ queryKey: [key] });
    },
    onError: (e) => setNote(String(e)),
  });

  const savePw = useMutation({
    mutationFn: () => patch("/auth/me", { password: newPw, current_password: currentPw }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
    onError: (e) => setNote(String(e)),
  });

  const revokeOthers = useMutation({
    mutationFn: () => del<{ revoked: number }>("/auth/sessions"),
    onSuccess: (res) => {
      setNote(res.revoked === 1 ? t("account.revokedOne") : t("account.revokedMany", { count: res.revoked }));
      qc.invalidateQueries({ queryKey: ["sessions"] });
    },
  });

  const others = (sessions.data ?? []).filter((s) => !s.current).length;

  return (
    <>
      <h2 className="text-sm secondary mt-6 mb-2">{t("account.title")}</h2>
      {note && <p className="text-xs mb-3" style={{ color: "var(--text-secondary)" }}>{note}</p>}

      <form
        className="card mb-4 flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          saveName.mutate();
        }}
      >
        <div className="flex-1 min-w-52">
          <label>{t("account.displayName")}</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <button className="btn primary" disabled={saveName.isPending || name.trim() === me.DisplayName}>
          {t("account.save")}
        </button>
      </form>

      <form
        className="card mb-4"
        onSubmit={(e) => {
          e.preventDefault();
          savePw.mutate();
        }}
      >
        <div className="flex gap-3 items-end flex-wrap">
          <div className="flex-1 min-w-44">
            <label>{t("account.currentPassword")}</label>
            <input type="password" value={currentPw} onChange={(e) => setCurrentPw(e.target.value)} autoComplete="current-password" required />
          </div>
          <div className="flex-1 min-w-44">
            <label>{t("account.newPassword")}</label>
            <input type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} autoComplete="new-password" minLength={8} required />
          </div>
          <button className="btn primary" disabled={savePw.isPending}>
            {t("account.changePassword")}
          </button>
        </div>
        <p className="muted text-xs mt-2 mb-0">
          {t("account.passwordHint")}
        </p>
      </form>

      <div className="card">
        <div className="flex items-center gap-3 mb-2">
          <span className="text-sm font-medium">{t("account.sessions")}</span>
          <span className="spacer flex-1" />
          {others > 0 && (
            <button className="btn sm" onClick={() => revokeOthers.mutate()} disabled={revokeOthers.isPending}>
              {t("account.revokeOthers")}
            </button>
          )}
        </div>
        {(sessions.data ?? []).map((s, i) => (
          <div key={i} className="flex items-center gap-3 text-xs py-1" style={{ borderTop: i > 0 ? "0.5px solid var(--border)" : "none" }}>
            <span>{t("account.loggedIn", { date: new Date(s.created_at).toLocaleString(locale) })}</span>
            <span className="muted">{t("account.expiresAt", { date: new Date(s.expires_at).toLocaleString(locale) })}</span>
            {s.current && <span className="badge st-done">{t("account.currentSession")}</span>}
          </div>
        ))}
        {sessions.data?.length === 0 && <p className="muted text-xs m-0">{t("account.noSessions")}</p>}
      </div>
    </>
  );
}
