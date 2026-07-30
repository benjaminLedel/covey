import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, NavLink, Route, Routes, useNavigate, useParams } from "react-router";
import {
  api, del, post,
  type EgressBuiltin, type EgressStats, type EgressStatus, type EgressTemplate, type Principal,
} from "../api";
import { AddHostForm, EgressLogTable, HostChips } from "../components/EgressBits";
import { ConfirmDialog, Modal } from "../components/Modal";

// Egress-Bereich mit Subseiten: Übersicht (Status + Monitoring) und Templates
// (Liste + Detailseite je Template). Die Zuweisung geschieht pro Agent im
// Egress-Reiter der Agenten-Seite; der Proxy erzwingt die effektive Allowlist
// fail-closed.
export default function Egress({ me }: { me: Principal }) {
  const canEdit = me.Role === "platform_admin" || me.Role === "security";
  return (
    <Routes>
      <Route index element={<Overview canEdit={canEdit} />} />
      <Route path="templates" element={<TemplatesPage canEdit={canEdit} />} />
      <Route path="templates/:id" element={<TemplateDetail canEdit={canEdit} />} />
    </Routes>
  );
}

function Header() {
  const { t } = useTranslation();
  return (
    <>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">{t("egress.title")}</h1>
        <span className="muted">{t("egress.subtitle")}</span>
      </div>
      <nav className="subnav">
        <NavLink to="/egress" end className={({ isActive }) => (isActive ? "active" : "")}>
          {t("egress.overview")}
        </NavLink>
        <NavLink to="/egress/templates" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("egress.templates")}
        </NavLink>
      </nav>
    </>
  );
}

function Overview({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation();
  const status = useQuery({ queryKey: ["egress", "status"], queryFn: () => api<EgressStatus>("/egress") });
  const stats = useQuery({
    queryKey: ["egress", "stats"],
    queryFn: () => api<EgressStats>("/egress/stats"),
    refetchInterval: 30000,
  });
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });

  return (
    <div>
      <Header />

      <HowItWorks />

      {status.data && <StatusBanner status={status.data} />}

      {status.data && <DefaultsCard status={status.data} canEdit={canEdit} />}

      <div className="stat-grid mb-6">
        <div className="card stat">
          <div className="v" style={{ color: "var(--text-success)" }}>{stats.data?.allowed_24h ?? "–"}</div>
          <div className="l">{t("egress.allowed24h")}</div>
        </div>
        <div className="card stat">
          <div className="v" style={{ color: stats.data?.blocked_24h ? "var(--text-danger)" : undefined }}>
            {stats.data?.blocked_24h ?? "–"}
          </div>
          <div className="l">{t("egress.blocked24h")}</div>
        </div>
        <div className="card stat">
          <div className="v">{templates.data?.length ?? "–"}</div>
          <div className="l">
            <Link to="/egress/templates" style={{ color: "inherit" }}>{t("egress.templates")}</Link>
          </div>
        </div>
        {(stats.data?.top_blocked.length ?? 0) > 0 && (
          <div className="card stat" style={{ gridColumn: "span 2" }}>
            <div className="l" style={{ marginTop: 0, marginBottom: 6 }}>{t("egress.topBlocked")}</div>
            <div className="flex flex-wrap gap-1">
              {stats.data!.top_blocked.map((entry) => (
                <span key={entry.host} className="chip" title={t("egress.blockedCount", { count: entry.count })}>
                  {entry.host}
                  <span className="src">{entry.count}×</span>
                </span>
              ))}
            </div>
          </div>
        )}
      </div>

      <section>
        <h2 className="text-base font-medium mb-2">{t("egress.monitoring")}</h2>
        <p className="muted text-xs mb-3" style={{ maxWidth: 620 }}>
          {t("egress.monitoringDesc")}
        </p>
        <EgressLogTable withAgentFilter />
      </section>
    </div>
  );
}

