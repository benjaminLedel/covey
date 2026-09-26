import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { block, isListItem, isText, parseBlocks, turnInto, writeBlocks, type Block, type BlockKind } from "./blocks";
import { Inline } from "./inline";

/* The note's body as blocks (#386), the web's counterpart of the app's
 * editor (mobile/lib/rich/editor.dart) — the same blocks, the same slash
 * menu, the same handle.
 *
 * A block reads as formatted text and becomes its source when clicked: the
 * caret goes where the markup is, and nothing hides what is stored. The
 * body stays Markdown throughout; every change is written back through
 * writeBlocks and handed up. */

type SlashOption = { id: string; label: string; kind?: BlockKind; aliases: string[]; glyph: string };

const OPTIONS: SlashOption[] = [
  { id: "text", label: "mobile.block_text", kind: "paragraph", aliases: ["text", "paragraph", "p"], glyph: "¶" },
  { id: "h1", label: "mobile.block_h1", kind: "heading1", aliases: ["h1", "heading", "title"], glyph: "H1" },
  { id: "h2", label: "mobile.block_h2", kind: "heading2", aliases: ["h2", "heading"], glyph: "H2" },
  { id: "h3", label: "mobile.block_h3", kind: "heading3", aliases: ["h3", "heading"], glyph: "H3" },
  { id: "bullet", label: "mobile.block_bullet", kind: "bullet", aliases: ["bullet", "list", "ul", "-"], glyph: "•" },
  { id: "numbered", label: "mobile.block_numbered", kind: "numbered", aliases: ["numbered", "ol", "1."], glyph: "1." },
  { id: "todo", label: "mobile.block_todo", kind: "todo", aliases: ["todo", "task", "check", "[]"], glyph: "☐" },
  { id: "toggle", label: "mobile.block_toggle", kind: "toggle", aliases: ["toggle", "details", "fold"], glyph: "▸" },
  { id: "callout", label: "mobile.block_callout", kind: "callout", aliases: ["callout", "note", "info", "tip"], glyph: "💡" },
  { id: "quote", label: "mobile.block_quote", kind: "quote", aliases: ["quote", "cite"], glyph: "❝" },
  { id: "divider", label: "mobile.block_divider", kind: "divider", aliases: ["divider", "line", "hr", "---"], glyph: "—" },
  { id: "table", label: "mobile.block_table", kind: "table", aliases: ["table", "grid"], glyph: "▦" },
  { id: "image", label: "mobile.block_image", kind: "image", aliases: ["image", "picture", "photo", "img"], glyph: "▣" },
];

export const CALLOUT_EMOJIS = ["💡", "⚠️", "✅", "❗", "📌", "ℹ️", "🔥", "📝", "🎯", "❓"];

/* Markdown typed at the start of a paragraph turns it into its block, as in
   the app and in Notion: "# ", "- ", "1. ", "[] ", "> ", "---". */
