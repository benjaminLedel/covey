import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, patch, post, roleLabel, type Human, type Principal } from "../api";
import { Avatar, PersonLink } from "../components/person";

export default function Users({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => api<Human[]>("/users"),
    retry: false,
  });

  if (users.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">{t("users.title")}</h1>
        <p className="muted">{t("users.noAccess", { role: me.Role })}</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("users.title")}</h1>
        <span className="muted">{t("users.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("users.desc")}
      </p>

      <CreateUser onDone={() => qc.invalidateQueries({ queryKey: ["users"] })} />

      {(users.data ?? []).map((u) => (
        <UserRow key={u.id} user={u} me={me} all={users.data ?? []} />
      ))}
    </div>
  );
}

function CreateUser({ onDone }: { onDone: () => void }) {
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
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </div>
        <div className="flex-1 min-w-40">
          <label>{t("users.name")}</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="min-w-40">
          <label>{t("users.role")}</label>
          <select value={role} onChange={(e) => setRole(e.target.value)}>
            {Object.entries(roleLabel).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
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
      </div>
      <p className="muted text-xs mt-2 mb-0">
        {t("users.profileHint")}
      </p>
      {mut.isError && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(mut.error as Error).message}</p>}
    </form>
  );
}

function UserRow({ user, me, all }: { user: Human; me: Principal; all: Human[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const isSelf = user.id === me.ID;
  const [resetting, setResetting] = useState(false);
  const [newPassword, setNewPassword] = useState("");
  const invalidate = () => {
    for (const key of ["users", "orgchart", "human"]) qc.invalidateQueries({ queryKey: [key] });
  };

  const setRole = useMutation({
    mutationFn: (role: string) => patch(`/users/${user.id}`, { role }),
    onSuccess: invalidate,
  });
  const setManager = useMutation({
    mutationFn: (managerId: string) => patch(`/users/${user.id}`, { manager_id: managerId }),
    onSuccess: invalidate,
  });
  const resetPassword = useMutation({
    mutationFn: () => patch(`/users/${user.id}`, { password: newPassword }),
    onSuccess: () => {
      setResetting(false);
      setNewPassword("");
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: () => del(`/users/${user.id}`),
    onSuccess: invalidate,
  });
  const error = setRole.error ?? setManager.error ?? resetPassword.error ?? remove.error;

  const identities = Object.entries(user.identities ?? {});

  return (
    <div className="card mb-2" style={{ padding: "11px 15px" }}>
      <div className="flex items-center gap-4 flex-wrap">
        <Link to={`/people/${user.id}`} title={t("org.openProfile")} style={{ color: "inherit", textDecoration: "none" }}>
          <Avatar name={user.display_name} human />
        </Link>
        <div className="flex-1 min-w-44">
          <div className="text-sm font-medium">
            <PersonLink human={user} />
            {user.job_title && <span className="muted text-xs"> · {user.job_title}</span>}
            {isSelf && <span className="muted text-xs"> {t("users.self")}</span>}
          </div>
          <div className="muted text-xs mono">
            {user.email}
            {identities.map(([system, id]) => (
              <span key={system}> · {system}: {id}</span>
            ))}
          </div>
        </div>
        <select
          value={user.role}
          onChange={(e) => setRole.mutate(e.target.value)}
          disabled={setRole.isPending}
          style={{ width: "auto" }}
        >
          {Object.entries(roleLabel).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
        <div>
          <label style={{ margin: 0, fontSize: 10 }}>{t("users.reportsTo")}</label>
          <select
            value={user.manager_id ?? ""}
            onChange={(e) => setManager.mutate(e.target.value)}
            disabled={setManager.isPending}
            style={{ width: "auto" }}
          >
            <option value="">{t("users.nobody")}</option>
            {all
              .filter((h) => h.id !== user.id)
              .map((h) => (
                <option key={h.id} value={h.id}>
                  {h.display_name}
                </option>
              ))}
          </select>
        </div>
        <button className="btn sm" onClick={() => setResetting(!resetting)}>
          {t("users.passwordReset")}
        </button>
        <button
          className="btn sm danger"
          onClick={() => remove.mutate()}
          disabled={isSelf || remove.isPending}
          title={isSelf ? t("users.cantDeleteSelf") : undefined}
        >
          {t("users.delete")}
        </button>
      </div>
      {resetting && (
        <form
          className="flex gap-3 items-end mt-3"
          onSubmit={(e) => {
            e.preventDefault();
            resetPassword.mutate();
          }}
        >
          <div className="flex-1 min-w-52">
            <label>{t("users.newPasswordLabel")}</label>
            <input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} minLength={8} required autoFocus />
          </div>
          <button className="btn sm primary" disabled={resetPassword.isPending}>
            {t("users.setPassword")}
          </button>
        </form>
      )}
      {error && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(error as Error).message}</p>}
    </div>
  );
}
