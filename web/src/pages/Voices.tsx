import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, del, post, type Principal, type Voice, type VoiceDetail } from "../api";

const canEdit = (role: string) => role === "org_admin" || role === "agent_owner";

/* The voices — a library beside the skills, for the other thing an agent gets
   from its organisation: how to write.

   Why a page of its own rather than a field on the agent: a voice is BUILT from
   uploaded texts, it holds for several agents, and one of its four parts is
   written by a model and released by a person. That is an object with a life,
   not a picker (spec/24). */
export default function Voices({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const voices = useQuery({ queryKey: ["voices"], queryFn: () => api<Voice[]>("/voices"), retry: false });
  const [name, setName] = useState("");
  const [language, setLanguage] = useState("de");
  const editable = canEdit(me.Role);

  const create = useMutation({
    mutationFn: () => post<Voice>("/voices", { name, language }),
    onSuccess: () => {
      setName("");
      qc.invalidateQueries({ queryKey: ["voices"] });
    },
  });

  if (voices.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">{t("voices.title")}</h1>
        <p className="muted">{(voices.error as Error).message}</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("voices.title")}</h1>
        <span className="muted">{t("voices.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 680 }}>
        {t("voices.desc")}
      </p>

      {editable && (
        <div className="card mb-4 flex items-end gap-2 flex-wrap">
          <label className="text-xs">
            <div className="muted mb-1">{t("voices.name")}</div>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("voices.namePlaceholder")} />
          </label>
          <label className="text-xs">
            <div className="muted mb-1">{t("voices.language")}</div>
            <input
              className="mono"
              style={{ width: 70 }}
              value={language}
              onChange={(e) => setLanguage(e.target.value)}
            />
          </label>
          <button className="btn primary sm" disabled={!name.trim() || create.isPending} onClick={() => create.mutate()}>
            {t("voices.new")}
          </button>
          {create.isError && (
            <span className="text-xs" style={{ color: "var(--error)" }}>
              {(create.error as Error).message}
            </span>
          )}
        </div>
      )}

      {(voices.data ?? []).map((v) => (
        <VoiceCard key={v.id} voice={v} editable={editable} />
      ))}
      {voices.data?.length === 0 && <p className="muted">{t("voices.empty")}</p>}
    </div>
  );
}

function VoiceCard({ voice, editable }: { voice: Voice; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const inval = () => {
    qc.invalidateQueries({ queryKey: ["voices"] });
    qc.invalidateQueries({ queryKey: ["voice", voice.id] });
  };

  const build = useMutation({ mutationFn: () => post<Voice>(`/voices/${voice.id}/build`), onSuccess: inval });
  const remove = useMutation({ mutationFn: () => del(`/voices/${voice.id}`), onSuccess: inval });

  return (
    <div className="card mb-3">
      <div className="flex items-baseline gap-2 flex-wrap">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>
          {voice.name}
        </h2>
        {voice.language && <span className="pill">{voice.language}</span>}
        {voice.version > 0 ? (
          <span className="pill ok">{t("voices.version", { version: voice.version })}</span>
        ) : (
          <span className="pill">{t("voices.unbuilt")}</span>
        )}
        {voice.released_at ? (
          <span className="pill ok">{t("voices.cardReleased")}</span>
        ) : voice.card ? (
          <span className="pill">{t("voices.cardDraft")}</span>
        ) : null}
        <span className="muted text-xs ml-auto">
          {voice.agents?.length ? (
            <>
              {t("voices.carriedBy")}{" "}
              {voice.agents.map((a, i) => (
                <span key={a.id}>
                  {i > 0 && ", "}
                  <Link to={`/agents/${a.id}`}>{a.display_name}</Link>
                </span>
              ))}
            </>
          ) : (
            t("voices.nobody")
          )}
        </span>
      </div>

      <p className="muted text-xs mb-2">
        {t("voices.builtFrom", { documents: voice.documents, words: voice.words })}
      </p>

      {/* What the build has to say about itself. Bands from three texts are
          usable — but whoever reads a finding against them should know what
          they rest on. */}
      {voice.notes?.map((n, i) => (
        <p key={i} className="text-xs" style={{ color: "var(--warning, #b45309)", margin: "0 0 4px" }}>
          {n}
        </p>
      ))}

      <div className="flex gap-2 mt-2 flex-wrap">
        <button className="btn sm" onClick={() => setOpen(!open)}>
          {open ? t("voices.hide") : t("voices.show")}
        </button>
        {editable && (
          <button className="btn sm" disabled={build.isPending} onClick={() => build.mutate()}>
            {build.isPending ? t("voices.building") : t("voices.build")}
          </button>
        )}
        {editable && (
          <button className="btn sm danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
            {t("voices.delete")}
          </button>
        )}
        {build.isError && (
          <span className="text-xs" style={{ color: "var(--error)" }}>
            {(build.error as Error).message}
          </span>
        )}
      </div>

      {open && <VoiceDetailView id={voice.id} editable={editable} />}
    </div>
  );
}

function VoiceDetailView({ id, editable }: { id: string; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const detail = useQuery({ queryKey: ["voice", id], queryFn: () => api<VoiceDetail>(`/voices/${id}`) });
  const [docName, setDocName] = useState("");
  const [docBody, setDocBody] = useState("");
  const [docKind, setDocKind] = useState<"author" | "reference">("author");
  const [card, setCard] = useState<string | null>(null);
  const inval = () => {
    qc.invalidateQueries({ queryKey: ["voice", id] });
    qc.invalidateQueries({ queryKey: ["voices"] });
  };

  const addDoc = useMutation({
    mutationFn: () => post(`/voices/${id}/documents`, { name: docName, body: docBody, kind: docKind }),
    onSuccess: () => {
      setDocName("");
      setDocBody("");
      inval();
    },
  });
  const dropDoc = useMutation({
    mutationFn: (docID: string) => del(`/voices/${id}/documents/${docID}`),
    onSuccess: inval,
  });
  const release = useMutation({
    mutationFn: () => post(`/voices/${id}/release`, { card: card ?? "" }),
    onSuccess: inval,
  });

  if (!detail.data) return <p className="muted text-xs mt-3">{t("common.loading")}</p>;
  const v = detail.data;
  const draft = card ?? v.card;

  return (
    <div className="mt-3 text-xs flex flex-col gap-3">
      <div>
        <div className="text-sm font-medium mb-1">{t("voices.corpus")}</div>
        <p className="muted mb-2" style={{ maxWidth: 680 }}>
          {t("voices.corpusHint")}
        </p>
        {v.corpus.map((d) => (
          <div key={d.id} className="flex items-baseline gap-2">
            <span className="mono">{d.name}</span>
            <span className="muted">{t("voices.documentWords", { words: d.words })}</span>
            {d.kind === "reference" && <span className="pill">{t("voices.reference")}</span>}
            {editable && (
              <button className="btn sm" onClick={() => dropDoc.mutate(d.id)}>
                {t("voices.delete")}
              </button>
            )}
          </div>
        ))}
        {v.corpus.length === 0 && <p className="muted">{t("voices.corpusEmpty")}</p>}
        {editable && (
          <div className="flex flex-col gap-2 mt-2" style={{ maxWidth: 680 }}>
            <div className="flex gap-2 items-center flex-wrap">
              <input
                value={docName}
                onChange={(e) => setDocName(e.target.value)}
                placeholder={t("voices.documentName")}
              />
              <select value={docKind} onChange={(e) => setDocKind(e.target.value as "author" | "reference")}>
                <option value="author">{t("voices.kindAuthor")}</option>
                <option value="reference">{t("voices.kindReference")}</option>
              </select>
              <button
                className="btn sm"
                disabled={!docName.trim() || !docBody.trim() || addDoc.isPending}
                onClick={() => addDoc.mutate()}
              >
                {t("voices.addDocument")}
              </button>
            </div>
            <textarea
              rows={5}
              value={docBody}
              onChange={(e) => setDocBody(e.target.value)}
              placeholder={t("voices.documentBody")}
            />
            {addDoc.isError && <span style={{ color: "var(--error)" }}>{(addDoc.error as Error).message}</span>}
          </div>
        )}
      </div>

      {/* The card is the only part a model writes — and therefore the only one
          a person releases. After that it stands in the prompt of every run of
          every agent carrying this voice. */}
      <div>
        <div className="text-sm font-medium mb-1">{t("voices.card")}</div>
        <p className="muted mb-2" style={{ maxWidth: 680 }}>
          {t("voices.cardHint")}
        </p>
        {draft ? (
          <>
            <textarea
              rows={8}
              style={{ width: "100%", maxWidth: 680 }}
              value={draft}
              onChange={(e) => setCard(e.target.value)}
              readOnly={!editable}
            />
            {editable && (
              <div className="flex gap-2 items-center mt-1">
                <button className="btn sm primary" disabled={release.isPending} onClick={() => release.mutate()}>
                  {t("voices.release")}
                </button>
                {v.released_at && <span className="muted">{t("voices.releasedAt", { at: v.released_at })}</span>}
              </div>
            )}
          </>
        ) : (
          <p className="muted">{t("voices.cardMissing")}</p>
        )}
      </div>

      {v.exemplars?.length > 0 && (
        <div>
          <div className="text-sm font-medium mb-1">{t("voices.exemplars")}</div>
          <p className="muted mb-2" style={{ maxWidth: 680 }}>
            {t("voices.exemplarsHint")}
          </p>
          {v.exemplars.map((ex, i) => (
            <div key={i} className="mb-2" style={{ maxWidth: 680 }}>
              <span className="pill">{t(`voices.role.${ex.role}`, ex.role)}</span>{" "}
              <span className="muted mono">{ex.from}</span>
              <p className="mt-1 mb-0">{ex.text}</p>
            </div>
          ))}
        </div>
      )}

      {v.contrast?.length > 0 && (
        <div>
          <div className="text-sm font-medium mb-1">{t("voices.contrast")}</div>
          <p className="muted mb-2" style={{ maxWidth: 680 }}>
            {t("voices.contrastHint")}
          </p>
          {v.contrast.map((c) => (
            <div key={c.metric}>
              <span>{c.label || c.metric}</span>{" "}
              <span className="muted">{t("voices.contrastValues", { author: c.author, other: c.other })}</span>
            </div>
          ))}
        </div>
      )}

      {/* What the agent actually gets. The four parts on their own do not say
          what lands in the prompt — and that is the question somebody has in
          front of a voice. */}
      <details>
        <summary className="text-sm font-medium">{t("voices.tone")}</summary>
        <pre className="mono" style={{ whiteSpace: "pre-wrap", maxWidth: 680 }}>
          {v.tone}
        </pre>
      </details>
    </div>
  );
}