const SHORTCUTS: [RegExp, BlockKind, (m: RegExpExecArray) => Partial<Block>][] = [
  [/^### (.*)$/s, "heading3", (m) => ({ text: m[1] })],
  [/^## (.*)$/s, "heading2", (m) => ({ text: m[1] })],
  [/^# (.*)$/s, "heading1", (m) => ({ text: m[1] })],
  [/^\[( |x)?\] (.*)$/s, "todo", (m) => ({ text: m[2], checked: m[1] === "x" })],
  [/^[-*] (.*)$/s, "bullet", (m) => ({ text: m[1] })],
  [/^1\. (.*)$/s, "numbered", (m) => ({ text: m[1] })],
  [/^> (.*)$/s, "quote", (m) => ({ text: m[1] })],
  [/^---$/, "divider", () => ({ text: "" })],
];

type Focus = { id: string; at: number | "end" } | null;

export default function BlockEditor({
  initial,
  onChange,
  resolveImage,
  upload,
  placeholder,
}: {
  initial: string;
  onChange: (markdown: string) => void;
  resolveImage: (ref: string) => string | null;
  upload: (file: File) => Promise<string>;
  placeholder: string;
}) {
  const { t } = useTranslation();
  const [blocks, setBlocks] = useState<Block[]>(() => parseBlocks(initial));
  const [focus, setFocus] = useState<Focus>(null);
  const [slash, setSlash] = useState<{ id: string; query: string; index: number } | null>(null);
  const [menu, setMenu] = useState<{ id: string; turn: boolean } | null>(null);
  const [drag, setDrag] = useState<{ id: string; over: string | null; after: boolean } | null>(null);
  const first = useRef(true);

  /* Every change goes up as Markdown — not the first render, which is what
     came in. */
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    onChange(writeBlocks(blocks));
  }, [blocks]); // eslint-disable-line react-hooks/exhaustive-deps

  const update = useCallback((id: string, change: Partial<Block>) => {
    setBlocks((bs) => bs.map((b) => (b.id === id ? { ...b, ...change } : b)));
  }, []);

  const indexOf = (id: string) => blocks.findIndex((b) => b.id === id);

  const insertAfter = (id: string, nb: Block, focusIt = true) => {
    setBlocks((bs) => {
      const i = bs.findIndex((b) => b.id === id);
      return [...bs.slice(0, i + 1), nb, ...bs.slice(i + 1)];
    });
    if (focusIt && isText(nb)) setFocus({ id: nb.id, at: 0 });
  };

  const remove = (id: string) => {
    const i = indexOf(id);
    setBlocks((bs) => {
      const rest = bs.filter((b) => b.id !== id);
      return rest.length ? rest : [block("paragraph")];
    });
    const prev = blocks[i - 1] ?? blocks[i + 1];
    if (prev && isText(prev)) setFocus({ id: prev.id, at: "end" });
  };

  const duplicate = (id: string) => {
    const b = blocks[indexOf(id)];
    insertAfter(id, { ...structuredClone(b), id: block("paragraph").id }, false);
  };

  /* A slash option applied: an empty block becomes it, a block with text
     gets it as the next block. */
  const apply = async (b: Block, opt: SlashOption, textWithoutSlash: string) => {
    setSlash(null);
    if (opt.kind === "image") {
      const file = await pickImage();
      if (!file) return;
      const ref = await upload(file);
      if (textWithoutSlash.trim() === "") {
        setBlocks((bs) => bs.map((x) => (x.id === b.id ? { ...x, kind: "image", text: "", ref } : x)));
      } else {
        update(b.id, { text: textWithoutSlash });
        insertAfter(b.id, block("image", { ref }), false);
      }
      return;
    }
    const kind = opt.kind!;
    if (textWithoutSlash.trim() === "") {
      const turned = turnInto({ ...b, text: "" }, kind);
      setBlocks((bs) => bs.map((x) => (x.id === b.id ? turned : x)));
      if (isText(turned)) setFocus({ id: b.id, at: 0 });
      else {
        const next = block("paragraph");
        insertAfter(b.id, next);
      }
    } else {
      update(b.id, { text: textWithoutSlash });
      const nb = turnInto(block("paragraph"), kind);
      insertAfter(b.id, nb);
    }
  };

  const onText = (b: Block, value: string, caret: number) => {
    // A shortcut at the start of a paragraph.
    if (b.kind === "paragraph") {
      for (const [re, kind, fields] of SHORTCUTS) {
        const m = re.exec(value);
        if (m && caret <= value.length) {
          const turned = { ...turnInto(b, kind), ...fields(m) };
          setBlocks((bs) => bs.map((x) => (x.id === b.id ? turned : x)));
          if (kind === "divider") insertAfter(b.id, block("paragraph"));
          else setFocus({ id: b.id, at: 0 });
          return;
        }
      }
    }
    update(b.id, { text: value });
    // "/" at the start or after a space opens the menu; what follows filters.
    const before = value.slice(0, caret);
    const m = /(^|\s)\/([^\s/]*)$/.exec(before);
    if (m) setSlash((s) => ({ id: b.id, query: m[2], index: s?.id === b.id && s.query === m[2] ? s.index : 0 }));
    else if (slash?.id === b.id) setSlash(null);
  };

  const options = useMemo(() => {
    if (!slash) return [];
    const q = slash.query.toLowerCase();
    return OPTIONS.filter((o) => !q || t(o.label).toLowerCase().includes(q) || o.aliases.some((a) => a.startsWith(q)));
  }, [slash, t]);

  const onKey = (b: Block, e: KeyboardEvent<HTMLTextAreaElement>) => {
    const el = e.currentTarget;
    if (e.nativeEvent.isComposing) return;
    if (slash?.id === b.id) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const d = e.key === "ArrowDown" ? 1 : -1;
        setSlash({ ...slash, index: (slash.index + d + options.length) % Math.max(options.length, 1) });
        return;
      }
      if (e.key === "Enter" && options.length) {
        e.preventDefault();
        const caret = el.selectionStart;
        const start = el.value.slice(0, caret).lastIndexOf("/");
        const rest = el.value.slice(0, start) + el.value.slice(caret);
        void apply(b, options[slash.index] ?? options[0], rest);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setSlash(null);
        return;
      }
    }
    const i = indexOf(b.id);
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      const caret = el.selectionStart;
      const head = el.value.slice(0, caret);
      const tail = el.value.slice(el.selectionEnd);
      // An empty list item ends the list, as everywhere.
      if (isListItem(b) && el.value === "") {
        update(b.id, { kind: "paragraph" });
        return;
      }
      update(b.id, { text: head });
      const kind: BlockKind = isListItem(b) ? b.kind : "paragraph";
      insertAfter(b.id, block(kind, { text: tail, checked: false }));
      return;
    }
    if (e.key === "Backspace" && el.selectionStart === 0 && el.selectionEnd === 0) {
      if (b.kind !== "paragraph") {
        e.preventDefault();
        update(b.id, { kind: "paragraph" });
        return;
      }
      const prev = blocks[i - 1];
      if (prev && isText(prev) && prev.kind !== "toggle") {
        e.preventDefault();
        const at = prev.text.length;
        setBlocks((bs) => bs.filter((x) => x.id !== b.id).map((x) => (x.id === prev.id ? { ...x, text: x.text + b.text } : x)));
        setFocus({ id: prev.id, at });
        return;
      }
      if (prev && !isText(prev) && b.text === "") {
        e.preventDefault();
        remove(b.id);
      }
      return;
    }
    if (e.key === "ArrowUp" && el.selectionStart === 0) {
      const prev = [...blocks.slice(0, i)].reverse().find(isText);
      if (prev) {
        e.preventDefault();
        setFocus({ id: prev.id, at: "end" });
      }
    }
    if (e.key === "ArrowDown" && el.selectionEnd === el.value.length) {
      const next = blocks.slice(i + 1).find(isText);
      if (next) {
        e.preventDefault();
        setFocus({ id: next.id, at: 0 });
      }
    }
  };

  /* Pictures pasted or dropped go to the media store and stand as their own
     block after the one they arrived in. */
  const addFiles = async (afterId: string, files: File[]) => {
    for (const f of files.filter((f) => f.type.startsWith("image/"))) {
      const ref = await upload(f);
      insertAfter(afterId, block("image", { ref }), false);
    }
  };

  const onDrop = (target: string) => {
    if (!drag || drag.id === target) return setDrag(null);
    setBlocks((bs) => {
      const moving = bs.find((b) => b.id === drag.id)!;
      const rest = bs.filter((b) => b.id !== drag.id);
      const at = rest.findIndex((b) => b.id === target) + (drag.after ? 1 : 0);
      return [...rest.slice(0, at), moving, ...rest.slice(at)];
    });
    setDrag(null);
  };

  return (
    <div
      className="nb"
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) e.preventDefault();
      }}
      onDrop={(e) => {
        if (e.dataTransfer.files.length && !drag) {
          e.preventDefault();
          void addFiles(blocks[blocks.length - 1].id, [...e.dataTransfer.files]);
        }
      }}
    >
      {blocks.map((b, i) => (
        <div
          key={b.id}
          className={`nb-row nb-${b.kind}${drag?.over === b.id ? (drag.after ? " drop-after" : " drop-before") : ""}`}
          onDragOver={(e) => {
            if (!drag) return;
            e.preventDefault();
            const r = e.currentTarget.getBoundingClientRect();
            setDrag({ ...drag, over: b.id, after: e.clientY > r.top + r.height / 2 });
          }}
          onDrop={(e) => {
            if (!drag) return;
            e.preventDefault();
            onDrop(b.id);
          }}
        >
          <div className="nb-gutter">
            <button
              type="button"
              className="nb-handle"
              draggable
              title={t("mobile.blockMenue")}
              aria-label={t("mobile.blockMenue")}
              onDragStart={(e) => {
                e.dataTransfer.effectAllowed = "move";
                e.dataTransfer.setData("text/plain", b.id);
                setDrag({ id: b.id, over: null, after: false });
              }}
              onDragEnd={() => setDrag(null)}
              onClick={() => setMenu(menu?.id === b.id ? null : { id: b.id, turn: false })}
            >
              ⋮⋮
            </button>
            {menu?.id === b.id && (
              <BlockMenu
                block={b}
                turn={menu.turn}
                onTurnList={() => setMenu({ id: b.id, turn: true })}
                onClose={() => setMenu(null)}
                onDelete={() => {
                  setMenu(null);
                  remove(b.id);
                }}
                onDuplicate={() => {
                  setMenu(null);
                  duplicate(b.id);
                }}
                onTurn={(kind) => {
                  setMenu(null);
                  setBlocks((bs) => bs.map((x) => (x.id === b.id ? turnInto(x, kind) : x)));
                }}
              />
            )}
          </div>
          <div className="nb-content">
            <BlockView
              b={b}
              number={numberAt(blocks, i)}
              focused={focus?.id === b.id ? focus.at : null}
              onFocus={(at) => setFocus({ id: b.id, at })}
              onBlur={() => {
                setFocus((f) => (f?.id === b.id ? null : f));
                setTimeout(() => setSlash((s) => (s?.id === b.id ? null : s)), 150);
              }}
              onText={(v, caret) => onText(b, v, caret)}
              onKey={(e) => onKey(b, e)}
              onPaste={(files) => void addFiles(b.id, files)}
              update={(c) => update(b.id, c)}
              resolveImage={resolveImage}
              placeholder={i === blocks.length - 1 && blocks.length === 1 ? placeholder : t("mobile.slashHinweis")}
            />
            {slash?.id === b.id && (
              <div className="nb-slash" role="listbox">
                {options.length === 0 && <div className="nb-slash-none">{t("mobile.blockKeiner")}</div>}
                {options.map((o, k) => (
                  <button
                    key={o.id}
                    type="button"
                    role="option"
                    aria-selected={k === slash.index}
                    className={`nb-slash-opt${k === slash.index ? " on" : ""}`}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      const el = document.activeElement as HTMLTextAreaElement | null;
                      const value = el?.value ?? b.text;
                      const caret = el?.selectionStart ?? value.length;
                      const start = value.slice(0, caret).lastIndexOf("/");
                      void apply(b, o, value.slice(0, start) + value.slice(caret));
                    }}
                    onMouseEnter={() => setSlash({ ...slash, index: k })}
                  >
                    <span className="nb-slash-glyph" aria-hidden="true">
                      {o.glyph}
                    </span>
                    {t(o.label)}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      ))}
      {/* Below the last block: a click starts a new one there. */}
      <div
        className="nb-tail"
        onClick={() => {
          const last = blocks[blocks.length - 1];
          if (isText(last) && last.kind === "paragraph" && last.text === "") setFocus({ id: last.id, at: 0 });
          else {
            const nb = block("paragraph");
            setBlocks((bs) => [...bs, nb]);
            setFocus({ id: nb.id, at: 0 });
          }
        }}
      />
    </div>
  );
}

/** A numbered item's number: counted from its run's start. */
function numberAt(blocks: Block[], i: number) {
  let n = 0;
  for (let k = i; k >= 0 && blocks[k].kind === "numbered"; k--) n++;
  return n;
}

function pickImage(): Promise<File | null> {
  return new Promise((resolve) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = "image/jpeg,image/png,image/gif,image/webp";
    input.onchange = () => resolve(input.files?.[0] ?? null);
    input.click();
  });
}

