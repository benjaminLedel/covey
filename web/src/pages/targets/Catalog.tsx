import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post, type MarketplaceEntry, type MarketplaceView } from "../../api";
import { TargetIcon } from "../../components/TargetIcon";
import { ConfirmDialog } from "../../components/Modal";

// Dieselben Bezeichnungen wie im Store — eine Art heisst nicht an zwei
// Stellen verschieden.
const kindKey: Record<MarketplaceEntry["kind"], string> = {
  builtin: "targets.kindBuiltin",
  custom: "targets.kindCustom",
  mcp: "targets.kindMcp",
  wasm: "targets.kindWasm",
};

// Der Plugin-Katalog: Zielsysteme, die nicht mitgeliefert werden, sondern aus
// einem Index kommen (spec/22).
//
// Zwei Dinge stehen hier bewusst sichtbar auf jeder Karte, weil man sie nicht
// im Nachhinein erfragen kann: WER das Plugin veröffentlicht und WOHER es
// kommt. Ein Plugin, dessen Herkunft man nicht prüfen kann, sollte niemand
// installieren.
//
// Installiert wird immer nur auf Klick. Es gibt keinen Auto-Update-Pfad, und
// das ist keine fehlende Bequemlichkeit: ein Katalog, der Fassungen von selbst
// nachzöge, wäre eine Lieferketten-Hintertür in jede Organisation gleichzeitig.

function hostOf(url?: string): string {
  if (!url) return "";
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

export function CatalogTab({ canEdit, query }: { canEdit: boolean; query: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [failed, setFailed] = useState<Record<string, string>>({});

  const market = useQuery({
    queryKey: ["marketplace"],
    queryFn: () => api<MarketplaceView>("/marketplace"),
  });

  const [confirmRemove, setConfirmRemove] = useState<MarketplaceEntry | null>(null);

  const remove = useMutation({
    mutationFn: (name: string) => del(`/targets/${name}`),
    onSuccess: () => {
      setConfirmRemove(null);
      qc.invalidateQueries({ queryKey: ["marketplace"] });
      qc.invalidateQueries({ queryKey: ["targets"] });
    },
  });

  const install = useMutation({
    mutationFn: (name: string) => post(`/marketplace/${name}/install`),
    onSuccess: (_data, name) => {
      setFailed((f) => {
        const next = { ...f };
        delete next[name];
        return next;
      });
      qc.invalidateQueries({ queryKey: ["marketplace"] });
      qc.invalidateQueries({ queryKey: ["targets"] });
    },
    onError: (err: Error, name) => setFailed((f) => ({ ...f, [name]: err.message })),
  });

  const view = market.data;
  const entries = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (view?.entries ?? []).filter(
      (e) =>
        !q ||
        e.name.toLowerCase().includes(q) ||
        e.label.toLowerCase().includes(q) ||
        e.description.toLowerCase().includes(q) ||
        e.publisher.toLowerCase().includes(q),
    );
  }, [view, query]);

  if (market.isLoading) return <p className="muted text-sm">{t("catalog.loading")}</p>;

  if (view && !view.enabled) {
    return (
      <div className="card" style={{ padding: 18 }}>
        <p className="text-sm">{t("catalog.disabled")}</p>
        <p className="muted text-xs mt-2" style={{ maxWidth: 560 }}>
          {t("catalog.disabledHint")}
        </p>
      </div>
    );
  }

  return (
    <div>
      {/* Herkunft und Alter des Katalogs gehören sichtbar an den Anfang: was
          hier steht, kommt von einem fremden Server. */}
      <div className="tgt-bar" style={{ alignItems: "baseline" }}>
        <span className="muted text-xs">
          {t("catalog.source")}{" "}
          <span className="mono">{hostOf(view?.source)}</span>
          {view?.fetched_at && (
            <> · {t("catalog.fetched", { time: new Date(view.fetched_at).toLocaleTimeString() })}</>
          )}
        </span>
      </div>

      {view?.error && (
        <p className="danger-text text-xs mb-3">
          {t("catalog.stale")} <span className="mono">{view.error}</span>
        </p>
      )}

      <div className="tgt-grid">
        {entries.map((e) => (
          <CatalogCard
            key={e.name}
            entry={e}
            canEdit={canEdit}
            busy={install.isPending && install.variables === e.name}
            error={failed[e.name]}
            onInstall={() => install.mutate(e.name)}
            onRemove={() => setConfirmRemove(e)}
          />
        ))}
      </div>

      {entries.length === 0 && !market.isLoading && (
        <p className="muted text-sm">{t("catalog.empty")}</p>
      )}

      {/* Deinstallieren nimmt das Plugin aus DIESER Organisation — der Eintrag
          im Katalog bleibt, und die Zugangsdaten bleiben auch: die gehören dem
          Zielsystem, nicht dem Plugin. */}
      {confirmRemove && (
        <ConfirmDialog
          title={t("catalog.removeTitle", { name: confirmRemove.label || confirmRemove.name })}
          confirmLabel={t("catalog.remove")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(confirmRemove.name)}
          onClose={() => setConfirmRemove(null)}
        >
          <p className="text-sm">{t("catalog.removeBody")}</p>
        </ConfirmDialog>
      )}
    </div>
  );
}

