import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { Link, Navigate, useLocation, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { api, del, deletePhoto, isNotFound, patch, photoUrl, ROLES, setPhoto, type Human, type OrgChart, type Principal } from "../api";
import AccountSettings from "../components/AccountSettings";
import { Avatar, PersonLink } from "../components/person";
import ProfileForm from "../components/ProfileForm";
import ChoiceList from "../components/ChoiceList";
import Section from "../components/Section";
import { NavIcon } from "../components/navicons";

export default function PersonPage({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  /* Opened from the member list, the way back leads there — not to the
     org chart one never came from (#743). */
  const fromMembers = (useLocation().state as { from?: string } | null)?.from === "members";
  const person = useQuery({
    queryKey: ["human", id],
    queryFn: () => api<Human>(`/org/humans/${id}`),
    // A 404 does not change on a second try; everything else keeps the default one retry.
    retry: (failures, err) => !isNotFound(err) && failures < 1,
  });
  const chart = useQuery({ queryKey: ["orgchart"], queryFn: () => api<OrgChart>("/org/chart") });

  if (person.isLoading) return null;
  /* Not in the organisation this session works in — the same dead end as on
     the agent page, reached the same ways: signing in again (?weiter=) into
     another seat, or /profile with the id of the previous one (#263). */
  if (isNotFound(person.error)) return <Navigate to="/agents" replace />;
  if (person.isError || !person.data) return <p className="danger-text">{t("person.notFound")}</p>;
  const h = person.data;

  const humans = chart.data?.humans ?? [];
  const agents = chart.data?.agents ?? [];
  const manager = humans.find((m) => m.id === h.manager_id);
  const reports = humans.filter((r) => r.manager_id === h.id);
  const ownAgents = agents.filter((a) => a.supervisor_id === h.id);

  const isAdmin = me.Role === "org_admin";
  const isSelf = h.id === me.ID;

  return (
    <div className="person-page">
      <Link to={fromMembers ? "/administration/members" : "/org"} className="ps-back">
        <span aria-hidden="true">‹</span> {fromMembers ? t("nav.members") : t("person.breadcrumb")}
      </Link>

      <header className="ps-header">
        {isSelf ? (
          <OwnPhoto human={h} />
        ) : (
          <Avatar name={h.display_name} size={56} human photo={photoUrl(h.id, h.photo_id)} />
        )}
        <div className="min-w-0">
          <h1 className="ps-name">
            {h.display_name}
            {isSelf && <span className="ps-self"> {t("person.self")}</span>}
          </h1>
          <p className="ps-meta">
            {[h.job_title, t(`role.${h.role}`, h.role), h.email].filter(Boolean).join(" · ")}
          </p>
        </div>
      </header>

      {/* One's own role and password are not changed here — the header names
          the role, the password sits with the account settings below. */}
      {isAdmin && !isSelf && <AccessSection human={h} isSelf={isSelf} />}

      <Section title={t("person.profile")} desc={t("person.profileDesc")}>
        <ProfileForm human={h} endpoint={isAdmin ? `/users/${h.id}` : "/auth/me"} readOnly={!isAdmin && !isSelf} />
        {!isAdmin && !isSelf && <p className="ps-note">{t("person.readOnlyHint")}</p>}
      </Section>

      <Section
        title={t("person.orgChart")}
        action={
          <Link to="/org" className="ps-link">
            {t("person.openOrgChart")}
          </Link>
        }
      >
        <dl className="ps-dl">
          <div>
            <dt>{t("person.reportsTo")}</dt>
            <dd>{manager ? <PersonLink human={manager} /> : <span className="muted">{t("person.nobody")}</span>}</dd>
          </div>
          <div>
            <dt>{t("person.directReports")}</dt>
            <dd>
              {reports.length === 0 ? (
                <span className="muted">{t("person.noDirect")}</span>
              ) : (
                <span className="flex gap-3 flex-wrap">
                  {reports.map((r) => (
                    <PersonLink key={r.id} human={r} />
                  ))}
                </span>
              )}
            </dd>
          </div>
          <div>
            <dt>{t("person.supervisedAgents")}</dt>
            <dd>
              {ownAgents.length === 0 ? (
                <span className="muted">{t("person.noAgents")}</span>
              ) : (
                <span className="flex gap-3 flex-wrap items-center">
                  {ownAgents.map((a) => (
                    <Link key={a.id} to={`/agents/${a.id}`} className="inline-flex items-center gap-1.5">
                      {a.display_name}
                      <span className={`badge state st-${a.killed ? "killed" : a.status}`}>
                        {t(`status.${a.killed ? "killed" : a.status}`, a.killed ? "killed" : a.status)}
                      </span>
                    </Link>
                  ))}
                </span>
              )}
            </dd>
          </div>
        </dl>
      </Section>

      {isSelf && <AccountSettings me={me} />}
    </div>
  );
}

/* Role, password, removal — what an admin changes about someone's access
   (#743). Each behind its own explicit step: the list used to change a role
   on the first wrong click of a select, and remove a person on one click.
   One's own role is not changed here; that keeps an admin from locking
   themselves out by accident (the server keeps the last admin anyway). */
function AccessSection({ human, isSelf }: { human: Human; isSelf: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [mode, setMode] = useState<"" | "role" | "password" | "remove">("");
  const [role, setRole] = useState(human.role);
  const [password, setPassword] = useState("");
  const invalidate = () => {
    for (const key of ["users", "orgchart", "human"]) void qc.invalidateQueries({ queryKey: [key] });
  };
  const close = () => {
    setMode("");
    setRole(human.role);
    setPassword("");
  };
  const saveRole = useMutation({
    mutationFn: () => patch(`/users/${human.id}`, { role }),
    onSuccess: () => {
      invalidate();
      setMode("");
    },
  });
  const savePassword = useMutation({
    mutationFn: () => patch(`/users/${human.id}`, { password }),
    onSuccess: close,
  });
  const remove = useMutation({
    mutationFn: () => del(`/users/${human.id}`),
    onSuccess: () => {
      invalidate();
      navigate("/administration/members", { replace: true });
    },
  });
  const error = saveRole.error ?? savePassword.error ?? remove.error;
  const roleChoices = ROLES.map((r) => ({ value: r, label: t(`role.${r}`, r), desc: t(`users.roleDesc.${r}`) }));

  return (
    <Section title={t("person.access")} desc={t("person.accessDesc")}>
      <div className="ps-rows">
        <div className="ps-row">
          <span className="ps-label">{t("users.role")}</span>
          <span className="ps-value">
            <span className="ps-strong">{t(`role.${human.role}`, human.role)}</span>
            <span className="ps-hint">{isSelf ? t("person.ownRole") : t(`users.roleDesc.${human.role}`, "")}</span>
          </span>
          {!isSelf && mode !== "role" && (
            <button className="btn sm" onClick={() => setMode("role")}>
              <NavIcon name="edit" />
              {t("person.roleChange")}
            </button>
          )}
        </div>
        {mode === "role" && (
          <form
            className="edit-panel ps-indent"
            onSubmit={(e) => {
              e.preventDefault();
              saveRole.mutate();
            }}
          >
            <ChoiceList
              name="role"
              legend={t("users.role")}
              value={role}
              onChange={setRole}
              choices={roleChoices}
              current={human.role}
              currentLabel={t("person.current")}
            />
            <span className="ps-hint mt-1">{t("person.roleHint")}</span>
            <div className="edit-panel-foot">
              <button type="button" className="btn" onClick={close}>
                {t("users.cancel")}
              </button>
              <button className="btn primary" disabled={role === human.role || saveRole.isPending}>
                <NavIcon name="check" />
                {t("person.roleSave")}
              </button>
            </div>
          </form>
        )}

        <div className="ps-row">
          <span className="ps-label">{t("person.passwordTitle")}</span>
          <span className="ps-value">
            <span className="ps-hint">{isSelf ? t("person.ownPassword") : t("person.passwordHint")}</span>
          </span>
          {!isSelf && mode !== "password" && (
            <button className="btn sm" onClick={() => setMode("password")}>
              <NavIcon name="key" />
              {t("users.passwordReset")}
            </button>
          )}
        </div>
        {mode === "password" && (
          <form
            className="edit-panel ps-indent"
            onSubmit={(e) => {
              e.preventDefault();
              savePassword.mutate();
            }}
          >
            <label htmlFor="new-password">{t("users.newPasswordLabel")}</label>
            <input
              id="new-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={8}
              required
              autoFocus
              autoComplete="new-password"
            />
            <div className="edit-panel-foot">
              <button type="button" className="btn" onClick={close}>
                {t("users.cancel")}
              </button>
              <button className="btn primary" disabled={password.length < 8 || savePassword.isPending}>
                <NavIcon name="check" />
                {t("users.setPassword")}
              </button>
            </div>
          </form>
        )}

        {!isSelf && (
          <div className="ps-row wide">
            <span className="ps-value">
              <span className="ps-strong">{t("person.removeTitle")}</span>
              <span className="ps-hint">{t("person.removeHint")}</span>
            </span>
            {mode !== "remove" && (
              <button className="btn sm quiet danger" onClick={() => setMode("remove")}>
                <NavIcon name="trash" />
                {t("users.delete")}
              </button>
            )}
          </div>
        )}
        {mode === "remove" && (
          <div className="edit-panel danger" role="alertdialog" aria-label={t("person.removeTitle")}>
            <span className="ps-strong">{t("users.confirmDelete", { name: human.display_name })}</span>
            <div className="edit-panel-foot">
              <button className="btn" onClick={close} autoFocus>
                {t("users.cancel")}
              </button>
              <button className="btn danger solid" onClick={() => remove.mutate()} disabled={remove.isPending}>
                <NavIcon name="trash" />
                {t("users.delete")}
              </button>
            </div>
          </div>
        )}
      </div>
      {error && <p className="danger-text text-xs mt-2 mb-0">{(error as Error).message}</p>}
    </Section>
  );
}

/* Das eigene Foto (#377): ein Klick auf das Bild wählt ein neues — auf dem
   Telefon bietet der Browser dabei die Kamera an —, „Entfernen" bringt das
   Monogramm zurück. Den Zuschnitt macht der Server. */
function OwnPhoto({ human }: { human: Human }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const done = () => {
    void qc.invalidateQueries({ queryKey: ["human", human.id] });
    void qc.invalidateQueries({ queryKey: ["me"] });
    void qc.invalidateQueries({ queryKey: ["orgchart"] });
  };
  const upload = useMutation({ mutationFn: setPhoto, onSuccess: done });
  const remove = useMutation({ mutationFn: deletePhoto, onSuccess: done });
  const busy = upload.isPending || remove.isPending;
  return (
    <div className="ps-photo">
      <button
        type="button"
        className="avatar-pick"
        onClick={() => input.current?.click()}
        disabled={busy}
        title={t("person.photoChange")}
        aria-label={t("person.photoChange")}
      >
        <Avatar name={human.display_name} size={56} human photo={photoUrl(human.id, human.photo_id)} />
      </button>
      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png"
        capture="user"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (f) upload.mutate(f);
        }}
      />
      {human.photo_id && (
        <button type="button" className="ps-photo-remove" onClick={() => remove.mutate()} disabled={busy}>
          {t("person.photoRemove")}
        </button>
      )}
      {(upload.isError || remove.isError) && (
        <span className="danger-text text-xs">{t("person.photoFailed")}</span>
      )}
    </div>
  );
}