/** A textarea as tall as its text. */
function Grow({
  value,
  focusAt,
  className,
  placeholder,
  onChange,
  onKeyDown,
  onBlur,
  onPaste,
}: {
  value: string;
  focusAt: number | "end" | null;
  className: string;
  placeholder?: string;
  onChange: (v: string, caret: number) => void;
  onKeyDown?: (e: KeyboardEvent<HTMLTextAreaElement>) => void;
  onBlur: () => void;
  onPaste?: (files: File[]) => void;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${el.scrollHeight}px`;
  }, [value]);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || focusAt === null) return;
    if (document.activeElement !== el) {
      el.focus();
      const at = focusAt === "end" ? el.value.length : Math.min(focusAt, el.value.length);
      el.setSelectionRange(at, at);
    }
  }, [focusAt]);
  return (
    <textarea
      ref={ref}
      rows={1}
      className={className}
      value={value}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value, e.target.selectionStart)}
      onKeyDown={onKeyDown}
      onBlur={onBlur}
      onPaste={(e) => {
        const files = [...e.clipboardData.files];
        if (files.length && onPaste) {
          e.preventDefault();
          onPaste(files);
        }
      }}
    />
  );
}

function BlockView({
  b,
  number,
  focused,
  onFocus,
  onBlur,
  onText,
  onKey,
  onPaste,
  update,
  resolveImage,
  placeholder,
}: {
  b: Block;
  number: number;
  focused: number | "end" | null;
  onFocus: (at: number | "end") => void;
  onBlur: () => void;
  onText: (v: string, caret: number) => void;
  onKey: (e: KeyboardEvent<HTMLTextAreaElement>) => void;
  onPaste: (files: File[]) => void;
  update: (c: Partial<Block>) => void;
  resolveImage: (ref: string) => string | null;
  placeholder: string;
}) {
  const { t } = useTranslation();
  const [emojiOpen, setEmojiOpen] = useState(false);

  /* The text part of a text block: formatted when resting, the source when
     being edited. */
  const text = (cls: string, ph = placeholder): ReactNode =>
    focused !== null ? (
      <Grow
        value={b.text}
        focusAt={focused}
        className={`nb-input ${cls}`}
        placeholder={ph}
        onChange={onText}
        onKeyDown={onKey}
        onBlur={onBlur}
        onPaste={onPaste}
      />
    ) : (
      <div
        className={`nb-text ${cls}${b.text ? "" : " empty"}`}
        data-placeholder={ph}
        tabIndex={0}
        onClick={() => onFocus("end")}
        onFocus={() => onFocus("end")}
      >
        <Inline text={b.text} />
      </div>
    );

  switch (b.kind) {
    case "paragraph":
      return text("nb-p");
    case "heading1":
    case "heading2":
    case "heading3":
      return text(`nb-${b.kind}`, t(`mobile.block_${b.kind.replace("heading", "h")}`));
    case "bullet":
      return (
        <div className="nb-li">
          <span className="nb-marker" aria-hidden="true">
            •
          </span>
          {text("nb-p", "")}
        </div>
      );
    case "numbered":
      return (
        <div className="nb-li">
          <span className="nb-marker nb-num" aria-hidden="true">
            {number}.
          </span>
          {text("nb-p", "")}
        </div>
      );
    case "todo":
      return (
        <div className={`nb-li${b.checked ? " done" : ""}`}>
          <input
            type="checkbox"
            className="nb-check"
            checked={!!b.checked}
            onChange={(e) => update({ checked: e.target.checked })}
            aria-label={t("mobile.checkliste")}
          />
          {text("nb-p", t("mobile.block_todo"))}
        </div>
      );
    case "quote":
      return <div className="nb-quote">{text("nb-p", t("mobile.block_quote"))}</div>;
    case "callout":
      return (
        <div className="nb-callout">
          <span className="nb-callout-emoji-wrap">
            <button type="button" className="nb-callout-emoji" onClick={() => setEmojiOpen((o) => !o)}>
              {b.emoji ?? "💡"}
            </button>
            {emojiOpen && (
              <div className="nb-emojis" onMouseLeave={() => setEmojiOpen(false)}>
                {CALLOUT_EMOJIS.map((em) => (
                  <button
                    key={em}
                    type="button"
                    onClick={() => {
                      update({ emoji: em });
                      setEmojiOpen(false);
                    }}
                  >
                    {em}
                  </button>
                ))}
              </div>
            )}
          </span>
          {text("nb-p", t("mobile.block_callout"))}
        </div>
      );
    case "toggle":
      return (
        <div className="nb-toggle">
          <div className="nb-li">
            <button
              type="button"
              className={`nb-toggle-arrow${b.open ? " open" : ""}`}
              onClick={() => update({ open: !b.open })}
              aria-expanded={!!b.open}
              aria-label={t("mobile.block_toggle")}
            >
              ▸
            </button>
            {text("nb-p nb-summary", t("mobile.block_toggle"))}
          </div>
          {b.open && <ToggleBody value={b.body ?? ""} onChange={(v) => update({ body: v })} />}
        </div>
      );
    case "divider":
      return <hr className="nb-hr" />;
    case "image": {
      const src = b.ref ? resolveImage(b.ref) : null;
      return src ? <img className="nb-img" src={src} alt="" /> : <div className="nb-img-missing">{b.ref}</div>;
    }
    case "table":
      return <TableBlock rows={b.rows ?? [[""]]} onChange={(rows) => update({ rows })} />;
  }
}

function ToggleBody({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  return editing || !value ? (
    <Grow
      value={value}
      focusAt={editing ? "end" : null}
      className="nb-input nb-toggle-body"
      placeholder={t("mobile.toggleLeer")}
      onChange={(v) => onChange(v)}
      onBlur={() => setEditing(false)}
    />
  ) : (
    <div className="nb-text nb-toggle-body" tabIndex={0} onClick={() => setEditing(true)} onFocus={() => setEditing(true)}>
      {value.split("\n").map((l, i) => (
        <div key={i}>
          <Inline text={l} />
          {l === "" && <br />}
        </div>
      ))}
    </div>
  );
}

function TableBlock({ rows, onChange }: { rows: string[][]; onChange: (rows: string[][]) => void }) {
  const { t } = useTranslation();
  const width = rows[0]?.length ?? 1;
  const set = (r: number, c: number, v: string) => onChange(rows.map((row, i) => (i === r ? row.map((x, j) => (j === c ? v : x)) : row)));
  return (
    <div className="nb-table-wrap">
      <table className="nb-table">
        <tbody>
          {rows.map((row, r) => (
            <tr key={r} className={r === 0 ? "head" : ""}>
              {row.map((cell, c) => (
                <td key={c}>
                  <input value={cell} onChange={(e) => set(r, c, e.target.value)} aria-label={`${r + 1}/${c + 1}`} />
                </td>
              ))}
              {rows.length > 1 && (
                <td className="nb-table-x">
                  <button type="button" title={t("mobile.zeileWeg")} onClick={() => onChange(rows.filter((_, i) => i !== r))}>
                    ×
                  </button>
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
      <div className="nb-table-add">
        <button type="button" onClick={() => onChange([...rows, Array(width).fill("")])}>
          + {t("mobile.zeileNeu")}
        </button>
        <button type="button" onClick={() => onChange(rows.map((row) => [...row, ""]))}>
          + {t("mobile.spalteNeu")}
        </button>
        {width > 1 && (
          <button type="button" onClick={() => onChange(rows.map((row) => row.slice(0, -1)))}>
            − {t("mobile.spalteWeg")}
          </button>
        )}
      </div>
    </div>
  );
}

function BlockMenu({
  block: b,
  turn,
  onTurnList,
  onClose,
  onDelete,
  onDuplicate,
  onTurn,
}: {
  block: Block;
  turn: boolean;
  onTurnList: () => void;
  onClose: () => void;
  onDelete: () => void;
  onDuplicate: () => void;
  onTurn: (kind: BlockKind) => void;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const away = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const esc = (e: globalThis.KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [onClose]);
  return (
    <div className="nb-menu" ref={ref} role="menu">
      {!turn ? (
        <>
          <button type="button" role="menuitem" onClick={onDelete} className="danger">
            {t("mobile.blockLoeschen")}
          </button>
          <button type="button" role="menuitem" onClick={onDuplicate}>
            {t("mobile.blockDuplizieren")}
          </button>
          {isText(b) && (
            <button type="button" role="menuitem" onClick={onTurnList}>
              {t("mobile.blockUmwandeln")}
            </button>
          )}
        </>
      ) : (
        OPTIONS.filter((o) => o.kind && o.kind !== "image" && o.kind !== "table" && o.kind !== "divider").map((o) => (
          <button key={o.id} type="button" role="menuitem" onClick={() => onTurn(o.kind!)} className={o.kind === b.kind ? "on" : ""}>
            <span className="nb-slash-glyph" aria-hidden="true">
              {o.glyph}
            </span>
            {t(o.label)}
          </button>
        ))
      )}
    </div>
  );
}
