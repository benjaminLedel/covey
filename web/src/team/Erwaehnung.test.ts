import { describe, expect, it } from "vitest";
import { offeneErwaehnung } from "./Erwaehnung";

describe("offeneErwaehnung", () => {
  it("opens on @ at the start and after a space", () => {
    expect(offeneErwaehnung("@", 1)).toEqual({ start: 0, frage: "" });
    expect(offeneErwaehnung("hi @Ad", 6)).toEqual({ start: 3, frage: "ad" });
  });
  it("stays shut inside a word and after the handle is finished", () => {
    expect(offeneErwaehnung("mail@example", 12)).toBeNull();
    expect(offeneErwaehnung("@ada ", 5)).toBeNull();
  });
  it("reads only what stands before the caret", () => {
    expect(offeneErwaehnung("@ki und mehr", 3)).toEqual({ start: 0, frage: "ki" });
  });
  it("takes letters beyond ASCII", () => {
    expect(offeneErwaehnung("@Jürg", 5)).toEqual({ start: 0, frage: "jürg" });
  });
});
