import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, patch, put, type Principal } from "../api";
import ApiKeys from "./ApiKeys";
import { NavIcon } from "./navicons";
import Section from "./Section";
import MobilePairing from "./MobilePairing";
import ThemeSwitch from "./ThemeSwitch";
import { LANG_LIST, istLang, ladeSprache, merkeSprache } from "../i18n";

type Session = { created_at: string; expires_at: string; current: boolean };

/* The four classes from internal/notify. They stand here as an ordering —
   which ones exist the server decides, and what it does not know does not
   appear in its response. */
const NOTIFY_CLASSES = ["decision", "task", "cost", "ops"] as const;

/* The response from /auth/notifications: one's own switches, and the classes
   that the installation has switched off for everyone (#180) — those stand
   here greyed out, instead of offering a switch that does nothing. */
type NotifyPrefs = { prefs: Record<string, boolean>; disabled: string[] };

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

  /* Which mails this person receives (#169). The server answers with the
     EFFECTIVE values, defaults factored in — so the interface carries no
     second copy of what "on by default" means. */
  const notify = useQuery({
    queryKey: ["notifications"],
    queryFn: () => api<NotifyPrefs>("/auth/notifications"),
  });
  const setNotify = useMutation({
    /* Only the one switch that changed goes over: two open browsers would
       otherwise overwrite each other's responses, which they had not
       even asked for. */
    mutationFn: (change: Record<string, boolean>) =>
      put<NotifyPrefs>("/auth/notifications", change),
    onSuccess: (prefs) => qc.setQueryData(["notifications"], prefs),
    onError: (e) => setNote(String(e)),
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
      <Section title={t("account.title")} desc={note || undefined}>
        <form
          className="field-grid two"
          onSubmit={(e) => {
            e.preventDefault();
            saveName.mutate();
          }}
        >
          <div className="field-wide flex gap-2 items-end">
            <div className="flex-1 min-w-0">
              <label htmlFor="account-name">{t("account.displayName")}</label>
              <input id="account-name" value={name} onChange={(e) => setName(e.target.value)} required />
            </div>
            <button className="btn primary" disabled={saveName.isPending || name.trim() === me.DisplayName}>
              {t("account.save")}
            </button>
          </div>
          {/* Language and appearance (#432): here with the other settings of
              the account, not in the menu at the foot. Both are kept in this
              browser. */}
          <div>
            <label htmlFor="account-lang">{t("lang.label")}</label>
            <select
              id="account-lang"
              value={istLang(i18n.language) ? i18n.language : "en"}
              onChange={(e) => {
                const lang = e.target.value;
                if (!istLang(lang)) return;
                merkeSprache(lang);
                void ladeSprache(lang);
              }}
            >
              {LANG_LIST.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.flag} {l.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="account-theme">{t("theme.label")}</label>
            <ThemeSwitch id="account-theme" />
          </div>
        </form>
      </Section>

      <Section title={t("person.passwordTitle")} desc={t("account.passwordHint")}>
        <form
          className="field-grid two"
          onSubmit={(e) => {
            e.preventDefault();
            savePw.mutate();
          }}
        >
          <div>
            <label htmlFor="pw-current">{t("account.currentPassword")}</label>
            <input id="pw-current" type="password" value={currentPw} onChange={(e) => setCurrentPw(e.target.value)} autoComplete="current-password" required />
          </div>
          <div>
            <label htmlFor="pw-new">{t("account.newPassword")}</label>
            <input id="pw-new" type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} autoComplete="new-password" minLength={8} required />
          </div>
          <div className="field-wide flex justify-end">
            <button className="btn primary" disabled={savePw.isPending || !currentPw || newPw.length < 8}>
              {t("account.changePassword")}
            </button>
          </div>
        </form>
      </Section>

      <Section
        title={t("account.sessions")}
        action={
          others > 0 && (
            <button className="btn sm" onClick={() => revokeOthers.mutate()} disabled={revokeOthers.isPending}>
              <NavIcon name="logout" />
              {t("account.revokeOthers")}
            </button>
          )
        }
      >
        <div className="ps-rows">
          {(sessions.data ?? []).map((s, i) => (
            <div key={i} className="ps-row wide">
              <span className="ps-value">
                <span>{t("account.loggedIn", { date: new Date(s.created_at).toLocaleString(locale) })}</span>
                <span className="ps-hint">{t("account.expiresAt", { date: new Date(s.expires_at).toLocaleString(locale) })}</span>
              </span>
              {s.current && <span className="ntag">{t("account.currentSession")}</span>}
            </div>
          ))}
        </div>
        {sessions.data?.length === 0 && <p className="ps-hint m-0">{t("account.noSessions")}</p>}
      </Section>

      <Section title={t("account.notify.title")} desc={t("account.notify.desc")}>
        <div className="ps-rows">
          {NOTIFY_CLASSES.map((c) => {
            const instanceOff = notify.data?.disabled?.includes(c) ?? false;
            return (
              <label key={c} className={`ps-check${instanceOff ? " off" : ""}`}>
                <input
                  type="checkbox"
                  checked={!instanceOff && (notify.data?.prefs?.[c] ?? false)}
                  disabled={instanceOff || notify.isLoading || setNotify.isPending}
                  onChange={(e) => setNotify.mutate({ [c]: e.target.checked })}
                />
                <span className="ps-value">
                  <span>{t(`account.notify.${c}`)}</span>
                  <span className="ps-hint">
                    {instanceOff ? t("account.notify.instanceOff") : t(`account.notify.${c}Hint`)}
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      </Section>

      <MobilePairing />

      {/* API keys and the Claude Code skill are for people who script
          against covey — folded away so the page reads as an account page,
          not a developer console. */}
      <details className="ps-advanced">
        <summary>
          <span className="ps-title">{t("account.developer")}</span>
          <span className="ps-hint">{t("account.developerDesc")}</span>
        </summary>
        <ApiKeys />
        <Section
          title={t("account.skill.title")}
          desc={t("account.skill.intro")}
          action={
            <a className="btn sm" href="/api/v1/skills/covey-agent.zip" download="covey-agent-skill.zip">
              {t("account.skill.download")}
            </a>
          }
        >
          <p className="text-sm mb-2 mt-0">{t("account.skill.downloadHint")}</p>
          <pre className="ps-code">{`unzip covey-agent-skill.zip -d ~/.claude/skills/`}</pre>
          <p className="ps-hint mt-3 mb-1">{t("account.skill.repoNote")}</p>
          <ol className="text-sm" style={{ margin: 0, paddingLeft: 18, lineHeight: 1.7 }}>
            <li>{t("account.skill.step1")}</li>
            <li>
              {t("account.skill.step3")} <code>/covey-agent</code>
            </li>
          </ol>
          <p className="ps-hint mt-2 mb-0">{t("account.skill.hint")}</p>
        </Section>
      </details>
    </>
  );
}
