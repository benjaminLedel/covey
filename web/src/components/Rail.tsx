import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { myThreads, type Principal } from "../api";
import { BirdMark } from "./BirdMark";
import { NavIcon } from "./navicons";
import ShellFoot from "./ShellFoot";

/* The rail (#388, #389): the thin column of icons at the left of every
 * signed-in page — team, notes, administration; search and the person
 * below. Both shells carry it, the same in both, so it never disappears
 * when one moves from a conversation to the settings: what changes is the
 * column beside it.
 *
 * Without the team surface (#328) there is no team to go to, and the notes
 * live in the console. */
export type RailPlace = "team" | "notes" | "admin";

export default function Rail({
  me,
  active,
  onLogout,
  onHelp,
  onSearch,
}: {
  me: Principal;
  active: RailPlace;
  onLogout: () => void;
  onHelp: () => void;
  onSearch: () => void;
}) {
  const { t } = useTranslation();
  const team = !!me.TeamSurface;
  const threads = useQuery({
    queryKey: ["threads"],
    queryFn: myThreads,
    refetchInterval: 20_000,
    retry: false,
    enabled: team,
  });
  const unread = (threads.data ?? []).reduce((n, th) => n + th.unread, 0);

  const item = (place: RailPlace, to: string, icon: string, label: string, badge = 0) => (
    <Link
      to={to}
      className={`tm-rail-item${active === place ? " on" : ""}`}
      aria-current={active === place ? "page" : undefined}
      title={label}
      aria-label={badge > 0 ? `${label} · ${t("team.ungelesen", { count: badge })}` : label}
    >
      <NavIcon name={icon} />
      {badge > 0 && <span className="tm-rail-zahl">{Math.min(badge, 99)}</span>}
      <span className="tm-rail-wort">{label}</span>
    </Link>
  );

  return (
    <nav className="tm-rail" aria-label={t("team.bereiche")}>
      <Link to={team ? "/" : "/agents"} className="tm-rail-mark" aria-label="covey">
        <BirdMark size={30} />
      </Link>
      {team && item("team", "/", "chat", t("team.workspace"), unread)}
      {item("notes", team ? "/team/notes" : "/notes", "note", t("mobile.notizen"))}
      {item("admin", "/agents", "cog", t("team.verwaltung"))}
      <span className="tm-rail-luft" />
      <button className="tm-rail-item" onClick={onSearch} title={`${t("team.suche")} (⌘K)`} aria-label={t("team.suche")}>
        <NavIcon name="search" />
      </button>
      <ShellFoot me={me} onLogout={onLogout} onHelp={onHelp} compact />
    </nav>
  );
}