function HowItWorks() {
  const { t } = useTranslation();
  const [hidden, setHidden] = useState(() => localStorage.getItem("covey.egress.howto") === "1");
  if (hidden) return null;
  return (
    <div className="mb-5">
      <div className="steps">
        <div className="card step">
          <span className="n">1</span>
          <span>
            <b>{t("egress.step1title")}</b>
            <p>{t("egress.step1body")}</p>
          </span>
        </div>
        <div className="card step">
          <span className="n">2</span>
          <span>
            <b>{t("egress.step2title")}</b>
            <p>{t("egress.step2body")}</p>
          </span>
        </div>
        <div className="card step">
          <span className="n">3</span>
          <span>
            <b>{t("egress.step3title")}</b>
            <p>{t("egress.step3body")}</p>
          </span>
        </div>
      </div>
      <button
        className="btn sm mt-2"
        style={{ border: "none", color: "var(--text-muted)", padding: "2px 4px" }}
        onClick={() => { localStorage.setItem("covey.egress.howto", "1"); setHidden(true); }}
      >
        {t("egress.howToHide")}
      </button>
    </div>
  );
}

function StatusBanner({ status }: { status: EgressStatus }) {
  const { t } = useTranslation();
  return (
    <div
      className="card mb-4 text-xs"
      style={{
        padding: "10px 14px",
        borderLeft: `3px solid ${status.enforced ? "var(--text-success)" : "var(--text-warning)"}`,
      }}
    >
      <span>{status.enforced ? t("egress.enforced") : t("egress.notEnforced")}</span>
    </div>
  );
}

