#!/usr/bin/env node
// Test-declaration inventory only. Never runs or imports Shepherdr runtime code.
// Use: node scripts/generate-shepherdr-2.1-test-index.mjs --source=/checkout/v2.1.0
// Add --check to verify docs/shepherdr-2.1-test-cases.jsonl is unchanged.
// Requires frontend/node_modules/typescript (installed as part of normal frontend setup).
import { createRequire } from "node:module";
import { createHash } from "node:crypto";
import { readdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const repo = resolve(fileURLToPath(new URL("..", import.meta.url)));
const ts = createRequire(join(repo, "frontend", "package.json"))("typescript");
const sha = "8df9402c63ebd97ae05ed309b0444e2d755cacc6";
const args = process.argv.slice(2);
const opt = (name) => args.find((x) => x.startsWith("--" + name + "="))?.split("=").slice(1).join("=");
const source = opt("source") && resolve(opt("source"));
const output = resolve(opt("output") || join(repo, "docs", "shepherdr-2.1-test-cases.jsonl"));
if (!source || !existsSync(join(source, "test"))) {
  console.error("Requires --source=absolute-path-to-Shepherdr-v2.1.0");
  process.exit(2);
}
const git = (...argv) => spawnSync("git", argv, {cwd: source, encoding:"utf8"});
const rev = git("rev-parse", "HEAD");
const dirty = git("status", "--porcelain", "--untracked-files=no");
if (rev.status || rev.stdout.trim() !== sha || dirty.status || dirty.stdout.trim()) {
  console.error("Refusing unpinned or dirty source (expected Shepherdr 2.1.0 SHA " + sha + ")");
  process.exit(2);
}

function policy(file) {
  const name = file.replace(/\.test\.tsx?$/, "");
  if (!name.startsWith("core-")) {
    if (name === "layout-automations")
      return ["ui-scenario", "D", "automations UI parity", "desktop+mobile", "Verify existing Automations and Assistants rules UX and migration compatibility, not Shepherdr pixels."];
    if (/^(sw|push|native-back|browser|voice|secrets|plugins|native|viewport|terminal)$/.test(name))
      return ["client-adaptation", "E", "platform/client", "mobile+desktop", "Compare user-observable contract with existing Solco mobile/WebView/Host, not Shepherdr's runtime/UI implementation."];
    if (/^(layout-(sending|answers|tasks|needs|attention|automations|iphone)|chat|needs|alerts|attention|outbox|typing|rows)$/.test(name))
      return ["ui-scenario", "B", "shared Chat and Assistants UI", "desktop+mobile", "Re-test relevant behavior through existing shared Chat/Go bridge/Expo UI; do not copy Shepherdr DOM or CSS."];
    return ["ui-scenario", "E", "non-MVP client UX", "desktop+mobile", "Preserve scenario and compare with existing Solco UI at case review; unneeded Shepherdr-specific pixels/actions require explicit non-applicability."];
  }
  const x = name.slice(5);
  if (/^(pilot-(sandbox|ssh|tls|proxy|gateway|backend|runtime|native|capture)|browser-protocol|browser-session|browsers|plugin-(build|production|assets|updates)|plugins)$/.test(x))
    return ["platform-decision", "E", "OS/hc/plugin-specific", "architecture", "Must explicitly record unsupported/macOS safety or existing Solco equivalent; cannot port hc/Bun/Linux isolation assumptions."];
  if (/^(client|protocol|terminal|terminal-client|screens|voice|remote|launcher|launch-matrix)$/.test(x))
    return ["herdr-adaptation", "A", "Herdr runtime transport", "desktop", "Translate safety/idempotency to verified Herdr 0.9.5 RPC/Session/Pane; do not copy hc frame protocol."];
  if (/^(automations|database-lock|source|ids|id-settings|failed-starts|logs|secrets|secret-helper|settings|provider-errors|provider-retries|native-ends)$/.test(x))
    return ["go-parity", "A", "store/receipts/automation/launch", "go-core", "Translate assertion to durable Go transaction or bounded Herdr launch behavior; add restart fault injection."];
  if (/^(heartbeats|health|news|capacity|plan-limits|pilot-(wakes|completion|usage|records|lifecycle|policy)|activity|unread)$/.test(x))
    return ["go-parity", "C", "Heartbeat/budget/events", "go-core", "Translate persistence/event/budget rules; Linux-only autonomy stays denied on macOS."];
  if (/^(tasks|task-(options|overview|processes|relocation)|coordinator-restart|dependencies|worktree|worktree-includes|git|github|github-pr|github-merges|github-logs|review-reader|new-repository|places|space-controls|pilot-(tasks|git|repositories|home|launch)|operator)$/.test(x))
    return ["go-parity", "E", "Coordinator/Task/Git/Review", "go-core", "Use real Git scratch fixture and Herdr agent identity, preserving review/revision/approval refusals."];
  if (/^(mailbox|native-asks|questions|cards|needs|commands|own-commands|child-steps|chat|artifacts|artifact-check|home|human-name|model|notifications|pilot-(commands|mail))$/.test(x))
    return ["go-parity", "B", "Assistant/approval/inbox", "go-core", "Port permission and effect semantics; reuse Solco Chat presentation instead of new chat engine."];
  return ["go-parity", "E", "remaining core domain", "go-core", "Case-level review must choose Go contract or explicit non-applicability before closing parity."];
}

function target(e) {
  if (ts.isIdentifier(e) && ["test","it","describe"].includes(e.text)) return { name: e.text, variant: "" };
  if (ts.isPropertyAccessExpression(e) && ts.isIdentifier(e.expression) && ["test","it","describe"].includes(e.expression.text))
    return { name: e.expression.text, variant: e.name.text };
  if (ts.isCallExpression(e) && ts.isPropertyAccessExpression(e.expression) && ts.isIdentifier(e.expression.expression) &&
      ["test","it","describe"].includes(e.expression.expression.text) && ["skipIf","each"].includes(e.expression.name.text))
    return { name: e.expression.expression.text, variant: e.expression.name.text };
  return null;
}
function title(a,sf) {
  if (!a) return {value:"<missing>",dynamic:true};
  if (ts.isStringLiteralLike(a) || ts.isNoSubstitutionTemplateLiteral(a)) return {value:a.text,dynamic:false};
  return {value:a.getText(sf).slice(0,600),dynamic:true};
}
const testDir = join(source,"test");
const files = readdirSync(testDir).filter((n) => /\.test\.tsx?$/.test(n)).sort();
const rows=[];
for (const file of files) {
  const contents=readFileSync(join(testDir,file),"utf8");
  const sf=ts.createSourceFile(file,contents,ts.ScriptTarget.Latest,true,file.endsWith(".tsx")?ts.ScriptKind.TSX:ts.ScriptKind.TS);
  if (sf.parseDiagnostics.length) throw new Error("TypeScript parse error "+file);
  const [disposition,phase,domain,owner,reason]=policy(file);
  let ordinal=0;
  function walk(n,groups=[],loop=false){
    if(ts.isCallExpression(n)) {
      const info=target(n.expression);
      if(info) {
        // Builder calls are nested below their real invocation; count only the outer call.
        if(ts.isCallExpression(n.parent) && n.parent.expression===n && ["each","skipIf"].includes(info.variant)) return;
        if(info.name==="describe") {
          const t=title(n.arguments[0],sf);
          for(const arg of n.arguments.slice(1))walk(arg,[...groups,t.value],loop);
          return;
        }
        if(info.name==="test" || info.name==="it") {
          const t=title(n.arguments[0],sf);
          const line=sf.getLineAndCharacterOfPosition(n.getStart(sf)).line+1;
          const ref="test/"+file;
          const id="S21-"+createHash("sha256").update(ref+":"+line+":"+t.value).digest("hex").slice(0,12);
          // Critical durable-claim cases start in A. Other rules/events + UI converge in D.
          let deliveryPhase=phase;
          if(file==="core-automations.test.ts")deliveryPhase=/claims before launch|durable command|interrupted claim/.test(t.value)?"A":"D";
          if(file==="core-tasks.test.ts"&&/merge requires a human card approval|merge card carries the exact commit|merge card never shows the checks of another commit/.test(t.value))deliveryPhase="B";
          ++ordinal;
          rows.push({
            id,source_file:ref,source_line:line,suite:groups.join(" / "),scenario:t.value,
            declaration:info.variant?info.name+"."+info.variant:info.name,
            parameterized:loop||t.dynamic||info.variant==="each",
            disposition,phase:deliveryPhase,domain
          });
          return;
        }
      }
    }
    const nextLoop=loop||ts.isForOfStatement(n)||ts.isForStatement(n)||ts.isForInStatement(n)||
      ts.isWhileStatement(n)||ts.isDoStatement(n);
    ts.forEachChild(n,(child)=>walk(child,groups,nextLoop));
  }
  walk(sf);
}
if(files.length!==168||rows.length!==1193)throw new Error("Incomplete inventory (files="+files.length+", declarations="+rows.length+")");
if(new Set(rows.map((r)=>r.id)).size!==rows.length)throw new Error("duplicate case ids");
const body=rows.map((r)=>JSON.stringify(r)).join("\n")+"\n";
if(args.includes("--check")) {
  if(!existsSync(output)||readFileSync(output,"utf8")!==body)throw new Error("Inventory stale: "+output);
}else writeFileSync(output,body);
const byPhase={},byDisposition={};
for(const r of rows){byPhase[r.phase]=(byPhase[r.phase]||0)+1;byDisposition[r.disposition]=(byDisposition[r.disposition]||0)+1;}
console.log(JSON.stringify({sha,files:files.length,declarations:rows.length,parameterized:rows.filter((r)=>r.parameterized).length,byPhase,byDisposition,output,check:args.includes("--check")},null,2));
