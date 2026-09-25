import { useTranslation } from "react-i18next";
import { useLocation } from "react-router";
import { BirdMark } from "../components/BirdMark";
import { appLink } from "../components/MobilePairing";

/* Where the pairing link lands when no app took it (#333).
 *
 * On a phone with the app installed, app.covey.work/pair?code=… never reaches
 * the browser — iOS and Android hand it to the app. Everything else ends up
 * here: a self-hosted instance (whose host the app does not claim), a phone
 * without the app, a desktop. The page hands the same code on through the
 * app's own scheme, and it works signed in or not: the code is the badge. */
export default function PairLink() {
  const { t } = useTranslation();
  const { search } = useLocation();
  const code = new URLSearchParams(search).get("code") ?? "";
  const valid = code.startsWith("coveypair_");
  const host = window.location.host;

  return (
    <div className="min-h-screen flex items-center justify-center" style={{ padding: 16, background: "var(--surface-0)" }}>
      <div className="card" style={{ maxWidth: 420, width: "100%", padding: 28 }}>
        <div className="flex items-center gap-3 mb-4">
          <BirdMark size={36} />
          <span style={{ fontSize: 20, fontWeight: 600 }}>covey</span>
        </div>
        <h1 style={{ fontSize: 22, fontWeight: 600, margin: "0 0 8px" }}>{t("appLink.title")}</h1>
        {valid ? (
          <>
            <p className="muted text-sm mt-0 mb-4">{t("appLink.lead", { host })}</p>
            <a className="btn primary" href={appLink(window.location.origin, code)} style={{ display: "inline-block" }}>
              {t("appLink.open")}
            </a>
            <p className="muted text-xs mt-4 mb-0">{t("appLink.noApp")}</p>
          </>
        ) : (
          <p className="muted text-sm mt-0 mb-0">{t("appLink.missing")}</p>
        )}
      </div>
    </div>
  );
}
