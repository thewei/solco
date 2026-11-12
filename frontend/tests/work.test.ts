import { expect, test } from "bun:test";
import { DRAFT_KEY, isOffline, KEEP_MS, loadPersisted, savePersisted, OFFLINE_MARK } from "../src/feedback/work";

function memory(initial?: string) {
  const data = new Map<string, string>(initial === undefined ? [] : [[DRAFT_KEY, initial]]);
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    raw: () => data.get(DRAFT_KEY),
  };
}

test("drafts, picked context and the outbox survive a save and a load", () => {
  const store = memory();
  const p = loadPersisted(store, 1000);
  p.drafts.a1 = { q1: { selected: ["main"], custom: "" }, q2: { selected: [], custom: "note" }, q3: { selected: [], custom: "" } };
  p.refs.a1 = [{ kind: "file", name: "docs/x.md" }];
  p.outbox.a1 = { at: 900, answers: { q1: { selected: ["main"], custom: "" } }, refs: [] };
  p.seen.a1 = 1000;
  savePersisted(store, p);
  const back = loadPersisted(store, 2000);
  expect(Object.keys(back.drafts.a1!)).toEqual(["q1", "q2"]); // the empty q3 draft is not written
  expect(back.refs.a1).toEqual([{ kind: "file", name: "docs/x.md" }]);
  expect(back.outbox.a1?.answers.q1?.selected).toEqual(["main"]);
});

test("work nobody touched for three days is dropped on load, fresh work stays", () => {
  const store = memory();
  const p = loadPersisted(store, 0);
  p.drafts.old = { q: { selected: ["x"], custom: "" } };
  p.drafts.fresh = { q: { selected: ["y"], custom: "" } };
  p.seen.old = 0;
  p.seen.fresh = KEEP_MS;
  savePersisted(store, p);
  const back = loadPersisted(store, KEEP_MS + 1000);
  expect(back.drafts.old).toBeUndefined();
  expect(back.drafts.fresh?.q?.selected).toEqual(["y"]);
});

test("malformed storage never throws and yields empty work", () => {
  for (const raw of ["not json", "null", "[]", '{"seen":5}', '{"seen":{"a":1},"drafts":{"a":{"q":{"selected":"x","custom":3}}}}']) {
    const p = loadPersisted(memory(raw), 10);
    expect(Object.keys(p.outbox)).toEqual([]);
    expect(Object.keys(p.drafts.a ?? {})).toEqual([]);
  }
  expect(loadPersisted(undefined).drafts).toEqual({});
});

test("saving without storage, or into a full one, does not throw", () => {
  const p = loadPersisted(undefined);
  p.drafts.a = { q: { selected: ["x"], custom: "" } };
  expect(() => savePersisted(undefined, p)).not.toThrow();
  const full = { getItem: () => null, setItem: () => { throw new Error("quota"); } };
  expect(() => savePersisted(full, p)).not.toThrow();
});

test("only the phone bridge's offline marker counts as unreachable", () => {
  expect(isOffline(new Error(`${OFFLINE_MARK}Can't reach your Mac.`))).toBe(true);
  expect(isOffline(new Error("feedback_final: That question is already closed."))).toBe(false);
  expect(isOffline("offline: x")).toBe(true);
  expect(isOffline(new Error("Network request failed"))).toBe(false);
});