function CatalogCard({
  entry: e,
  canEdit,
  busy,
  error,
  onInstall,
  onRemove,
}: {
  entry: MarketplaceEntry;
  canEdit: boolean;
  busy: boolean;
  error?: string;
  onInstall: () => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  const builtin = e.kind === "builtin";

  return (
    <article className="card tgt-card">
      <div className="tgt-head">
        <span className={`tgt-mark${e.icon ? " brand" : ` k-${e.kind}`}`} aria-hidden="true">
          {/* Das Signet kommt eingebettet aus dem Katalog (data:-URI, von der
              API auf erlaubte Bildformen geprüft). Fehlt es oder taugt es
              nicht, zeichnet TargetIcon das Kategorie-Symbol — eine Karte ohne
              Bild gibt es nicht. */}
          {e.icon ? (
            <img src={e.icon} alt="" width={17} height={17} style={{ display: "block" }} />
          ) : (
            <TargetIcon name={e.name} kind={e.kind} category={e.category} size={17} />
          )}
        </span>
        <div className="tgt-id">
          <div className="tgt-name" title={e.label || e.name}>
            {e.label || e.name}
          </div>
          <div className="tgt-slug mono">{e.name}</div>
        </div>
        <span className={`tgt-kind k-${e.kind}`}>{t(kindKey[e.kind])}</span>
      </div>

      <p className="tgt-desc">{e.description || "—"}</p>

      <div className="tgt-meta">
        <span>
          {t("catalog.by")} {e.publisher}
        </span>
        {e.license && <span>{e.license}</span>}
        {e.version && <span className="mono">v{e.version}</span>}
        {builtin && e.builtin_since && <span>{t("catalog.since", { v: e.builtin_since })}</span>}
      </div>

      {e.deprecated && <p className="danger-text text-[11px] mt-2">{e.deprecated}</p>}

      <div className="tgt-foot">
        {builtin ? (
          // Ein kompiliertes Plugin lässt sich nicht installieren; der Katalog
          // führt es, damit man es findet, statt raten zu müssen, welcher der
          // drei Arten es angehört.
          <span className="tgt-kind">{t("catalog.shipped")}</span>
        ) : e.installed ? (
          e.update_available ? (
            <button className="btn sm primary" disabled={!canEdit || busy} onClick={onInstall}>
              {busy ? t("catalog.installing") : t("catalog.update", { v: e.version })}
            </button>
          ) : (
            <span className="tgt-kind">
              {t("catalog.installed", { v: e.installed_version })}
            </span>
          )
        ) : e.installed_elsewhere ? (
          <span className="tgt-kind" title={t("catalog.takenHint")}>
            {t("catalog.taken")}
          </span>
        ) : (
          <button className="btn sm" disabled={!canEdit || busy} onClick={onInstall}>
            {busy ? t("catalog.installing") : t("catalog.install")}
          </button>
        )}

        {e.homepage && (
          <a className="btn sm" href={e.homepage} target="_blank" rel="noreferrer noopener">
            {t("catalog.source2")}
          </a>
        )}

        {e.installed && canEdit && (
          <button className="btn sm danger" onClick={onRemove}>
            {t("catalog.remove")}
          </button>
        )}
      </div>

      {/* Der Digest-Fehlschlag ist der einzige Fehler, der hier wirklich zählt:
          das Artefakt ist nicht mehr das, worauf der Eintrag zeigt. Er gehört
          unverkürzt an den Menschen, der geklickt hat. */}
      {error && <p className="danger-text text-[11px] mt-2 mono">{error}</p>}

      {!builtin && !e.installed && (
        <p className="muted text-[11px] mt-2">{t("catalog.arrivesDisabled")}</p>
      )}
    </article>
  );
}
