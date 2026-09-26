import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import BlockEditor from "./BlockEditor";
import { useGerman } from "../test/render";

beforeEach(() => useGerman());

/* The web's block editor (#386): what one types comes out as the Markdown
   the app writes. */
function setup(initial: string) {
  const onChange = vi.fn();
  render(
    <BlockEditor initial={initial} onChange={onChange} resolveImage={() => null} upload={async () => "covey-media://x"} placeholder="…" />,
  );
  const last = () => onChange.mock.calls[onChange.mock.calls.length - 1]?.[0] as string;
  return { onChange, last };
}

const focused = () => document.activeElement as HTMLTextAreaElement;
const type = (value: string) => fireEvent.change(focused(), { target: { value, selectionStart: value.length } });
const key = (k: string, extra: object = {}) => fireEvent.keyDown(focused(), { key: k, ...extra });

describe("block editor", () => {
  it("turns into its source on a click and writes back Markdown", () => {
    const { last } = setup("# Titel\nText");
    fireEvent.click(screen.getByText("Text"));
    type("Text **fett**");
    expect(last()).toBe("# Titel\nText **fett**");
  });

  it("makes a heading from the slash menu", () => {
    const { last } = setup("");
    fireEvent.click(document.querySelector(".nb-text")!);
    type("/h2");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    key("Enter");
    type("Abschnitt");
    expect(last()).toBe("## Abschnitt");
  });

  it("turns '- ' at the start into a bullet, and Enter continues the list", () => {
    const { last } = setup("");
    fireEvent.click(document.querySelector(".nb-text")!);
    type("- ");
    type("eins");
    focused().setSelectionRange(4, 4);
    key("Enter");
    type("zwei");
    expect(last()).toBe("- eins\n- zwei");
  });

  it("joins a block with the one before on Backspace at its start", () => {
    const { last } = setup("Hallo\nWelt");
    fireEvent.click(screen.getByText("Welt"));
    focused().setSelectionRange(0, 0);
    key("Backspace");
    expect(last()).toBe("HalloWelt");
  });

  it("ticks a checklist item", () => {
    const { last } = setup("- [ ] Angebot schicken");
    fireEvent.click(screen.getByRole("checkbox"));
    expect(last()).toBe("- [x] Angebot schicken");
  });

  it("deletes a block from the handle's menu", () => {
    const { last } = setup("eins\nzwei");
    fireEvent.click(screen.getAllByLabelText(/Block: klicken/)[0]);
    fireEvent.click(screen.getByText("Löschen"));
    expect(last()).toBe("zwei");
  });
});

describe("inline text", () => {
  it("leaves underscores inside a word alone", () => {
    setup("Datei Ledel_21_09_2026.pdf und _betont_");
    expect(screen.getByText(/Ledel_21_09_2026\.pdf/)).toBeInTheDocument();
    expect(screen.getByText("betont").tagName).toBe("EM");
  });
});
