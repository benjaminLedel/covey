import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post, type MarketplaceEntry, type MarketplaceView } from "../../api";
import { TargetIcon } from "../../components/TargetIcon";
import { ConfirmDialog } from "../../components/Modal";

// The same labels as in the store — one kind is not called differently in two
// places.
const kindKey: Record<MarketplaceEntry["kind"], string> = {
  builtin: "targets.kindBuiltin",
  custom: "targets.kindCustom",
  mcp: "targets.kindMcp",
  wasm: "targets.kindWasm",
};

// The plugin catalogue: target systems that do not ship with the platform but
// come from an index (spec/22).
//
// Two things stand deliberately visible on every card here, because they
// cannot be asked for afterwards: WHO publishes the plugin and WHERE it
// comes from. No one should install a plugin whose origin cannot be
// checked.
//
// Installing always happens only on a click. There is no auto-update path,
// and that is not a missing convenience: a catalogue that pulled versions in
// on its own would be a supply-chain backdoor into every organisation at once.

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
      {/* Origin and age of the catalogue belong visibly at the top: what
          stands here comes from someone else's server. */}
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

      {/* Uninstalling takes the plugin out of THIS organisation — the entry in
          the catalogue stays, and so do the credentials: those belong to the
          target system, not to the plugin. */}
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
          {/* The badge comes embedded from the catalogue (data:-URI, checked
              by the API for allowed image kinds). Missing or unusable,
              `TargetIcon` draws the category symbol — a card without an image
              does not exist. */}
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
          // A compiled plugin cannot be installed; the catalogue lists it so
          // that one finds it, instead of having to guess which of the three
          // kinds it belongs to.
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

      {/* The digest failure is the only error that really counts here: the
          artifact is no longer what the entry points at. It belongs unshort-
          ened to the person who clicked. */}
      {error && <p className="danger-text text-[11px] mt-2 mono">{error}</p>}

      {!builtin && !e.installed && (
        <p className="muted text-[11px] mt-2">{t("catalog.arrivesDisabled")}</p>
      )}
    </article>
  );
}
