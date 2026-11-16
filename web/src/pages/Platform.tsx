import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Route, Routes } from "react-router";
import {
  api, del, patch, post, put,
  type Account, type Organization, type Principal, type Setting, type WaitlistCode,
} from "../api";
import Organizations from "./Organizations";
import PlatformHeader from "./platform/Header";
import Mail from "./platform/Mail";
import Push from "./platform/Push";

// The platform panel: the installation, not an organisation inside it.
//
// Four pages, and the dividing line to the administration panel is always the
// same question: does this apply to ALL tenants or only to the one I am
// working in right now? Tenants, accounts, flags and waitlist codes apply to
// all — that is why they stand here and behind s.platformAdmin, not behind an
// organisation role (FR-003, finding F).
export default function Platform({ me }: { me: Principal }) {
  return (
    <Routes>
      <Route index element={<Organizations me={me} />} />
      <Route path="accounts" element={<Accounts me={me} />} />
      <Route path="settings" element={<Settings />} />
      <Route path="mail" element={<Mail />} />
      <Route path="push" element={<Push />} />
      <Route path="waitlist" element={<Waitlist />} />
    </Routes>
  );
}

// --- Accounts ---

function Accounts({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const accounts = useQuery({ queryKey: ["platform", "accounts"], queryFn: () => api<Account[]>("/platform/accounts") });
  const orgs = useQuery({ queryKey: ["orgs"], queryFn: () => api<Organization[]>("/platform/orgs") });
  /* A list to scan and a search over it; one account open at a time (#743).
     Every row used to carry two selects and two delete buttons that acted
     on the first click — one wrong pick changed what someone may do in an
     organisation, or across the whole installation. */
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const q = query.trim().toLowerCase();
  const list = (accounts.data ?? []).filter(
    (a) =>
      !q ||
      a.email.toLowerCase().includes(q) ||
      (a.display_name ?? "").toLowerCase().includes(q) ||
      a.seats.some((s) => s.org_name.toLowerCase().includes(q)),
  );

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>{t("platform.accountsDesc")}</p>
      {(accounts.data?.length ?? 0) > 0 && (
        <div className="flex items-center gap-3 mb-3 flex-wrap">
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("platform.accountsSearch")}
            aria-label={t("platform.accountsSearch")}
            style={{ maxWidth: 320 }}
          />
          <span className="muted text-xs">{t("platform.accountsCount", { count: list.length })}</span>
        </div>
      )}
      {list.length > 0 && (
        <div className="card member-list">
          {list.map((a) => (
            <AccountRow
              key={a.id}
              account={a}
              isSelf={a.id === me.AccountID}
              orgs={orgs.data ?? []}
              open={open === a.id}
              onToggle={() => setOpen(open === a.id ? null : a.id)}
            />
          ))}
        </div>
      )}
      {accounts.data?.length === 0 && <p className="muted text-xs">{t("platform.noAccounts")}</p>}
    </div>
  );
}

/* The roles a seat can carry — the same set the API accepts (validRoles). */
const ORG_ROLES = ["org_admin", "agent_owner", "security", "auditor", "controlling"];

