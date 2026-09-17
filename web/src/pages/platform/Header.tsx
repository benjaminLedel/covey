import { useTranslation } from "react-i18next";
import { NavLink } from "react-router";

/** PlatformHeader is the header and sub-navigation of the platform panel.
 *
 *  It stands in its own file because two pages need it that come out of each
 *  other: Platform.tsx renders the tenant list from Organizations.tsx as its
 *  start page. Were the header in either of the two, they would import each
 *  other in a circle. */
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
        <NavLink to="/platform/mail" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("platform.tabMail")}
        </NavLink>
        <NavLink to="/platform/waitlist" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("platform.tabWaitlist")}
        </NavLink>
      </nav>
    </>
  );
}
