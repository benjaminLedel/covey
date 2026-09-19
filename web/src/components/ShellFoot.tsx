import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { NavLink } from "react-router";
import { buildInfo, post, type Principal } from "../api";
import { useMemberships, useSwitchOrg } from "./OrgSwitcher";
import { NavIcon, initials } from "./navicons";
import GitHubLink from "./GitHubLink";
import LangPicker from "./LangPicker";
import ThemeSwitch from "./ThemeSwitch";

/* Der Fuß der linken Spalte: wer angemeldet ist, und dahinter alles, was man
 * selten braucht — Organisation wechseln, Erscheinungsbild, Sprache, Hilfe,
 * abmelden.
 *
 * Er liegt hier und nicht in einer der beiden Schalen, weil beide ihn tragen
 * und er in beiden derselbe sein muss. Bis dahin hatte der Workspace eine
 * eigene Kopfzeile mit Erscheinungsbild, Sprache und einem Abmelden-Knopf
 * daneben — zwei Grundgerüste für eine Anwendung, und an zwei Stellen
 * gepflegt.
 */
export default function ShellFoot({
  me,
  onLogout,
  onHelp,
}: {
  me: Principal;
  onLogout: () => void;
  /** Öffnet die Hilfe-Schublade, die die Schale selbst hält. */
  onHelp: () => void;
}) {
  const { t } = useTranslation();
  const [userMenu, setUserMenu] = useState(false);
  const memberships = useMemberships();
  const seats = memberships.data ?? [];
  const switchOrg = useSwitchOrg(onLogout);
  const activeOrg = seats.length > 1 ? seats.find((m) => m.org_id === me.OrgID) : undefined;
  const build = useQuery({ queryKey: ["version"], queryFn: buildInfo, staleTime: Infinity, retry: false });

  const logout = async () => {
    await post("/auth/logout");
    onLogout();
  };

  return (
    <div className="side-foot">
  <div className="suser-row">
    <NavLink to="/profile" className="suser" title={t("nav.profile")}>
      <span className="avatar">{initials(me.DisplayName)}</span>
      <span className="min-w-0">
        <span className="nm truncate block">{me.DisplayName}</span>
        <span className="rl block truncate">
          {t(`role.${me.Role}`, me.Role)}
          {activeOrg && ` · ${activeOrg.org_name}`}
        </span>
      </span>
    </NavLink>
    <button
      className={`icon-btn foot-menu-btn${userMenu ? " open" : ""}`}
      onClick={() => setUserMenu(v => !v)}
      title={t("nav.userMenu")}
      aria-label={t("nav.userMenu")}
      aria-expanded={userMenu}
    >
      <NavIcon name="dots" />
    </button>
    {userMenu && (
      <>
        <div className="foot-menu-backdrop" onClick={() => setUserMenu(false)} />
        <div className="foot-menu">
          {seats.length > 1 && (
            <>
              <div className="foot-menu-sec">{t("nav.orgSwitch")}</div>
              {seats.map((m) => {
                const active = m.org_id === me.OrgID;
                return (
                  <button
                    key={m.org_id}
                    onClick={() => { setUserMenu(false); switchOrg.mutate(m.org_id); }}
                    disabled={active || switchOrg.isPending}
                    aria-current={active ? "true" : undefined}
                    style={active ? { fontWeight: 600 } : undefined}
                  >
                    <NavIcon name="box" />
                    <span className="truncate">{m.org_name}</span>
                  </button>
                );
              })}
              <div className="sep" />
            </>
          )}
          <div className="foot-menu-sec">{t("theme.label")}</div>
          <ThemeSwitch />
          <div className="sep" />
          <LangPicker variant="menu" />
          <div className="sep" />
          <GitHubLink url={build.data?.source} variant="menu" />
          <button onClick={() => { setUserMenu(false); onHelp(); }}>
            <NavIcon name="help" />
            {t("nav.help")}
          </button>
          <div className="sep" />
          <button className="danger" onClick={logout}>
            <NavIcon name="logout" />
            {t("nav.logout")}
          </button>
        </div>
      </>
    )}
      </div>
    </div>
  );
}
