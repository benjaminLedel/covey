import { useState } from "react";
import { BirdMark } from "../components/BirdMark";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { api, post, type Agent, type RuntimeInfo, type SetupState } from "../api";
import i18n from "../i18n";

/* Die Einrichtung: der Zugang zuerst, dann darf der Zugang arbeiten.
 *
 * Drei Karten, jede überspringbar. Das ist verantwortbar, weil alles hier auch
 * von Hand geht — Secrets-/Runtime-Seite, Vorlagenbibliothek, das
 * Anlege-Formular. Die Karten kaufen keine Exklusivität, sondern die
 * Reihenfolge: ohne Credential kann nichts von dem laufen, was die Oberfläche
 * anbietet, und mit einem lässt sich das meiste FÜR jemanden erledigen statt
 * VON ihm.
 *
 * Erledigte Karten bleiben sichtbar und abgehakt. Wer zurückkommt, soll sehen,
 * was steht — nicht raten müssen, ob er schon hier war. spec/20. */
export default function Setup() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const state = useQuery({ queryKey: ["setup"], queryFn: () => api<SetupState>("/setup/state") });
  const refresh = () => qc.invalidateQueries({ queryKey: ["setup"] });

  if (!state.data) return null;
  const st = state.data;
  const done = [st.engine_done, st.org_done, st.people_done].filter(Boolean).length;

  return (
    <div className="setup-page">
      {/* Die Kopfleiste liegt auf derselben Spalte wie die Karten: die Marke
          steht über deren linker Kante, der Ausgang über der rechten. Vorher
          lief sie über die volle Breite, und der Knopf schwebte weit rechts
          ohne Bezug zu irgendetwas. */}
      <header className="setup-head">
        <div className="setup-head-in">
          <span className="brand">
            <BirdMark size={26} />
            covey
          </span>
          <span className="ml-auto" />
          <span className="secondary text-xs">{t("setup.progress", { done, total: 3 })}</span>
          {/* Der Weg heraus, immer sichtbar: überspringbar heißt, dass man es
              sieht — nicht, dass man es erraten muss. */}
          <Link className="btn sm" to="/">
            {done === 3 ? t("setup.finish") : t("setup.later")}
          </Link>
        </div>
      </header>

      <div className="setup-body">
        <div className="flex items-baseline gap-3 mb-2">
          <h1 className="text-[22px]">{t("setup.title")}</h1>
          <span className="secondary">{t("setup.subtitle")}</span>
        </div>
        <p className="secondary text-xs mb-4">{t("setup.lead")}</p>

        <EngineCard state={st} onDone={refresh} />
        <OrgCard state={st} onDone={refresh} />
        <PeopleCard state={st} onDone={refresh} />
      </div>
    </div>
  );
}

function Card({
  n,
  title,
  hint,
  done,
  children,
}: {
  n: number;
  title: string;
  hint: string;
  done: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="card mb-3">
      <div className="flex items-baseline gap-2 mb-1">
        <span className={`setup-n${done ? " done" : ""}`}>{done ? "✓" : n}</span>
        <h2 className="text-sm" style={{ fontWeight: 600 }}>{title}</h2>
      </div>
      <p className="muted text-xs mb-3" style={{ marginLeft: 28 }}>{hint}</p>
      <div style={{ marginLeft: 28 }}>{children}</div>
    </div>
  );
}

// --- Karte 1: Motor und Zugang -------------------------------------------
//
// Die Felder kommen aus der Engine-Deklaration, nicht aus einer Liste hier:
// eine neue Engine bringt ihren Einrichtungsschritt selbst mit.