function AccountRow({
  account,
  isSelf,
  orgs,
  open,
  onToggle,
}: {
  account: Account;
  isSelf: boolean;
  orgs: Organization[];
  open: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  const admin = account.platform_role === "system_admin";
  return (
    <div className={`account${open ? " open" : ""}`}>
      <button type="button" className="member-row member-link account-head" onClick={onToggle} aria-expanded={open}>
        <div className="flex-1 min-w-0 text-left">
          <div className="text-sm font-medium truncate">
            {account.display_name || account.email}
            {isSelf && <span className="muted text-xs"> {t("platform.you")}</span>}
          </div>
          <div className="muted text-xs truncate">{account.email}</div>
        </div>
        <div className="account-chips">
          {admin && <span className="ntag">{t("platform.roleSystemAdmin")}</span>}
          {!account.email_verified_at && <span className="badge">{t("platform.unverified")}</span>}
          {account.seats.length === 0 ? (
            <span className="muted text-xs">{t("platform.noSeat")}</span>
          ) : (
            account.seats.map((s) => (
              <span key={s.org_id} className="account-seat">
                {s.org_name} · {t(`role.${s.role}`, s.role)}
              </span>
            ))
          )}
        </div>
        <span className="muted text-xs account-last">
          {account.last_login_at
            ? t("platform.lastLogin", { date: new Date(account.last_login_at).toLocaleDateString() })
            : t("platform.neverSignedIn")}
        </span>
        <span className="member-chev" aria-hidden="true">{open ? "⌄" : "›"}</span>
      </button>
      {open && <AccountDetail account={account} isSelf={isSelf} orgs={orgs} />}
    </div>
  );
}

function AccountDetail({ account, isSelf, orgs }: { account: Account; isSelf: boolean; orgs: Organization[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  /* Seats (#262). The counts on the organisations page change with them, and
     one's own seats are also what the org switcher and the session show. */
  const changed = () => {
    void qc.invalidateQueries({ queryKey: ["platform", "accounts"] });
    void qc.invalidateQueries({ queryKey: ["orgs"] });
    if (isSelf) {
      void qc.invalidateQueries({ queryKey: ["memberships"] });
      void qc.invalidateQueries({ queryKey: ["me"] });
    }
  };
  // Which step is open: the instance level, one seat's role or removal, adding a seat, deleting.
  const [step, setStep] = useState("");
  const [level, setLevel] = useState(account.platform_role);
  const [seatRole, setSeatRole] = useState("");
  const [addOrg, setAddOrg] = useState("");
  const [addRole, setAddRole] = useState("agent_owner");
  const done = () => {
    setStep("");
    changed();
  };

  const setPlatformRole = useMutation({
    mutationFn: () => patch(`/platform/accounts/${account.id}`, { platform_role: level }),
    onSuccess: done,
  });
  const seat = useMutation({
    mutationFn: (op: { org: string; role?: string }) =>
      op.role
        ? patch(`/platform/orgs/${op.org}/members/${account.id}`, { role: op.role })
        : del(`/platform/orgs/${op.org}/members/${account.id}`),
    onSuccess: done,
  });
  const add = useMutation({
    mutationFn: () => post(`/platform/orgs/${addOrg}/members`, { account_id: account.id, role: addRole }),
    onSuccess: () => {
      setAddOrg("");
      done();
    },
  });
  /* Deleting the login with every seat (#732). The server refuses one's own
     account, the last org admin of an organisation and the last system admin;
     the button is not offered for one's own row. */
  const remove = useMutation({
    mutationFn: () => del(`/platform/accounts/${account.id}`),
    onSuccess: changed,
  });
  const free = orgs.filter((o) => !account.seats.some((s) => s.org_id === o.id));
  const error = (setPlatformRole.error ?? seat.error ?? add.error ?? remove.error) as Error | null;
  const cancel = () => setStep("");

  return (
    <div className="account-detail">
      <div className="text-xs font-medium mb-1">{t("platform.detailOrgs")}</div>
      {account.seats.length === 0 && <p className="muted text-xs mt-0">{t("platform.noSeat")}</p>}
      {account.seats.map((s) => (
        <div key={s.org_id} className="access-row text-xs">
          <span className="flex-1 min-w-0">
            <span className="font-medium">{s.org_name}</span>
            <span className="muted"> · {t(`role.${s.role}`, s.role)}</span>
          </span>
          {step === `role:${s.org_id}` ? (
            <form
              className="flex gap-2 items-center"
              onSubmit={(e) => {
                e.preventDefault();
                seat.mutate({ org: s.org_id, role: seatRole });
              }}
            >
              <select value={seatRole} onChange={(e) => setSeatRole(e.target.value)} style={{ width: 170 }} autoFocus>
                {ORG_ROLES.map((r) => (
                  <option key={r} value={r}>{t(`role.${r}`, r)}</option>
                ))}
              </select>
              <button className="btn sm primary" disabled={seatRole === s.role || seat.isPending}>{t("platform.save")}</button>
              <button type="button" className="btn sm" onClick={cancel}>{t("users.cancel")}</button>
            </form>
          ) : step === `remove:${s.org_id}` ? (
            <span className="flex gap-2 items-center">
              <span>{t("platform.seatRemoveConfirm", { org: s.org_name })}</span>
              <button className="btn sm danger" onClick={() => seat.mutate({ org: s.org_id })} disabled={seat.isPending}>
                {t("platform.seatRemove")}
              </button>
              <button className="btn sm" onClick={cancel}>{t("users.cancel")}</button>
            </span>
          ) : (
            <span className="flex gap-2">
              <button
                className="btn sm"
                onClick={() => {
                  setSeatRole(s.role);
                  setStep(`role:${s.org_id}`);
                }}
              >
                {t("person.roleChange")}
              </button>
              <button className="btn sm" onClick={() => setStep(`remove:${s.org_id}`)}>
                {t("platform.seatRemove")}
              </button>
            </span>
          )}
        </div>
      ))}
      {free.length > 0 &&
        (step === "add" ? (
          <form
            className="access-row text-xs flex-wrap"
            onSubmit={(e) => {
              e.preventDefault();
              add.mutate();
            }}
          >
            <select value={addOrg} onChange={(e) => setAddOrg(e.target.value)} className="flex-1 min-w-44" autoFocus>
              <option value="">{t("platform.seatAddPick")}</option>
              {free.map((o) => (
                <option key={o.id} value={o.id}>{o.name}</option>
              ))}
            </select>
            <select value={addRole} onChange={(e) => setAddRole(e.target.value)} style={{ width: 170 }}>
              {ORG_ROLES.map((r) => (
                <option key={r} value={r}>{t(`role.${r}`, r)}</option>
              ))}
            </select>
            <button className="btn sm primary" disabled={!addOrg || add.isPending}>{t("platform.seatAdd")}</button>
            <button type="button" className="btn sm" onClick={cancel}>{t("users.cancel")}</button>
          </form>
        ) : (
          <button className="btn sm mt-1" onClick={() => setStep("add")}>{t("platform.seatAddButton")}</button>
        ))}

      <div className="text-xs font-medium mt-4 mb-1">{t("platform.detailInstance")}</div>
      <div className="access-row text-xs">
        <span className="flex-1 min-w-0">
          <span className="font-medium">
            {account.platform_role === "system_admin" ? t("platform.roleSystemAdmin") : t("platform.roleUser")}
          </span>
          <span className="muted block">{t("platform.levelHint")}</span>
        </span>
        {step === "level" ? (
          <form
            className="flex gap-2 items-center"
            onSubmit={(e) => {
              e.preventDefault();
              setPlatformRole.mutate();
            }}
          >
            <select value={level} onChange={(e) => setLevel(e.target.value)} style={{ width: 170 }} autoFocus>
              <option value="user">{t("platform.roleUser")}</option>
              <option value="system_admin">{t("platform.roleSystemAdmin")}</option>
            </select>
            <button className="btn sm primary" disabled={level === account.platform_role || setPlatformRole.isPending}>
              {t("platform.save")}
            </button>
            <button type="button" className="btn sm" onClick={cancel}>{t("users.cancel")}</button>
          </form>
        ) : (
          !isSelf && (
            <button
              className="btn sm"
              onClick={() => {
                setLevel(account.platform_role);
                setStep("level");
              }}
            >
              {t("person.roleChange")}
            </button>
          )
        )}
      </div>

      {!isSelf && (
        <div className="access-row text-xs mt-2">
          <span className="flex-1 min-w-0 muted">{t("platform.deleteHint")}</span>
          {step === "delete" ? (
            <span className="flex gap-2 items-center">
              <span>{t("platform.deleteAccountConfirm", { email: account.email, count: account.seats.length })}</span>
              <button className="btn sm danger" onClick={() => remove.mutate()} disabled={remove.isPending}>
                {t("platform.deleteAccount")}
              </button>
              <button className="btn sm" onClick={cancel}>{t("users.cancel")}</button>
            </span>
          ) : (
            <button className="btn sm danger" onClick={() => setStep("delete")}>
              {t("platform.deleteAccount")}
            </button>
          )}
        </div>
      )}
      {error && <p className="danger-text text-xs mt-2 mb-0">{error.message}</p>}
    </div>
  );
}

// --- Flags of the installation ---

function Settings() {
  const { t } = useTranslation();
  const settings = useQuery({ queryKey: ["platform", "settings"], queryFn: () => api<Setting[]>("/platform/settings") });

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>{t("platform.settingsDesc")}</p>
      {/* mail.* has a page of its own: seven fields that only make sense
          together, plus the flag that proves them. Here they would stand as
          seven independent lines — and the password as an eighth. push.*
          likewise (#431): its empty values mean "the environment decides",
          which only its own page can show. */}
      {(settings.data ?? [])
        .filter((s) => !s.key.startsWith("mail.") && !s.key.startsWith("push."))
        .map((s) => (
          <SettingRow key={s.key} setting={s} />
        ))}
    </div>
  );
}

/** The choice values a flag knows. Stands here and not in the backend,
 *  because it is a matter of presentation: the API checks the same values
 *  once more (settings.validate), otherwise a call without a surface would
 *  go unchecked. */
const CHOICES: Record<string, string[]> = {
  "signup.mode": ["off", "waitlist", "open"],
  "notify.decision": ["on", "off"],
  "notify.task": ["on", "off"],
  "notify.cost": ["on", "off"],
  "notify.ops": ["on", "off"],
  "telemetry.mode": ["on", "off"],
};

function SettingRow({ setting }: { setting: Setting }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [value, setValue] = useState(setting.value);
  const save = useMutation({
    mutationFn: (v: string) => put(`/platform/settings/${setting.key}`, { value: v }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform", "settings"] }),
  });
  const choices = CHOICES[setting.key];
  const changed = setting.value !== setting.default;

  return (
    <div className="card mb-2" style={{ padding: "11px 15px" }}>
      <div className="flex items-center gap-4 flex-wrap">
        <div className="flex-1 min-w-52">
          <div className="text-sm font-medium mono">{setting.key}</div>
          <div className="muted text-xs">{t(`platform.setting.${setting.key}`, "")}</div>
        </div>
        {choices ? (
          <select
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
              save.mutate(e.target.value);
            }}
            disabled={save.isPending}
            style={{ width: 170 }}
          >
            {choices.map((c) => (
              <option key={c} value={c}>
                {/* A value that several flags share (on/off) has its text once
                    under platform.choice and not per flag. */}
                {t([`platform.choice.${setting.key}.${c}`, `platform.choice.${c}`], c)}
              </option>
            ))}
          </select>
        ) : (
          <form
            className="flex gap-2 items-center"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate(value);
            }}
          >
            <input value={value} onChange={(e) => setValue(e.target.value)} style={{ width: 200 }} />
            <button className="btn sm primary" disabled={save.isPending || value === setting.value}>
              {t("platform.save")}
            </button>
          </form>
        )}
        <span className="muted text-xs" style={{ minWidth: 110 }}>
          {changed ? t("platform.default", { value: setting.default }) : t("platform.unchanged")}
        </span>
      </div>
      {save.isError && (
        <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(save.error as Error).message}</p>
      )}
    </div>
  );
}