function DefaultsCard({ status, canEdit }: { status: EgressStatus; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const invalidate = () => qc.invalidateQueries({ queryKey: ["egress"] });
  const delHost = useMutation({ mutationFn: (id: string) => del(`/egress/defaults/${id}`), onSuccess: invalidate });
  // Auf Host-Grenze prüfen, nicht auf Endung: "boese-anthropic.com" ist ein
  // fremder Host und darf den Hinweis auf die fehlende LLM-Freigabe nicht
  // verschlucken. Muster können ein Wildcard-Präfix und einen Port tragen.
  const isAnthropic = (pattern: string) => {
    const host = pattern.trim().toLowerCase().replace(/^\*\./, "").split(":")[0];
    return host === "anthropic.com" || host.endsWith(".anthropic.com");
  };
  const hasLLM = status.defaults.some((h) => isAnthropic(h.pattern));

  return (
    <div className="card mb-4" style={{ padding: "13px 15px" }}>
      <p className="text-xs font-medium" style={{ margin: "0 0 4px" }}>{t("egress.baseAllowlist")}</p>
      <p className="muted text-xs" style={{ margin: "0 0 8px", maxWidth: 620 }}>
        {t("egress.baseAllowlistDesc")}
      </p>
      <div className="flex flex-wrap gap-1 mb-2">
        <HostChips
          hosts={status.defaults}
          canEdit={canEdit}
          onDelete={(id) => delHost.mutate(id)}
          emptyText={t("egress.baseEmpty")}
        />
        {status.env.map((p) => (
          <span key={p} className="chip is-fixed" title={t("egress.envChipTitle")}>
            {p}
            <span className="src">ENV</span>
          </span>
        ))}
      </div>
      {canEdit && (
        <div style={{ maxWidth: 560 }}>
          <AddHostForm onAdd={(pattern, note) => post("/egress/defaults", { pattern, note }).then(invalidate)} />
        </div>
      )}
      {!hasLLM && (
        <p className="text-xs mt-2" style={{ color: "var(--text-warning)", margin: "8px 0 0" }}>
          {t("egress.llmHint")}
        </p>
      )}
    </div>
  );
}

function TemplatesPage({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const [showCreate, setShowCreate] = useState(false);

  const list = templates.data ?? [];
  return (
    <div>
      <Header />
      <div className="flex items-start gap-3 mb-4">
        <p className="muted text-xs" style={{ maxWidth: 560, margin: 0 }}>
          {t("egress.catalogDesc")}
        </p>
        {canEdit && (
          <button className="btn primary ml-auto" style={{ flexShrink: 0 }} onClick={() => setShowCreate(true)}>
            {t("egress.newTemplate")}
          </button>
        )}
      </div>

      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}>
        {list.map((tpl) => (
          <Link
            key={tpl.id}
            to={`/egress/templates/${tpl.id}`}
            className="card fade block"
            style={{ padding: "13px 15px", textDecoration: "none", color: "inherit" }}
          >
            <div className="flex items-baseline gap-2 mb-1">
              <span className="font-medium">{tpl.name}</span>
              <span className="muted text-xs ml-auto">
                {tpl.agents.length === 0
                  ? t("egress.notAssigned")
                  : tpl.agents.length === 1
                    ? t("egress.assignedToAgent", { count: 1 })
                    : t("egress.assignedToAgents", { count: tpl.agents.length })}
              </span>
            </div>
            {tpl.description && <p className="muted text-xs mb-2" style={{ margin: "0 0 8px" }}>{tpl.description}</p>}
            <div className="flex flex-wrap gap-1">
              {tpl.hosts.slice(0, 4).map((h) => (
                <span key={h.id} className="chip">{h.pattern}</span>
              ))}
              {tpl.hosts.length > 4 && <span className="muted text-xs" style={{ alignSelf: "center" }}>{t("egress.moreHosts", { count: tpl.hosts.length - 4 })}</span>}
              {tpl.hosts.length === 0 && <span className="muted text-xs">{t("egress.noHosts")}</span>}
            </div>
          </Link>
        ))}
        {list.length === 0 && !templates.isLoading && (
          <p className="muted text-xs">
            {canEdit ? t("egress.noTemplatesHint") : t("egress.noTemplatesAdmin")}
          </p>
        )}
      </div>

      <BuiltinCatalog canEdit={canEdit} />

      {showCreate && (
        <CreateTemplateModal
          onClose={() => setShowCreate(false)}
          onCreated={(id) => { setShowCreate(false); navigate(`/egress/templates/${id}`); }}
        />
      )}
    </div>
  );
}

function BuiltinCatalog({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const builtins = useQuery({ queryKey: ["egress", "builtin"], queryFn: () => api<EgressBuiltin[]>("/egress/builtin") });
  const [err, setErr] = useState<string | null>(null);

  const importTpl = useMutation({
    mutationFn: (slug: string) => post<EgressTemplate>(`/egress/builtin/${slug}`),
    onSuccess: () => { setErr(null); qc.invalidateQueries({ queryKey: ["egress"] }); },
    onError: (e: Error) => setErr(e.message),
  });

  const list = builtins.data ?? [];
  if (list.length === 0) return null;
  return (
    <section className="mt-8">
      <h2 className="text-base font-medium mb-2">{t("egress.catalog")}</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 560 }}>
        {t("egress.catalogDesc")}
      </p>
      {err && <p className="text-xs mb-2" style={{ color: "var(--text-danger)" }}>{err}</p>}
      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}>
        {list.map((b) => (
          <div key={b.slug} className="card" style={{ padding: "13px 15px", opacity: b.imported ? 0.7 : 1 }}>
            <div className="flex items-baseline gap-2 mb-1">
              <span className="font-medium">{b.name}</span>
              <span className="ml-auto" style={{ flexShrink: 0 }}>
                {b.imported ? (
                  b.template_id ? (
                    <Link to={`/egress/templates/${b.template_id}`} className="badge st-done" style={{ textDecoration: "none" }}>
                      {t("egress.adopted")}
                    </Link>
                  ) : (
                    <span className="badge st-done">{t("egress.adopted")}</span>
                  )
                ) : canEdit ? (
                  <button className="btn sm" disabled={importTpl.isPending} onClick={() => importTpl.mutate(b.slug)}>
                    {t("egress.adopt")}
                  </button>
                ) : null}
              </span>
            </div>
            <p className="muted text-xs" style={{ margin: "0 0 8px" }}>{b.description}</p>
            <div className="flex flex-wrap gap-1">
              {b.hosts.map((h) => (
                <span key={h.pattern} className="chip" title={h.note}>{h.pattern}</span>
              ))}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function CreateTemplateModal({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => post<EgressTemplate>("/egress/templates", { name: name.trim(), description: desc.trim() }),
    onSuccess: (result) => { qc.invalidateQueries({ queryKey: ["egress"] }); onCreated(result.id); },
    onError: (e: Error) => setErr(e.message),
  });

  return (
    <Modal
      title={t("egress.newTemplate")}
      onClose={onClose}
      footer={
        <>
          <button className="btn sm" onClick={onClose}>{t("egress.cancel")}</button>
          <button className="btn sm primary" disabled={create.isPending || !name.trim()} onClick={() => create.mutate()}>
            {t("egress.createTemplateCreate")}
          </button>
        </>
      }
    >
      <p className="muted text-xs" style={{ margin: "0 0 12px" }}>
        {t("egress.createTemplateDesc")}
      </p>
      <label>
        {t("egress.createTemplateName")}
        <input
          placeholder="Zammad-Prod"
          value={name}
          autoFocus
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && name.trim() && create.mutate()}
        />
      </label>
      <label style={{ marginTop: 10 }}>
        {t("egress.createTemplateOptDesc")}
        <input
          placeholder="Helpdesk-Produktion + Wissensdatenbank"
          value={desc}
          onChange={(e) => setDesc(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && name.trim() && create.mutate()}
        />
      </label>
      {err && <p className="text-xs mt-2" style={{ color: "var(--text-danger)", margin: "8px 0 0" }}>{err}</p>}
    </Modal>
  );
}

function TemplateDetail({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation();
  const { id } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const invalidate = () => qc.invalidateQueries({ queryKey: ["egress"] });

  const [confirmDelete, setConfirmDelete] = useState(false);
  const remove = useMutation({
    mutationFn: () => del(`/egress/templates/${id}`),
    onSuccess: () => { invalidate(); navigate("/egress/templates"); },
  });
  const delHost = useMutation({ mutationFn: (hid: string) => del(`/egress/template-hosts/${hid}`), onSuccess: invalidate });

  const tpl = (templates.data ?? []).find((item) => item.id === id);
  if (templates.isLoading) return <Header />;
  if (!tpl) {
    return (
      <div>
        <Header />
        <p className="muted text-sm">{t("egress.notFound")}</p>
      </div>
    );
  }

  return (
    <div>
      <Header />
      <p className="text-xs mb-3">
        <Link to="/egress/templates" className="muted" style={{ textDecoration: "none" }}>{t("egress.backToList")}</Link>
      </p>

      <div className="flex items-baseline gap-3 mb-1">
        <h2 className="text-lg font-medium">{tpl.name}</h2>
        {canEdit && (
          <button className="btn sm danger ml-auto" onClick={() => setConfirmDelete(true)}>{t("egress.deleteTemplate")}</button>
        )}
      </div>
      {tpl.description && <p className="muted text-xs mb-4">{tpl.description}</p>}

      <section className="card mb-5" style={{ padding: "14px 16px", maxWidth: 640 }}>
        <p className="text-xs font-medium mb-2" style={{ margin: "0 0 8px" }}>{t("egress.allowedHosts")}</p>
        <div className="flex flex-wrap gap-1 mb-3">
          <HostChips
            hosts={tpl.hosts}
            canEdit={canEdit}
            onDelete={(hid) => delHost.mutate(hid)}
            emptyText={t("egress.emptyHosts")}
          />
        </div>
        {canEdit && (
          <AddHostForm onAdd={(pattern, note) => post(`/egress/templates/${tpl.id}/hosts`, { pattern, note }).then(invalidate)} />
        )}
      </section>

      <section className="mb-5">
        <p className="text-xs font-medium mb-2">{t("egress.usedBy")}</p>
        {tpl.agents.length === 0 ? (
          <p className="muted text-xs">{t("egress.notUsed")}</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {tpl.agents.map((a) => (
              <Link key={a.id} to={`/agents/${a.id}`} className="chip" style={{ textDecoration: "none" }}>
                {a.display_name || a.slug}
                <span className="src">{a.slug}</span>
              </Link>
            ))}
          </div>
        )}
      </section>

      {confirmDelete && (
        <ConfirmDialog
          title={t("egress.deleteTemplateTitle", { name: tpl.name })}
          onClose={() => setConfirmDelete(false)}
          onConfirm={() => remove.mutate()}
          pending={remove.isPending}
        >
          <p style={{ margin: 0 }}>
            {tpl.agents.length > 0
              ? (tpl.agents.length === 1
                  ? t("egress.deleteWithAgentsOne", { count: 1 })
                  : t("egress.deleteWithAgentsOther", { count: tpl.agents.length }))
              : t("egress.deleteNoAgents")}
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}
