import { useTranslation } from "react-i18next";
import { post, type Principal } from "../api";
import { BirdMark } from "../public/chrome";

/* Angemeldet, aber ohne Organisation.

   Diesen Zustand konnte es früher nicht geben: die Anmeldung WAR die
   Mitgliedschaft. Seit die Anmeldung am Konto hängt (FR-002, P1), entsteht ein
   Konto bei der Selbstregistrierung, bevor irgendeine Organisation es kennt.

   Die Seite sagt genau das und nichts darüber hinaus. Beitreten und Gründen
   kommen als eigener Schritt (P5); bis dahin wäre ein Knopf, der beides
   verspricht, eine Zusage, die niemand einlöst. */
export default function NoOrganization({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const { t } = useTranslation();

  const abmelden = async () => {
    await post("/auth/logout");
    onLogout();
  };

  return (
    <div className="login-bg pub-shell">
      <div className="landing pub-signin">
        <div className="pub-signin-brand login-rise">
          <BirdMark size={52} />
          <h1 className="login-wordmark">Covey</h1>
        </div>
        <div className="login-card login-rise" style={{ animationDelay: "0.16s" }}>
          <h2 className="login-card-title">{t("noOrg.title")}</h2>
          <p className="landing-pitch">{t("noOrg.text", { email: me.Email })}</p>
          <button className="btn w-full justify-center mt-4" onClick={abmelden}>
            {t("noOrg.signOut")}
          </button>
        </div>
      </div>
    </div>
  );
}
