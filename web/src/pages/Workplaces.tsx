import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api,
  post,
  createWorkplace,
  deleteWorkplace,
  type Principal,
  type Workplace,
} from "../api";
import { ConfirmDialog } from "../components/Modal";

/* Die Arbeitsplätze — dieselbe Ansicht, die es für Zielsysteme gibt, für das
   andere Ding, das eine Organisation anschließt: das Image, in dem ein Agent
   arbeitet.

   Sie hat sie vorher nur als Zeile in einem Auswahlfeld am Agenten gesehen, und
   damit ließ sich keine der Fragen beantworten, die man an einen Arbeitsplatz
   hat: Welches Image ist das, woher kommt es, liegt es hier, und wie viele
   Kollegen arbeiten darin. Vier Antworten, die es nur zusammen tun. */

const darfHolen = (role: string) => role === "org_admin" || role === "security";

/* Der Digest ist der Pin und deshalb sechzig Zeichen lang. Gezeigt wird der
   Anfang: genug, um zwei Instanzen zu vergleichen, kurz genug, um in einer
   Zeile zu stehen. Vollständig steht er im title. */
function kurzerDigest(image: string): string {
  const i = image.indexOf("@sha256:");
  if (i < 0) return "";
  return image.slice(i + 8, i + 20);
}

