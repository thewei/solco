import { expect, test } from "bun:test";
import { appendQuote, choiceFor, isLongText, quoteBlock } from "../src/feedback/compose";

test("a passage becomes a Markdown quote, line by line", () => {
  expect(quoteBlock("one\n\ntwo")).toBe("> one\n>\n> two");
  expect(quoteBlock("  spaced  ")).toBe("> spaced");
});

test("a quote lands after what is typed, and an empty selection changes nothing", () => {
  expect(appendQuote("", "why")).toBe("> why\n\n");
  expect(appendQuote("yes, but  \n", "why")).toBe("yes, but\n\n> why\n\n");
  expect(appendQuote("typed", "   ")).toBe("typed");
});

const qs = [
  { id: "a", type: "single", options: ["x", "y"] },
  { id: "t", type: "text" },
  { id: "b", type: "multiple", options: ["p", "q", "r"] },
];

test("a number key picks in the touched question when it has that option, else in the first one that has it", () => {
  expect(choiceFor(qs, "b", 3)?.id).toBe("b");
  expect(choiceFor(qs, "a", 3)?.id).toBe("b"); // a has two options only
  expect(choiceFor(qs, "t", 1)?.id).toBe("a"); // a text question takes no number
  expect(choiceFor(qs, null, 2)?.id).toBe("a");
  expect(choiceFor(qs, null, 4)).toBeUndefined();
});

test("long means a screenful: over 420 characters or 8 lines", () => {
  expect(isLongText("short")).toBe(false);
  expect(isLongText("x".repeat(421))).toBe(true);
  expect(isLongText(Array(9).fill("l").join("\n"))).toBe(true);
});
