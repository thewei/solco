import { expect, test } from "bun:test";
import { takesKeyboard } from "../src/feedback/focus";

test("only text fields raise the keyboard, so choosing an option never scrolls the form", () => {
  expect(takesKeyboard({ tagName: "TEXTAREA" })).toBe(true);
  expect(takesKeyboard({ tagName: "INPUT" })).toBe(true);
  expect(takesKeyboard({ tagName: "BUTTON" })).toBe(false);
  expect(takesKeyboard({ tagName: "DIV" })).toBe(false);
  expect(takesKeyboard(null)).toBe(false);
  expect(takesKeyboard(undefined)).toBe(false);
});