function Zeile({ w, me }: { w: Workplace; me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const loeschen = useMutation({
    mutationFn: () => deleteWorkplace(w.name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workplaces"] }),
  });
  const holen = useMutation({
    mutationFn: () => post<{ image: string; problems?: string[] }>(`/workplaces/${w.name}/pull`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workplaces"] }),
  });
  const probleme = holen.data?.problems ?? [];

  return (
    <div className="card mb-3">
      <div className="flex items-baseline gap-2 flex-wrap">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>
          {w.label}
        </h2>
        {w.default && <span className="pill">{t("workplaces.default")}</span>}
        {/* „Vorhanden" beantwortet der Runner, nicht die Control Plane: Ein
            Image liegt dort, wo die Sandbox startet. Fehlt die Antwort, hat
            niemand gefragt werden können — das ist etwas anderes als „nicht
            da" und steht deshalb auch anders da. */}
        {w.available === true && <span className="pill ok">{t("workplaces.present")}</span>}
        {w.available === false && <span className="pill">{t("workplaces.absent")}</span>}
        <span className="muted text-xs ml-auto">
          {w.agents?.length ? (
            <>
              {t("workplaces.worksHere")}{" "}
              {w.agents.map((a, i) => (
                <span key={a.id}>
                  {i > 0 && ", "}
                  <Link to={`/agents/${a.id}`}>{a.display_name}</Link>
                </span>
              ))}
            </>
          ) : (
            t("workplaces.nobody")
          )}
        </span>
      </div>

      <p className="muted text-xs mb-2" style={{ maxWidth: 720 }}>
        {w.description}
      </p>

      <div className="text-xs flex flex-col gap-1" style={{ maxWidth: 720 }}>
        <div className="flex items-baseline gap-2 flex-wrap">
          <span className="muted">{t("workplaces.image")}</span>
          {/* Der Tag ist der Name, unter dem das Image veröffentlicht wurde —
              das, was man mit einer Release-Notiz vergleicht. Der Digest
              daneben ist, was tatsächlich startet. */}
          <span className="mono">{w.tag || w.image}</span>
          {kurzerDigest(w.image) && (
            <span className="muted mono" title={w.image}>
              @{kurzerDigest(w.image)}…
            </span>
          )}
          {w.platforms?.length ? <span className="muted">· {w.platforms.join(", ")}</span> : null}
        </div>
        <div>
          <span className="muted">
            {w.source ? t(`workplaces.source.${w.source}`) : ""}
          </span>
        </div>
      </div>

      {w.available === false && (
        <div className="flex items-center gap-2 mt-3">
          {/* Holen geht nur, was auch zu holen ist. Ein selbst gebautes Image
              trägt einen Namen, den keine Registry kennt — dort ist der Bau
              die Antwort, und die steht daneben. */}
          {w.image.includes("/") ? (
            <>
              <button
                className="btn sm"
                disabled={!darfHolen(me.Role) || holen.isPending}
                onClick={() => holen.mutate()}
              >
                {holen.isPending ? t("workplaces.pulling") : t("workplaces.pull")}
              </button>
              <span className="muted text-xs">{t("workplaces.pullHint")}</span>
            </>
          ) : (
            <span className="muted text-xs">
              {t("workplaces.buildHint", { build: w.build })}
            </span>
          )}
        </div>
      )}
      {holen.isError && (
        <p className="danger-text text-xs mt-2">{(holen.error as Error).message}</p>
      )}
      {holen.isSuccess && probleme.length === 0 && (
        <p className="muted text-xs mt-2">{t("workplaces.pulled")}</p>
      )}
      {probleme.map((p, i) => (
        <p key={i} className="warn-text text-xs mt-2">
          {p}
        </p>
      ))}
      {w.kind === "own" && darfHolen(me.Role) && (
        <>
          <button
            className="btn sm danger mt-3"
            onClick={() => setConfirm(true)}
            disabled={loeschen.isPending}
          >
            {t("workplaces.delete")}
          </button>
          {loeschen.isError && (
            <p className="danger-text text-xs mt-2">{(loeschen.error as Error).message}</p>
          )}
          {confirm && (
            <ConfirmDialog
              title={t("workplaces.deleteTitle", { name: w.label || w.name })}
              confirmLabel={t("workplaces.delete")}
              pending={loeschen.isPending}
              onClose={() => setConfirm(false)}
              onConfirm={() => {
                setConfirm(false);
                loeschen.mutate();
              }}
            >
              {t("workplaces.deleteBody")}
            </ConfirmDialog>
          )}
        </>
      )}
    </div>
  );
}

/* embedded: siehe Runtimes — der Reiter trägt die Überschrift. */
export default function Workplaces({ me, embedded = false }: { me: Principal; embedded?: boolean }) {
  const { t } = useTranslation();
  const list = useQuery({
    queryKey: ["workplaces"],
    queryFn: () => api<Workplace[]>("/workplaces"),
  });

  return (
    <div>
      {!embedded && (
        <div className="flex items-baseline gap-3 mb-2">
          <h1 className="text-[22px]">{t("workplaces.title")}</h1>
          <span className="muted">{t("workplaces.subtitle")}</span>
        </div>
      )}
      <p className="muted text-xs mb-4" style={{ maxWidth: 720 }}>
        {t("workplaces.desc")}
      </p>

      {list.isError && <p className="danger-text">{t("workplaces.loadError")}</p>}
      {(list.data ?? []).map((w) => (
        <Zeile key={w.name} w={w} me={me} />
      ))}

      {darfHolen(me.Role) && <Anlegen />}
    </div>
  );
}

/* Ein eigenes Image anmelden. Es steht hier und nicht als freies Textfeld am
   Agenten: Dort war es unsichtbar (in keiner Übersicht), unbeschrieben (eine
   Registry-Adresse sagt nicht, wozu sie da ist) und ein Tippfehler fiel erst
   beim Wecken auf. Einmal angelegt, wird es danach ausgewählt wie jedes
   andere. */
function Anlegen() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [offen, setOffen] = useState(false);
  const [name, setName] = useState("");
  const [label, setLabel] = useState("");
  const [beschreibung, setBeschreibung] = useState("");
  const [image, setImage] = useState("");

  const anlegen = useMutation({
    mutationFn: () => createWorkplace({ name, label, description: beschreibung, image }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["workplaces"] });
      setOffen(false);
      setName("");
      setLabel("");
      setBeschreibung("");
      setImage("");
    },
  });

  if (!offen) {
    return (
      <button className="btn sm" onClick={() => setOffen(true)}>
        {t("workplaces.add")}
      </button>
    );
  }
  return (
    <form
      className="card"
      style={{ maxWidth: 640 }}
      onSubmit={(e) => {
        e.preventDefault();
        anlegen.mutate();
      }}
    >
      <h2 className="text-sm mb-2" style={{ fontWeight: 600 }}>
        {t("workplaces.add")}
      </h2>
      <label>{t("workplaces.fieldName")}</label>
      <input
        className="mono mb-2"
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="dev-flutter"
        required
      />
      <label>{t("workplaces.fieldLabel")}</label>
      <input className="mb-2" value={label} onChange={(e) => setLabel(e.target.value)} />
      <label>{t("workplaces.fieldDescription")}</label>
      <input
        className="mb-2"
        value={beschreibung}
        onChange={(e) => setBeschreibung(e.target.value)}
        placeholder={t("workplaces.fieldDescriptionHint")}
      />
      <label>{t("workplaces.fieldImage")}</label>
      <input
        className="mono mb-2"
        value={image}
        onChange={(e) => setImage(e.target.value)}
        placeholder="registry.example.com/team/sandbox:2026-08"
        required
      />
      {anlegen.isError && (
        <p className="danger-text text-xs mb-2">{(anlegen.error as Error).message}</p>
      )}
      <div className="flex gap-2">
        <button className="btn sm primary" type="submit" disabled={anlegen.isPending}>
          {t("workplaces.create")}
        </button>
        <button className="btn sm" type="button" onClick={() => setOffen(false)}>
          {t("modal.cancel")}
        </button>
      </div>
    </form>
  );
}
