import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, istSystemAdmin, post, put, ROLES, type Principal } from "../api";

// Single sign-on for the organisation (#723): the provider, the e-mail
// domains it speaks for (proven by a DNS record), and whether it is required.
// The org admin's alone; nobody else sees the panel.

type SSOConfig = {
  issuer: string;
  client_id: string;
  default_role: string;
  required: boolean;
};

type SSODomain = { domain: string; token: string; record: string; verified_at?: string; verified_method?: string };

type SSOStatus = {
  available: boolean;
  redirect_uri?: string;
  config?: SSOConfig;
  domains?: SSODomain[];
  signed_in_via_sso?: boolean;
};

const empty = { issuer: "", client_id: "", secret: "", default_role: "auditor" };

export default function SSOSettings({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const isAdmin = me.Role === "org_admin";
  const status = useQuery({
    queryKey: ["org-sso"],
    queryFn: () => api<SSOStatus>("/org/sso"),
    enabled: isAdmin,
    retry: false,
  });
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState(empty);
  const [domain, setDomain] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["org-sso"] });

  const save = useMutation({
    mutationFn: () => put<SSOConfig>("/org/sso", form),
    onSuccess: () => {
      setEditing(false);
      setForm(empty);
      refresh();
    },
  });
  const remove = useMutation({ mutationFn: () => del("/org/sso"), onSuccess: refresh });
  const addDomain = useMutation({
    mutationFn: () => post<SSODomain>("/org/sso/domains", { domain }),
    onSuccess: () => {
      setDomain("");
      refresh();
    },
  });
  const verify = useMutation({
    mutationFn: (d: string) => post<SSODomain>(`/org/sso/domains/${encodeURIComponent(d)}/verify`),
    onSuccess: refresh,
  });
  /* An instance system admin may confirm a domain without its DNS record
     (#729); the server checks the same and keeps who did it. */
  const confirmByHand = useMutation({
    mutationFn: (d: string) => post<SSODomain>(`/org/sso/domains/${encodeURIComponent(d)}/confirm`),
    onSuccess: refresh,
  });
  const dropDomain = useMutation({
    mutationFn: (d: string) => del(`/org/sso/domains/${encodeURIComponent(d)}`),
    onSuccess: refresh,
  });
  const required = useMutation({
    mutationFn: (on: boolean) => put<{ rewritten: number }>("/org/sso/required", { required: on }),
    onSuccess: refresh,
  });

  if (!isAdmin) return null;
  const s = status.data;
  if (!s?.available) return null;
  const cfg = s.config;
  const domains = s.domains ?? [];
  const verified = domains.some((d) => d.verified_at);

  const startEdit = () => {
    setForm({
      ...empty,
      issuer: cfg?.issuer ?? "",
      client_id: cfg?.client_id ?? "",
      default_role: cfg?.default_role ?? "auditor",
    });
    setEditing(true);
  };
  const field = (key: "issuer" | "client_id" | "secret", label: string, hint?: string) => (
    <label className="block text-xs mb-2">
      <span className="block mb-1">{label}</span>
      <input
        className="w-full"
        type={key === "secret" ? "password" : "text"}
        autoComplete="off"
        value={form[key]}
        placeholder={hint}
        onChange={(e) => setForm({ ...form, [key]: e.target.value })}
      />
    </label>
  );

  return (
    <section className="card mb-4" aria-labelledby="sso-title" style={{ padding: 16 }}>
      <h2 id="sso-title" className="text-[16px] mb-1">
        {t("sso.title")}
      </h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("sso.desc")}
      </p>

      {!editing && !cfg && <p className="text-sm mb-3">{t("sso.none")}</p>}
      {!editing && cfg && (
        <div className="text-sm mb-3">
          <p>{t("sso.configured", { issuer: cfg.issuer })}</p>
          <p className="muted text-xs">{t("sso.roleLine", { role: t(`role.${cfg.default_role}`, cfg.default_role) })}</p>
        </div>
      )}
      {s.redirect_uri && (
        <p className="text-xs mb-3">
          <span className="muted">{t("sso.redirect")} </span>
          <code>{s.redirect_uri}</code>
        </p>
      )}

      {editing && (
        <form
          className="mb-3"
          style={{ maxWidth: 480 }}
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {field("issuer", t("sso.fIssuer"), "https://login.example.org/realms/acme")}
          {field("client_id", t("sso.fClientID"))}
          {field("secret", t("sso.fSecret"), cfg ? t("sso.keepSecret") : undefined)}
          <label className="block text-xs mb-3">
            <span className="block mb-1">{t("sso.fRole")}</span>
            <select value={form.default_role} onChange={(e) => setForm({ ...form, default_role: e.target.value })}>
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {t(`role.${r}`, r)}
                </option>
              ))}
            </select>
          </label>
          <div className="flex gap-2">
            <button className="btn sm" type="submit" disabled={save.isPending}>
              {save.isPending ? t("sso.checking") : t("sso.save")}
            </button>
            <button className="btn sm ghost" type="button" onClick={() => setEditing(false)}>
              {t("sso.cancel")}
            </button>
          </div>
          {save.error && <p className="err text-xs mt-2">{(save.error as Error).message}</p>}
        </form>
      )}

      {!editing && (
        <div className="flex gap-2 flex-wrap mb-4">
          <button className="btn sm" onClick={startEdit}>
            {cfg ? t("sso.edit") : t("sso.setUp")}
          </button>
          {cfg && (
            <button className="btn sm ghost" disabled={remove.isPending} onClick={() => remove.mutate()}>
              {t("sso.remove")}
            </button>
          )}
        </div>
      )}

      <h3 className="text-sm mb-1">{t("sso.domains")}</h3>
      <p className="muted text-xs mb-2" style={{ maxWidth: 640 }}>
        {t("sso.domainsDesc")}
      </p>
      {domains.length > 0 && (
        <table className="tbl text-sm mb-2">
          <tbody>
            {domains.map((d) => (
              <tr key={d.domain}>
                <td>{d.domain}</td>
                <td className="text-xs">
                  {d.verified_at ? (
                    <span>{d.verified_method === "manual" ? t("sso.verifiedByHand") : t("sso.verified")}</span>
                  ) : (
                    <span>
                      {t("sso.txtHint")} <code>{d.record}</code> = <code>{d.token}</code>
                    </span>
                  )}
                </td>
                <td className="whitespace-nowrap">
                  {!d.verified_at && (
                    <button className="btn sm" disabled={verify.isPending} onClick={() => verify.mutate(d.domain)}>
                      {t("sso.verify")}
                    </button>
                  )}{" "}
                  {!d.verified_at && istSystemAdmin(me) && (
                    <>
                      <button
                        className="btn sm ghost"
                        disabled={confirmByHand.isPending}
                        onClick={() => {
                          if (window.confirm(t("sso.confirmByHandAsk", { domain: d.domain }))) confirmByHand.mutate(d.domain);
                        }}
                      >
                        {t("sso.confirmByHand")}
                      </button>{" "}
                    </>
                  )}
                  <button
                    className="btn sm ghost"
                    disabled={dropDomain.isPending}
                    onClick={() => dropDomain.mutate(d.domain)}
                  >
                    {t("sso.removeDomain")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {verify.error && <p className="err text-xs mb-2">{(verify.error as Error).message}</p>}
      {confirmByHand.error && <p className="err text-xs mb-2">{(confirmByHand.error as Error).message}</p>}
      <form
        className="flex gap-2 mb-4"
        style={{ maxWidth: 480 }}
        onSubmit={(e) => {
          e.preventDefault();
          addDomain.mutate();
        }}
      >
        <input
          className="flex-1"
          aria-label={t("sso.fDomain")}
          placeholder="example.org"
          value={domain}
          onChange={(e) => setDomain(e.target.value)}
        />
        <button className="btn sm" type="submit" disabled={!domain.trim() || addDomain.isPending}>
          {t("sso.addDomain")}
        </button>
      </form>
      {addDomain.error && <p className="err text-xs mb-2">{(addDomain.error as Error).message}</p>}

      {cfg && (
        <>
          <h3 className="text-sm mb-1">{t("sso.requiredTitle")}</h3>
          <p className="muted text-xs mb-2" style={{ maxWidth: 640 }}>
            {t("sso.requiredDesc")}
          </p>
          {cfg.required ? (
            <button className="btn sm ghost" disabled={required.isPending} onClick={() => required.mutate(false)}>
              {t("sso.requiredOff")}
            </button>
          ) : (
            <>
              {!s.signed_in_via_sso && <p className="text-xs mb-2">{t("sso.signInFirst")}</p>}
              <button
                className="btn sm"
                disabled={required.isPending || !s.signed_in_via_sso || !verified}
                onClick={() => {
                  if (window.confirm(t("sso.requiredConfirm"))) required.mutate(true);
                }}
              >
                {t("sso.requiredOn")}
              </button>
            </>
          )}
          {required.data && required.data.rewritten > 0 && (
            <p className="text-xs mt-2">{t("sso.rewritten", { count: required.data.rewritten })}</p>
          )}
          {required.error && <p className="err text-xs mt-2">{(required.error as Error).message}</p>}
        </>
      )}
    </section>
  );
}
