import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type AuditEntry } from "../api";

// Aufsicht → Audit: was MENSCHEN an der Plattform getan haben.
//
// Das Gegenstück zum Recording, das die Agenten zeigt. Beide Hälften zusammen
// ergeben erst Nachvollziehbarkeit: Ohne diese hier könnte jemand eine
// Guard-Rail löschen, den Agenten arbeiten lassen und die Regel wieder
// anlegen — im Recording stünde ein tadelloser Lauf.
//
// Bewusst ohne Request-Inhalte: In ihnen stünden Secret-Werte. Festgehalten
// ist, wer wann was angefasst hat.
/** `embedded` lässt den eigenen Seitenkopf weg — das Administrations-Panel
 *  bringt seinen eigenen mit. Die Zahl der Einträge bleibt: sie ist keine
 *  Überschrift, sondern das Ergebnis der Abfrage. */
export default function Audit({ embedded }: { embedded?: boolean }) {
  const { t } = useTranslation();
  const [nurFehlschlaege, setNurFehlschlaege] = useState(false);
  const [suche, setSuche] = useState("");

  const spur = useQuery({
    queryKey: ["audit"],
    queryFn: () => api<AuditEntry[]>("/audit?limit=300"),
    refetchInterval: 15000,
  });

  const eintraege = (spur.data ?? []).filter((e) => {
    if (nurFehlschlaege && e.status < 400) return false;
    const q = suche.trim().toLowerCase();
    if (!q) return true;
    return (e.path + " " + e.actor_email + " " + e.method).toLowerCase().includes(q);
  });

  return (
    <div>
      <div className="flex items-center gap-3 mb-1 flex-wrap">
        {!embedded && <h1 className="text-[22px]">{t("audit.title")}</h1>}
        {/* Waehrend die Abfrage laeuft, stand hier „0 Eintraege" — bei einer
            Audit-Spur die denkbar schlechteste Falschaussage: ihr Fehlen sieht
            aus wie „nichts ist passiert". */}
        <span className="muted text-sm">
          {spur.isLoading ? t("common.loading") : t("audit.count", { count: eintraege.length })}
        </span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 720 }}>
        {t("audit.lead")}
      </p>

      <div className="card mb-3 flex items-center gap-3 flex-wrap" style={{ padding: "9px 14px" }}>
        <input
          value={suche}
          onChange={(e) => setSuche(e.target.value)}
          placeholder={t("audit.searchPlaceholder")}
          style={{ minWidth: 260 }}
        />
        <label className="flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            checked={nurFehlschlaege}
            onChange={(e) => setNurFehlschlaege(e.target.checked)}
          />
          {t("audit.onlyRejected")}
        </label>
      </div>

      <div className="card" style={{ padding: 0 }}>
        {spur.isError && (
          <p className="danger-text text-xs" style={{ padding: 14 }}>
            {(spur.error as Error).message}
          </p>
        )}
        {spur.data && eintraege.length === 0 && (
          <p className="muted text-sm" style={{ padding: "18px 14px" }}>
            {t("audit.empty")}
          </p>
        )}
        {eintraege.length > 0 && (
          <table className="tbl">
            <thead>
              <tr>
                <th style={{ width: 150 }}>{t("audit.colTime")}</th>
                <th style={{ width: 220 }}>{t("audit.colActor")}</th>
                <th style={{ width: 80 }}>{t("audit.colMethod")}</th>
                <th>{t("audit.colPath")}</th>
                <th style={{ width: 70 }}>{t("audit.colStatus")}</th>
              </tr>
            </thead>
            <tbody>
              {eintraege.map((e) => (
                <tr key={e.id}>
                  <td className="muted text-xs">{new Date(e.created_at).toLocaleString()}</td>
                  <td className="text-xs">
                    {e.actor_email}
                    <span className="muted"> · {e.actor_role}</span>
                  </td>
                  <td className="mono text-xs">{e.method}</td>
                  <td
                    className="mono text-xs"
                    style={{ maxWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                    title={e.path}
                  >
                    {e.path.replace("/api/v1", "")}
                  </td>
                  <td className="mono text-xs">
                    <span className={`badge ${e.status >= 400 ? "st-failed" : "st-done"}`}>{e.status}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