function EngineCard({ state, onDone }: { state: SetupState; onDone: () => void }) {
  const { t } = useTranslation();
  const engines = state.engines.filter(e => (e.credentials ?? []).length > 0);
  const [engine, setEngine] = useState(engines[0]?.name ?? "claude-code");
  const [kind, setKind] = useState("");
  const [value, setValue] = useState("");

  const chosen: RuntimeInfo | undefined = engines.find(e => e.name === engine);
  const creds = chosen?.credentials ?? [];
  const activeKind = kind || creds[0]?.kind || "";
  const cred = creds.find(c => c.kind === activeKind);

  const save = useMutation({
    mutationFn: () => post<{ ok: boolean }>("/setup/engine", { engine, kind: activeKind, value }),
    onSuccess: () => {
      setValue("");
      onDone();
    },
  });

  if (state.engine_done) {
    return (
      <Card n={1} title={t("setup.engine.title")} hint={t("setup.engine.hint")} done>
        <p className="text-xs">{t("setup.engine.done")}</p>
      </Card>
    );
  }

  return (
    <Card n={1} title={t("setup.engine.title")} hint={t("setup.engine.hint")} done={false}>
      <form
        onSubmit={e => { e.preventDefault(); save.mutate(); }}
        style={{ display: "grid", gap: 10 }}
      >
        <div>
          <label>{t("setup.engine.engine")}</label>
          <select value={engine} onChange={e => { setEngine(e.target.value); setKind(""); }}>
            {engines.map(e => (
              <option key={e.name} value={e.name}>{e.label || e.name}</option>
            ))}
          </select>
        </div>
        {creds.length > 1 && (
          <div>
            <label>{t("setup.engine.kind")}</label>
            <select value={activeKind} onChange={e => setKind(e.target.value)}>
              {creds.map(c => (
                <option key={c.kind} value={c.kind}>{c.label}</option>
              ))}
            </select>
          </div>
        )}
        <div>
          <label>{cred?.label ?? t("setup.engine.value")}</label>
          <input
            type="password"
            value={value}
            autoFocus
            onChange={e => setValue(e.target.value)}
            placeholder={cred?.secret}
            className="mono"
          />
          <div className="muted text-xs" style={{ marginTop: 3 }}>
            {t("setup.engine.valueHint", { key: cred?.secret ?? "" })}
          </div>
        </div>
        {save.isError && (
          <div className="danger-text text-xs">{String((save.error as Error)?.message ?? save.error)}</div>
        )}
        <div>
          <button className="btn primary" disabled={!value.trim() || save.isPending}>
            {save.isPending ? t("setup.engine.checking") : t("setup.engine.save")}
          </button>
        </div>
      </form>
    </Card>
  );
}

// --- Karte 2: Was macht dieses Unternehmen -------------------------------

function OrgCard({ state, onDone }: { state: SetupState; onDone: () => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState(state.org_name);
  const [description, setDescription] = useState(state.org_description);

  const save = useMutation({
    mutationFn: () => post<{ ok: boolean }>("/setup/org", { name, description }),
    onSuccess: onDone,
  });

  return (
    <Card n={2} title={t("setup.org.title")} hint={t("setup.org.hint")} done={state.org_done}>
      <form
        onSubmit={e => { e.preventDefault(); save.mutate(); }}
        style={{ display: "grid", gap: 10 }}
      >
        <div>
          <label>{t("setup.org.name")}</label>
          <input value={name} onChange={e => setName(e.target.value)} />
        </div>
        <div>
          <label>{t("setup.org.description")}</label>
          <textarea
            rows={4}
            value={description}
            placeholder={t("setup.org.placeholder")}
            onChange={e => setDescription(e.target.value)}
          />
        </div>
        {save.isError && (
          <div className="danger-text text-xs">{String((save.error as Error)?.message ?? save.error)}</div>
        )}
        <div>
          <button className="btn primary" disabled={save.isPending}>
            {save.isPending ? t("setup.saving") : t("setup.org.save")}
          </button>
        </div>
      </form>
    </Card>
  );
}

// --- Karte 3: Die Personalabteilung --------------------------------------

function PeopleCard({ state, onDone }: { state: SetupState; onDone: () => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [onboard, setOnboard] = useState(true);

  const create = useMutation({
    mutationFn: () => post<{ agent: Agent }>(`/setup/people?lang=${encodeURIComponent(i18n.language)}`, { onboard }),
    onSuccess: res => {
      onDone();
      navigate(`/agents/${res.agent.id}`);
    },
  });

  if (state.people_done) {
    return (
      <Card n={3} title={t("setup.people.title")} hint={t("setup.people.hint")} done>
        <button className="btn sm" onClick={() => navigate(`/agents/${state.people_id}`)}>
          {t("setup.people.open")}
        </button>
      </Card>
    );
  }

  return (
    <Card n={3} title={t("setup.people.title")} hint={t("setup.people.hint")} done={false}>
      <p className="text-xs mb-3">{t("setup.people.lead")}</p>
      {!state.llm_available && (
        <p className="muted text-xs mb-3">{t("setup.people.noLLM")}</p>
      )}
      <label className="text-xs" style={{ display: "block", marginBottom: 10 }}>
        <input type="checkbox" checked={onboard} onChange={e => setOnboard(e.target.checked)} />{" "}
        {t("setup.people.onboard")}
      </label>
      {create.isError && (
        <div className="danger-text text-xs mb-2">
          {String((create.error as Error)?.message ?? create.error)}
        </div>
      )}
      <button className="btn primary" onClick={() => create.mutate()} disabled={create.isPending}>
        {create.isPending ? t("setup.people.creating") : t("setup.people.create")}
      </button>
    </Card>
  );
}
