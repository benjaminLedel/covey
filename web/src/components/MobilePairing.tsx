import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import { api, post } from "../api";

type Pairing = {
  id: string;
  code: string;
  expires_at: string;
};

type PairingState = {
  id: string;
  expires_at: string;
  redeemed_at: string | null;
  device?: string;
};

/* What the QR code carries (#330, #333): an ordinary https link to this
   instance's /pair. On app.covey.work the phone's own camera opens the app
   with it (the host is claimed, internal/httpapi/applinks.go); anywhere else
   it opens the /pair page, which hands it on. The address is this page's own
   origin: the browser knows under which name the person reaches the
   instance, and a server behind a proxy does not. The app parses both shapes
   (mobile/lib/pairing.dart). */
export const pairingPayload = (origin: string, code: string) =>
  `${origin}/pair?code=${encodeURIComponent(code)}`;

/* The same pairing through the app's own scheme — for the desktop app, which
   has no camera to scan with, and for the /pair page. */
export const appLink = (origin: string, code: string) =>
  `covey://pair?instance=${encodeURIComponent(origin)}&code=${encodeURIComponent(code)}`;

/* Pairing the mobile app by QR code.
 *
 * The code is good for one use and five minutes, and it appears on this
 * screen and nowhere else. While it is shown, the card asks every two seconds
 * whether the app has used it, so it can say "paired with <device>" instead of
 * leaving the person to guess — and the key list below refreshes, because the
 * pairing has just put a key into it. */
export default function MobilePairing() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [pairing, setPairing] = useState<Pairing | null>(null);
  const [qr, setQr] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  const state = useQuery({
    queryKey: ["pairing", pairing?.id],
    queryFn: () => api<PairingState>(`/auth/pairings/${pairing!.id}`),
    enabled: !!pairing,
    refetchInterval: (q) => (q.state.data?.redeemed_at ? false : 2000),
  });

  const paired = !!state.data?.redeemed_at;
  const expired = !!pairing && !paired && now >= Date.parse(pairing.expires_at);

  useEffect(() => {
    if (paired) qc.invalidateQueries({ queryKey: ["api-keys"] });
  }, [paired, qc]);

  // The countdown, and the moment the code stops being worth showing.
  useEffect(() => {
    if (!pairing || paired) return;
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, [pairing, paired]);

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const p = await post<Pairing>("/auth/pairings");
      /* Dark on light whatever the theme: a scanner reads contrast, not
         design tokens. */
      const svg = await QRCode.toString(pairingPayload(window.location.origin, p.code), {
        type: "svg",
        margin: 2,
        errorCorrectionLevel: "M",
        color: { dark: "#000000", light: "#ffffff" },
      });
      setQr(`data:image/svg+xml;utf8,${encodeURIComponent(svg)}`);
      setNow(Date.now());
      setPairing(p);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    setPairing(null);
    setQr("");
  };

  const left = pairing ? Math.max(0, Math.round((Date.parse(pairing.expires_at) - now) / 1000)) : 0;
  const countdown = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
  const notHttps = window.location.protocol !== "https:" && !["localhost", "127.0.0.1"].includes(window.location.hostname);

  return (
    <div className="card mt-4">
      <span className="text-sm font-medium">{t("account.pairing.title")}</span>
      <p className="muted text-xs mt-1 mb-3" style={{ maxWidth: 640 }}>{t("account.pairing.intro")}</p>
      {error && <p className="danger-text text-xs mb-2">{error}</p>}

      {!pairing && (
        <button className="btn sm primary" type="button" onClick={start} disabled={busy}>
          {t("account.pairing.start")}
        </button>
      )}

      {pairing && !paired && !expired && (
        <div className="flex gap-4 items-start flex-wrap">
          <img
            src={qr}
            alt={t("account.pairing.qrAlt")}
            width={208}
            height={208}
            style={{ borderRadius: 8, background: "#fff", border: "0.5px solid var(--border)" }}
          />
          <div className="text-sm" style={{ maxWidth: 320 }}>
            <p className="mt-0 mb-2">{t("account.pairing.scan")}</p>
            <p className="muted text-xs mt-0 mb-2">{t("account.pairing.waiting", { time: countdown })}</p>
            {notHttps && <p className="danger-text text-xs mt-0 mb-2">{t("account.pairing.notHttps")}</p>}
            <p className="muted text-xs mt-0 mb-2">{t("account.pairing.desktop")}</p>
            <div className="flex gap-2 flex-wrap">
              <a className="btn sm" href={appLink(window.location.origin, pairing.code)}>
                {t("account.pairing.openApp")}
              </a>
              <button className="btn sm" type="button" onClick={close}>
                {t("account.pairing.cancel")}
              </button>
            </div>
          </div>
        </div>
      )}

      {pairing && expired && (
        <div className="flex gap-2 items-center flex-wrap">
          <span className="text-sm">{t("account.pairing.expired")}</span>
          <button className="btn sm primary" type="button" onClick={start} disabled={busy}>
            {t("account.pairing.again")}
          </button>
        </div>
      )}

      {paired && (
        <div className="flex gap-2 items-center flex-wrap">
          <span className="text-sm">{t("account.pairing.paired", { device: state.data?.device ?? "" })}</span>
          <button className="btn sm" type="button" onClick={close}>
            {t("account.pairing.done")}
          </button>
        </div>
      )}
    </div>
  );
}
