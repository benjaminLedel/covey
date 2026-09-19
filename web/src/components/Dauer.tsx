import { useEffect, useState } from "react";

/* Die Dauer zählt im Browser weiter.
 *
 * Vom Server käme sie als Zahl, die in dem Moment falsch ist, in dem sie
 * ankommt — und eine Dauer, die erst beim nächsten Abruf springt, sagt das
 * Gegenteil dessen, was sie zeigen soll: dass hier gerade etwas passiert.
 */
export default function Dauer({ seit, className = "" }: { seit: string; className?: string }) {
  const [jetzt, setJetzt] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setJetzt(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const s = Math.max(0, Math.floor((jetzt - new Date(seit).getTime()) / 1000));
  const text =
    s < 60
      ? `${s}s`
      : s < 3600
        ? `${Math.floor(s / 60)}m ${s % 60}s`
        : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return (
    <time className={className} dateTime={seit}>
      {text}
    </time>
  );
}