// --- Waitlist codes ---

function Waitlist() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const codes = useQuery({ queryKey: ["platform", "waitlist"], queryFn: () => api<WaitlistCode[]>("/platform/waitlist-codes") });
  const orgs = useQuery({ queryKey: ["orgs"], queryFn: () => api<Organization[]>("/platform/orgs") });
  const invalidate = () => qc.invalidateQueries({ queryKey: ["platform", "waitlist"] });

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>{t("platform.waitlistDesc")}</p>
      <CreateCode orgs={orgs.data ?? []} onDone={invalidate} />
      {(codes.data ?? []).map((c) => (
        <CodeRow key={c.hash} code={c} orgs={orgs.data ?? []} onChanged={invalidate} />
      ))}
      {codes.data?.length === 0 && <p className="muted text-xs">{t("platform.noCodes")}</p>}
    </div>
  );
}

function CreateCode({ orgs, onDone }: { orgs: Organization[]; onDone: () => void }) {
  const { t } = useTranslation();
  const [label, setLabel] = useState("");
  const [maxUses, setMaxUses] = useState(1);
  const [orgID, setOrgID] = useState("");
  const [pattern, setPattern] = useState("");
  const [expires, setExpires] = useState("");
  /* The generated code stands exactly once here — the database only knows its
     hash. That is why it stays until someone clicks it away, instead of
     vanishing after the next render. */
  const [plaintext, setPlaintext] = useState("");

  const mut = useMutation({
    mutationFn: () =>
      post<{ code: string }>("/platform/waitlist-codes", {
        label,
        max_uses: maxUses,
        org_id: orgID,
        email_pattern: pattern,
        // A date without a time is valid until the end of the day.
        expires_at: expires ? new Date(`${expires}T23:59:59`).toISOString() : "",
      }),
    onSuccess: (data) => {
      setPlaintext(data.code);
      setLabel("");
      setPattern("");
      setExpires("");
      onDone();
    },
  });

  return (
    <>
      <form
        className="card mb-3"
        onSubmit={(e) => {
          e.preventDefault();
          mut.mutate();
        }}
      >
        <div className="flex gap-3 items-end flex-wrap">
          <div className="flex-1 min-w-44">
            <label>{t("platform.codeLabel")}</label>
            <input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t("platform.codeLabelPlaceholder")} required />
          </div>
          <div style={{ width: 90 }}>
            <label>{t("platform.codeMaxUses")}</label>
            <input type="number" min={1} value={maxUses} onChange={(e) => setMaxUses(Number(e.target.value))} />
          </div>
          <div style={{ width: 150 }}>
            <label>{t("platform.codeExpires")}</label>
            <input type="date" value={expires} onChange={(e) => setExpires(e.target.value)} />
          </div>
          <div className="min-w-44">
            <label>{t("platform.codeOrg")}</label>
            <select value={orgID} onChange={(e) => setOrgID(e.target.value)}>
              <option value="">{t("platform.codeOrgNone")}</option>
              {orgs.map((o) => (
                <option key={o.id} value={o.id}>{o.name}</option>
              ))}
            </select>
          </div>
          <div style={{ width: 150 }}>
            <label>{t("platform.codePattern")}</label>
            <input value={pattern} onChange={(e) => setPattern(e.target.value)} placeholder="@firma.de" />
          </div>
          <button className="btn primary" disabled={mut.isPending}>{t("platform.createCode")}</button>
        </div>
        {mut.isError && <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(mut.error as Error).message}</p>}
      </form>

      {plaintext && (
        <div className="card mb-4" style={{ borderColor: "var(--border-accent, var(--border))" }}>
          <div className="flex items-center gap-3 flex-wrap">
            <span className="text-sm">{t("platform.codeCreated")}</span>
            <code className="mono text-sm" style={{ fontSize: 15 }}>{plaintext}</code>
            <button className="btn sm" onClick={() => void navigator.clipboard.writeText(plaintext)}>
              {t("platform.copy")}
            </button>
            <button className="btn sm" onClick={() => setPlaintext("")}>{t("platform.dismiss")}</button>
          </div>
          <p className="muted text-xs mt-2 m-0">{t("platform.codeOnce")}</p>
        </div>
      )}
    </>
  );
}

