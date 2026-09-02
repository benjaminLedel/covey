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

// Das Plattform-Panel: die Installation, nicht eine Organisation darin.
//
// Vier Seiten, und die Trennlinie zum Administrations-Panel ist immer
// dieselbe Frage: gilt das für ALLE Mandanten oder nur für den, in dem ich
// gerade arbeite? Mandanten, Konten, Schalter und Wartelisten-Codes gelten für
// alle — deshalb stehen sie hier und hinter s.platformAdmin, nicht hinter einer
// Organisations-Rolle (FR-003, Befund F).
export default function Platform({ me }: { me: Principal }) {
  return (
    <Routes>
      <Route index element={<Organizations me={me} />} />
      <Route path="accounts" element={<Accounts me={me} />} />
      <Route path="settings" element={<Settings />} />
      <Route path="mail" element={<Mail />} />
      <Route path="waitlist" element={<Waitlist />} />
    </Routes>
  );
}

// --- Konten ---

function Accounts({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const accounts = useQuery({ queryKey: ["platform", "accounts"], queryFn: () => api<Account[]>("/platform/accounts") });

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>{t("platform.accountsDesc")}</p>
      {(accounts.data ?? []).map((a) => (
        <AccountRow key={a.id} account={a} isSelf={a.id === me.AccountID} />
      ))}
      {accounts.data?.length === 0 && <p className="muted text-xs">{t("platform.noAccounts")}</p>}
    </div>
  );
}

function AccountRow({ account, isSelf }: { account: Account; isSelf: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const setRole = useMutation({
    mutationFn: (platform_role: string) => patch(`/platform/accounts/${account.id}`, { platform_role }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["platform", "accounts"] }),
  });

  return (
    <div className="card mb-2" style={{ padding: "11px 15px" }}>
      <div className="flex items-center gap-4 flex-wrap">
        <div className="flex-1 min-w-52">
          <div className="text-sm font-medium">
            {account.display_name || account.email}
            {isSelf && <span className="muted text-xs"> {t("platform.you")}</span>}
            {!account.email_verified_at && <span className="badge ml-2">{t("platform.unverified")}</span>}
          </div>
          <div className="muted text-xs mono">{account.email}</div>
          <div className="muted text-xs">
            {account.seats.length === 0
              ? t("platform.noSeat")
              : account.seats.map((s) => `${s.org_name} (${t(`role.${s.role}`, s.role)})`).join(" · ")}
          </div>
        </div>
        <div className="muted text-xs" style={{ minWidth: 130 }}>
          {account.last_login_at
            ? t("platform.lastLogin", { date: new Date(account.last_login_at).toLocaleDateString() })
            : t("platform.neverSignedIn")}
        </div>
        <select
          value={account.platform_role}
          onChange={(e) => setRole.mutate(e.target.value)}
          disabled={setRole.isPending}
          style={{ width: 170 }}
        >
          <option value="user">{t("platform.roleUser")}</option>
          <option value="system_admin">{t("platform.roleSystemAdmin")}</option>
        </select>
      </div>
      {setRole.isError && (
        <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>{(setRole.error as Error).message}</p>
      )}
    </div>
  );
}

// --- Schalter der Installation ---

function Settings() {
  const { t } = useTranslation();
  const settings = useQuery({ queryKey: ["platform", "settings"], queryFn: () => api<Setting[]>("/platform/settings") });

  return (
    <div>
      <PlatformHeader />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>{t("platform.settingsDesc")}</p>
      {/* mail.* hat eine eigene Seite: sieben Felder, die nur zusammen einen
          Sinn ergeben, plus den Knopf, der sie beweist. Hier stuenden sie als
          sieben unabhaengige Zeilen — und das Passwort als achte. */}
      {(settings.data ?? [])
        .filter((s) => !s.key.startsWith("mail."))
        .map((s) => (
          <SettingRow key={s.key} setting={s} />
        ))}
    </div>
  );
}

/** Die Auswahlwerte, die ein Schalter kennt. Steht hier und nicht im Backend,
 *  weil es eine Frage der Darstellung ist: die API prüft dieselben Werte noch
 *  einmal (settings.validate), sonst wäre ein Aufruf ohne Oberfläche
 *  ungeprüft. */
const CHOICES: Record<string, string[]> = {
  "signup.mode": ["off", "waitlist", "open"],
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
                {t(`platform.choice.${setting.key}.${c}`, c)}
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

// --- Wartelisten-Codes ---

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
  /* Der erzeugte Code steht genau einmal hier — die Datenbank kennt nur seinen
     Hash. Deshalb bleibt er stehen, bis jemand ihn wegklickt, statt nach dem
     nächsten Rendern zu verschwinden. */
  const [plaintext, setPlaintext] = useState("");

  const mut = useMutation({
    mutationFn: () =>
      post<{ code: string }>("/platform/waitlist-codes", {
        label,
        max_uses: maxUses,
        org_id: orgID,
        email_pattern: pattern,
        // Ein Datum ohne Uhrzeit gilt bis zum Ende des Tages.
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
