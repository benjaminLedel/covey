import { useTranslation } from "react-i18next";
import { NavLink } from "react-router";

/** Kopf und Unternavigation des Plattform-Panels.
 *
 *  Steht in einer eigenen Datei, weil ihn zwei Seiten brauchen, die
 *  auseinander hervorgehen: Platform.tsx rendert die Mandantenliste aus
 *  Organizations.tsx als Startseite. Läge der Kopf in einer der beiden,
 *  importierten sie einander im Kreis. */
export default function PlatformHeader() {
  const { t } = useTranslation();
  return (
    <>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">{t("platform.title")}</h1>
        <span className="muted">{t("platform.subtitle")}</span>
      </div>
      <nav className="subnav">
        <NavLink to="/platform" end className={({ isActive }) => (isActive ? "active" : "")}>
          {t("platform.tabOrgs")}
        </NavLink>
        <NavLink to="/platform/accounts" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("platform.tabAccounts")}
        </NavLink>
        <NavLink to="/platform/settings" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("platform.tabSettings")}
        </NavLink>
        <NavLink to="/platform/waitlist" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("platform.tabWaitlist")}
        </NavLink>
      </nav>
    </>
  );
}