function CodeRow({ code, orgs, onChanged }: { code: WaitlistCode; orgs: Organization[]; onChanged: () => void }) {
  const { t } = useTranslation();
  const revoke = useMutation({
    mutationFn: () => del(`/platform/waitlist-codes/${code.hash.slice(0, 16)}`),
    onSuccess: onChanged,
  });
  const expired = code.expires_at != null && new Date(code.expires_at) < new Date();
  const open = !code.revoked_at && !expired && code.used_count < code.max_uses;
  const org = orgs.find((o) => o.id === code.org_id);

  return (
    <div className="card mb-2" style={{ padding: "11px 15px" }}>
      <div className="flex items-center gap-4 flex-wrap">
        <div className="flex-1 min-w-52">
          <div className="text-sm font-medium">{code.label || t("platform.codeNoLabel")}</div>
          <div className="muted text-xs">
            {t("platform.codeUses", { used: code.used_count, max: code.max_uses })}
            {code.expires_at && ` · ${t("platform.codeUntil", { date: new Date(code.expires_at).toLocaleDateString() })}`}
            {org && ` · ${t("platform.codeJoins", { org: org.name })}`}
            {code.email_pattern && ` · ${code.email_pattern}`}
          </div>
        </div>
        <span className={`badge ${open ? "st-ok" : ""}`}>
          {code.revoked_at
            ? t("platform.codeRevoked")
            : expired
              ? t("platform.codeExpired")
              : code.used_count >= code.max_uses
                ? t("platform.codeUsedUp")
                : t("platform.codeOpen")}
        </span>
        <button className="btn sm danger" onClick={() => revoke.mutate()} disabled={!open || revoke.isPending}>
          {t("platform.revoke")}
        </button>
      </div>
      {revoke.isError && (
        <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(revoke.error as Error).message}</p>
      )}
    </div>
  );
}
