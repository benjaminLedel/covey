import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { buildInfo } from "../api";
import { Modal } from "./Modal";
import MobilePairing, { desktopAppLink } from "./MobilePairing";
import { NavIcon } from "./navicons";

/* Where the apps are (#432). The phone apps are one app for every
   installation — it asks which covey to work with — so the store addresses are
   the project's, not this instance's. A store the app is not published in yet
   stays unlinked: a link that ends in "not found" is worse than a sentence
   that says so. Whoever publishes it sets the address here. */
export const stores: { ios: string | null; android: string | null } = {
  ios: null, // https://apps.apple.com/app/id6817223004 once it has passed review
  android: null, // https://play.google.com/store/apps/details?id=work.covey.covey_mobile
};

export default function AppsDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const build = useQuery({ queryKey: ["version"], queryFn: buildInfo, staleTime: Infinity, retry: false });
  const mac = build.data ? desktopAppLink(build.data.source, build.data.version, "macos") : null;
  const windows = build.data ? desktopAppLink(build.data.source, build.data.version, "windows") : null;

  const rows: { icon: string; name: string; href: string | null; action: string }[] = [
    { icon: "phone", name: t("apps.ios"), href: stores.ios, action: t("apps.appStore") },
    { icon: "phone", name: t("apps.android"), href: stores.android, action: t("apps.playStore") },
    { icon: "laptop", name: t("apps.mac"), href: mac, action: t("apps.download") },
    { icon: "laptop", name: t("apps.windows"), href: windows, action: t("apps.download") },
  ];

  return (
    <Modal title={t("apps.title")} onClose={onClose}>
      <p className="muted text-sm mt-0 mb-4">{t("apps.intro")}</p>

      <h3 className="apps-step">{t("apps.install")}</h3>
      <ul className="apps-list">
        {rows.map((r) => (
          <li key={r.name}>
            <NavIcon name={r.icon} />
            <span className="nm">{r.name}</span>
            {r.href ? (
              <a className="btn sm" href={r.href} target="_blank" rel="noreferrer">
                {r.action}
              </a>
            ) : (
              <span className="muted text-xs">{t("apps.notYet")}</span>
            )}
          </li>
        ))}
      </ul>

      <h3 className="apps-step">{t("apps.pair")}</h3>
      <p className="muted text-xs mt-0 mb-3">{t("apps.pairIntro")}</p>
      <MobilePairing bare />
    </Modal>
  );
}
