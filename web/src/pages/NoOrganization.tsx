import { useTranslation } from "react-i18next";
import { post, type Principal } from "../api";
import { useMemberships, useSwitchOrg } from "../components/OrgSwitcher";
import { BirdMark } from "../public/chrome";

/* Signed in, but without an organisation.

   This state could not exist before: signing in WAS the membership. Since
   sign-in is tied to the account (FR-002, P1), self-registration creates an
   account before any organisation knows about it.

   The page says exactly that and nothing beyond it. Joining and founding
   come as their own step (P5); until then a button that promises both would
   be a pledge nobody redeems.

   One case does have somewhere to go (#262): the account holds seats, but the
   session has none active — the operator added a seat after this sign-in, or
   removed the last one the session was working from. Then the page offers
   those seats instead of the explanation. */
export default function NoOrganization({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const { t } = useTranslation();
  const memberships = useMemberships();
  const switchOrg = useSwitchOrg(onLogout);
  const seats = memberships.data ?? [];

  const abmelden = async () => {
    await post("/auth/logout");
    onLogout();
  };

  return (
    <div className="login-bg pub-shell">
      <div className="landing pub-signin">
        <div className="pub-signin-brand login-rise">
          <BirdMark size={52} />
          <h1 className="login-wordmark">covey</h1>
        </div>
        <div className="login-card login-rise" style={{ animationDelay: "0.16s" }}>
          <h2 className="login-card-title">{t("noOrg.title")}</h2>
          {seats.length > 0 ? (
            <>
              <p className="landing-pitch">{t("noOrg.pick")}</p>
              {seats.map((m) => (
                <button
                  key={m.org_id}
                  className="btn primary w-full justify-center mt-2"
                  onClick={() => switchOrg.mutate(m.org_id)}
                  disabled={switchOrg.isPending}
                >
                  {m.org_name}
                </button>
              ))}
            </>
          ) : (
            <p className="landing-pitch">{t("noOrg.text", { email: me.Email })}</p>
          )}
          <button className="btn w-full justify-center mt-4" onClick={abmelden}>
            {t("noOrg.signOut")}
          </button>
        </div>
      </div>
    </div>
  );
}
