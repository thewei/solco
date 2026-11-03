#!/usr/bin/env node
// Audit-only: validates frozen Shepherdr 2.1 case coverage and Solco-native requirements.
// No tests are claimed to have passed merely because these indexes are syntactically valid.
//   node scripts/check-shepherdr-2.1-parity.mjs
//   node scripts/check-shepherdr-2.1-parity.mjs --gate=A1
//   node scripts/check-shepherdr-2.1-parity.mjs --gate=all
import { existsSync, readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";

const repo = resolve(fileURLToPath(new URL("..", import.meta.url)));
const locate = (file) => join(repo, "docs", file);
const files = {
  source: locate("shepherdr-2.1-test-cases.jsonl"),
  decisions: locate("shepherdr-2.1-parity-decisions.jsonl"),
  own: locate("solco-assistants-required-cases.jsonl")
};
const readLines = (p) => readFileSync(p, "utf8").split("\n").filter((x) => x.trim()).map((line, i) => {
  try { return JSON.parse(line); }
  catch (e) { throw new Error(p + ":" + (i + 1) + ": " + String(e)); }
});
for (const file of Object.values(files)) if (!existsSync(file)) throw new Error("Missing test inventory: " + file);

const source = readLines(files.source);
const decisions = readLines(files.decisions);
const own = readLines(files.own);
const errors = [];
const statuses = new Set(["planned", "verified", "not_applicable"]);
const actions = new Set(["go", "herdr_adapter", "existing_ui", "not_applicable", "unsupported_contract"]);
const batches = new Set(["A1", "A2", "B1", "B2", "B3", "C1", "C2", "D1", "D2", "E1", "E2", "E3", "E4"]);
const sourceById = new Map(source.map((x) => [x.id, x]));
if (source.length !== 1193 || sourceById.size !== source.length) errors.push("Expected exactly 1193 unique pinned source test declarations");
if (new Set(source.map((x) => x.source_file)).size !== 168) errors.push("Expected 168 pinned source test files");
if (own.length !== 31 || new Set(own.map((x) => x.id)).size !== own.length) errors.push("Expected 31 unique Solco-native required scenarios");

const decisionMap = new Map();
for (const row of decisions) {
  if (!sourceById.has(row.id)) errors.push("Unknown source case: " + row.id);
  if (decisionMap.has(row.id)) errors.push("Duplicate source case decision: " + row.id);
  decisionMap.set(row.id, row);
  if (!statuses.has(row.status)) errors.push(row.id + ": invalid status");
  if (!actions.has(row.action)) errors.push(row.id + ": invalid action");
  if (!batches.has(row.batch)) errors.push(row.id + ": invalid batch");
  if (typeof row.rationale !== "string" || row.rationale.length < 18) errors.push(row.id + ": needs a case-specific rationale");
  if (row.status === "not_applicable" && row.action !== "not_applicable")
    errors.push(row.id + ": unsupported capability requires verified negative test, not silent exclusion");
  if (row.status === "verified" && row.action === "not_applicable")
    errors.push(row.id + ": use status=not_applicable for a reviewed exclusion");
  if (["verified", "not_applicable"].includes(row.status) && row.reviewed_assertions !== true)
    errors.push(row.id + ": review all original assertions and parameter expansions before closure");
  if (row.status === "not_applicable" && (!row.evidence || typeof row.evidence !== "string"))
    errors.push(row.id + ": an exclusion needs recorded source review/evidence");
  if (row.status === "verified") {
    if (typeof row.evidence !== "string" || row.evidence.length < 12) errors.push(row.id + ": actual verification evidence is required");
    const bad = targetProblem(row.target_test);
    if (bad) errors.push(row.id + ": " + bad);
  }
}


function targetProblem(target) {
  if (typeof target !== "string") return "no target_test";
  const [file, name] = target.split("#");
  const full = join(repo, file);
  if (!existsSync(full)) return "target file does not exist: " + file;
  if (name && !readFileSync(full, "utf8").includes(name)) return "target test " + name + " is not in " + file;
  return null;
}

const ownMap = new Map();
for (const row of own) {
  if (!/^SOLCO-[AMWRDE]\d{2}$/.test(row.id)) errors.push("Invalid Solco-owned case: " + row.id);
  if (ownMap.has(row.id)) errors.push("Duplicate Solco-owned case: " + row.id);
  ownMap.set(row.id, row);
  if (!batches.has(row.batch)) errors.push(row.id + ": invalid batch");
  if (typeof row.scenario !== "string" || row.scenario.length < 12) errors.push(row.id + ": scenario must be described");
  if (!["planned", "verified"].includes(row.status)) errors.push(row.id + ": Solco-native cases are mandatory; exclusion is not allowed");
  if (row.status === "verified") {
    const bad = targetProblem(row.target_test);
    if (bad) errors.push(row.id + ": " + bad);
    if (typeof row.evidence !== "string" || row.evidence.length < 12) errors.push(row.id + ": verification evidence is required");
  }
}
if (errors.length) {
  console.error(errors.slice(0, 50).join("\n"));
  process.exit(1);
}

const counts = (rows) => rows.reduce((obj, x) => {
  obj[x.status] = (obj[x.status] ?? 0) + 1;
  return obj;
}, {});
const gate = process.argv.find((arg) => arg.startsWith("--gate="))?.slice(7);
if (gate && gate !== "all" && !batches.has(gate)) throw new Error("Unknown gate: " + gate);
const selectedSource = gate === "all" ? source.map((x) => x.id) :
  gate ? decisions.filter((x) => x.batch === gate).map((x) => x.id) : [];
const selectedOwn = gate === "all" ? own : gate ? own.filter((x) => x.batch === gate) : [];
const sourceDone = selectedSource.filter((id) => {
  const row = decisionMap.get(id);
  return row && ["verified", "not_applicable"].includes(row.status);
}).length;
const ownDone = selectedOwn.filter((row) => row.status === "verified").length;
const expected = selectedSource.length + selectedOwn.length;
console.log(JSON.stringify({
  shepherdr: {
    files: 168, static_test_declarations: source.length,
    manually_triaged: decisions.length, unreviewed: source.length - decisions.length,
    status: counts(decisions)
  },
  solco_required: { total: own.length, status: counts(own) },
  gate: gate ?? "status_only",
  selected: expected,
  complete: sourceDone + ownDone,
  incomplete: expected - sourceDone - ownDone
}, null, 2));
if (gate && (!expected || sourceDone !== selectedSource.length || ownDone !== selectedOwn.length)) {
  console.error("Gate not complete: source cases require verified tests or reviewed inapplicability; all Solco-native requirements require verified tests.");
  process.exit(1);
}
