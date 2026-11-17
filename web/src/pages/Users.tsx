import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, photoUrl, post, ROLES, type Human, type Principal } from "../api";
import { Avatar } from "../components/person";

/** `embedded` leaves the own page head out — in the administration panel
 *  one already stands above, and two headings stacked are one
 *  too many. */
export default function Users({ me, embedded }: { me: Principal; embedded?: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => api<Human[]>("/users"),
    retry: false,
  });

  if (users.isError) {
    return (
      <div>
        {!embedded && <h1 className="text-[22px] mb-3">{t("users.title")}</h1>}
        <p className="muted">{t("users.noAccess", { role: me.Role })}</p>
      </div>
    );
  }

  const list = users.data ?? [];
  return (
    <div>
      {!embedded && <h1 className="text-[22px] mb-2">{t("users.title")}</h1>}
      {/* The list first (#743). Single sign-on has its own tab, the form
          opens on demand: adding someone is the rare case, looking up who
          is here and what they may do the common one. */}
      <div className="flex items-start justify-between gap-3 mb-1 flex-wrap">
        <p className="muted text-xs m-0" style={{ maxWidth: 640 }}>
          {t("users.desc")}
        </p>
        {!adding && (
          <button className="btn primary sm" onClick={() => setAdding(true)}>
            {t("users.add")}
          </button>
        )}
      </div>
      <RoleHelp />
      {adding && (
        <CreateUser
          onDone={() => {
            setAdding(false);
            qc.invalidateQueries({ queryKey: ["users"] });
          }}
          onCancel={() => setAdding(false)}
        />
      )}
      <div className="muted text-xs mb-2">{t("users.count", { count: list.length })}</div>
      <div className="card member-list">
        {list.map((u) => (
          <UserRow key={u.id} user={u} me={me} />
        ))}
      </div>
    </div>
  );
}

/* What the roles may do, in one line each. The select offered five names
   and nothing that said what lies behind them. */
function RoleHelp() {
  const { t } = useTranslation();
  return (
    <details className="settings-advanced mt-2 mb-4" style={{ maxWidth: 640 }}>
      <summary>{t("users.rolesHelp")}</summary>
      <dl className="role-help">
        {ROLES.map((r) => (
          <div key={r}>
            <dt>{t(`role.${r}`, r)}</dt>
            <dd className="muted">{t(`users.roleDesc.${r}`)}</dd>
          </div>
        ))}
      </dl>
    </details>
  );
}

function CreateUser({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("agent_owner");
  const [password, setPassword] = useState("");
  const mut = useMutation({
    mutationFn: () => post("/users", { email, display_name: name, role, password }),
    onSuccess: () => {
      setEmail("");
      setName("");
      setPassword("");
      onDone();
    },
  });
  return (
    <form
      className="card mb-4"
      onSubmit={(e) => {
        e.preventDefault();
        mut.mutate();
      }}
    >
      <div className="flex gap-3 items-end flex-wrap">
        <div className="flex-1 min-w-48">
          <label>{t("users.email")}</label>
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </div>
        <div className="flex-1 min-w-40">
          <label>{t("users.name")}</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="min-w-40">
          <label>{t("users.role")}</label>
          <select value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.map((value) => (
              <option key={value} value={value}>
                {t(`role.${value}`, value)}
              </option>
            ))}
          </select>
        </div>
        <div className="min-w-44">
          <label>{t("users.password")}</label>
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} minLength={8} required />
        </div>
        <button className="btn primary" disabled={mut.isPending}>
          {t("users.create")}
        </button>
        <button type="button" className="btn" onClick={onCancel}>
          {t("users.cancel")}
        </button>
      </div>
      <p className="muted text-xs mt-2 mb-0">
        {t("users.profileHint")}
      </p>
      {mut.isError && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(mut.error as Error).message}</p>}
    </form>
  );
}

/* A row only shows and leads on (#743). Role, password and removal sit on
   the person's page behind an explicit "change" — a select in the list
   changed a role on the first wrong click. */
function UserRow({ user, me }: { user: Human; me: Principal }) {
  const { t } = useTranslation();
  const isSelf = user.id === me.ID;
  return (
    <Link to={`/people/${user.id}`} state={{ from: "members" }} className="member-row member-link">
      <Avatar name={user.display_name} human photo={photoUrl(user.id, user.photo_id)} />
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium truncate">
          {user.display_name}
          {isSelf && <span className="muted text-xs"> {t("users.self")}</span>}
        </div>
        <div className="muted text-xs truncate">
          {user.job_title ? `${user.job_title} · ` : ""}
          {user.email}
        </div>
      </div>
      <span className="ntag">{t(`role.${user.role}`, user.role)}</span>
      <span className="member-chev" aria-hidden="true">›</span>
    </Link>
  );
}
