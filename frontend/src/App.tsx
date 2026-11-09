import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { event as bridgeEvent } from "mygo-runtime";
import { chat, events, type ChatBridgeState, type ChatDelta, type ChatCatalog, type ChatCatalogEntry } from "./mygo";
import { Icon, StopIcon } from "./icons";
import { selectContents, useLinkHint } from "./native";
import { OutboxRow, TurnRow, userDisplay, type Outbox } from "./turn";
import { ContextControl } from "./context";
import { TocRail } from "./toc";
import { SubagentContext, type SubagentContextValue } from "./subagents";
import { DRAWER_DEFAULT, DRAWER_MAX, DRAWER_MIN, SubagentDrawer } from "./drawer";
import { turnText, useTurnList } from "./turnlist";
import { AttachChips, IMAGE_EXT, ModelPanel, PlusPanel, SlashPanel, fileToBase64, slashMatches } from "./composer";
import { ChatComposerBox, ChatComposerTextarea, ChatComposerToolbar, useComposerAutoSize } from "./composer-surface";
import { BlockedDock, QuestionDock, SessionState, TasksTray, WorkingLine, isFreeTextOption, planFinished } from "./dock";
import { QueueCard } from "./queue-card";
import { loadStored, saveStored } from "./drafts";
import { applyChatDelta } from "./chat-delta";
import "./chat.css";

/**
 * The Chat body of the /workspace WorkspacePrimarySurface, hosted by the
 * native window inside ui.WebView. The conversation runtime, send path and
 * queue stay in Go (internal/agent + internal/herdr): this page renders the
 * bridge projection and calls the bound service — presentation only.
 */

// The bridge mirrors conversation.PromptDisposition as "ready" | "queue" |
// "terminal" | "unknown"; the generated client types it as string.
// `dockAbove` is the host page's own dock (the Projects page puts "needs you"
// and the plan/approve decision there), drawn first in the composer column.
// The native sidebar's and quick panel's nested sub-agent rows ask for the
// same drawer the page's cards open: the shell emits chat:subagent-open
// (chatSubagentOpen, chat_subagents.go) once the row's conversation is the
// bound one, so the id is always listed. The typed accessor lands in
// mygo.ts when `mygo generate` next runs on a tree that compiles; until
// then the page subscribes through the runtime directly.
const chatSubagentOpen = bridgeEvent<string>("chat:subagent-open");
/** shortModel is a model id as the composer bar shows it: no provider prefix, no date or "latest" suffix. */
function shortModel(id: string): string {
  return id.slice(id.lastIndexOf("/") + 1).replace(/-(\d{8}|latest)$/, "");
}

/**
 * OfflineChat is the page's composer before any Agent is there to talk to (an
 * Assistant whose Coordinator is not running): the SAME composer and column,
 * so the conversation looks and behaves the same before and after the first
 * message — only what needs a live Agent (the + menu, the model panel, the
 * context ring) is held back. `send` owns whatever is missing: it starts the
 * Agent, waits until the Chat is on it and delivers the prompt, resolving with
 * the Chat's state. A rejection is the ordinary "Not delivered · Retry · Edit"
 * row, so a failed start never loses the text. The host drops the prop once the
 * Chat is bound; the same instance then carries on as the live Chat.
 */
export interface OfflineChat {
  /** The draft's storage key (the Assistant id): the box keeps its text per conversation. */
  key: string;
  /** What the log shows in place of the new-session invitation (hidden once a prompt is in flight). */
  empty: React.ReactNode;
  placeholder: string;
  /** The Agent choice, drawn where the live Chat draws its model chip. */
  chip?: React.ReactNode;
  send: (text: string) => Promise<ChatBridgeState>;
  /**
   * The host bound the Chat read-only to the Assistant's previous Coordinator
   * conversation: the log shows those turns above the composer until the live
   * Coordinator takes the same page over.
   */
  history?: boolean;
  /** The composer was clicked: the host may start the Coordinator now, so it is up by the time the person has typed. */
  onFocus?: () => void;
}

/**
 * LocalCommand is a `/command` the host page answers itself instead of sending
 * to an agent (the Assistants page's `/memory`, which opens the memory files in
 * the person's editor). It shows in the `/` list, wins over an agent's command of
 * the same name, and only counts at the start of a draft.
 */
export interface LocalCommand {
  /** The command with its slash, e.g. "/memory". */
  name: string;
  detail: string;
  run: () => void;
}

/** The kind of a LocalCommand's row in the `/` list (agentcatalog kinds never use it). */
const LOCAL_KIND = "local";

const OFFLINE_DRAFT = (key: string) => `as-draft:${key}`;

/** offlineState is the Chat's projection while there is no Agent: an empty, ready conversation that keeps the app's appearance. */
function offlineState(base: ChatBridgeState | null, history = false): ChatBridgeState {
  const past = history && base?.readOnly === true;
  return {
    seq: base?.seq ?? 0,
    conversationId: "",
    loading: past ? base.loading : false,
    hasOlder: past ? base.hasOlder : undefined,
    disposition: "ready",
    theme: base?.theme ?? "",
    highContrast: base?.highContrast,
    serifProse: base?.serifProse,
    voiceSource: base?.voiceSource,
    outline: false,
    features: { voice: true, attach: false, queue: false, reorder: false, edit: false, subagentDrawer: false },
    turns: past ? base.turns : [],
  };
}

export function App({ dockAbove, offline, localCommands }: { dockAbove?: React.ReactNode; offline?: OfflineChat; localCommands?: LocalCommand[] } = {}) {
  const [bridgeState, setStateRaw] = useState<ChatBridgeState | null>(null);
  const isOffline = offline !== undefined;
  const offlineHistory = offline?.history === true;
  const state = useMemo(() => (isOffline ? offlineState(bridgeState, offlineHistory) : bridgeState), [isOffline, bridgeState, offlineHistory]);
  // Synchronous watermark: snapshots, full notifications and delta patches
  // must use one ordering gate even when React batches state updates.
  const stateRef = useRef<ChatBridgeState | null>(null);
  const setState = useCallback((next: ChatBridgeState) => {
    const previous = stateRef.current;
    if (previous && next.seq < previous.seq) return;
    stateRef.current = next;
    setStateRaw(next);
  }, []);
  const repairingRef = useRef(false);
  const onChatDelta = useCallback((delta: ChatDelta) => {
    const previous = stateRef.current;
    if (previous && delta.seq <= previous.seq) return;
    const next = applyChatDelta(previous, delta);
    if (next) {
      setState(next);
      return;
    }
    // Missing event or an intervening snapshot: resolve through the
    // canonical bridge, not through a guessed tail or stale message index.
    if (repairingRef.current) return;
    repairingRef.current = true;
    void chat.snapshot().then(setState).catch(() => {}).finally(() => {
      repairingRef.current = false;
    });
  }, [setState]);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [showJump, setShowJump] = useState(false);
  // Turns that arrived while the user was reading above (the pill's count).
  const [unread, setUnread] = useState(0);
  // ⌘F: the in-conversation find bar (WebKit's own window.find does the work).
  const [finding, setFinding] = useState(false);
  const [findQuery, setFindQuery] = useState("");
  // Native feel: a hairline under the title bar once the log has scrolled,
  // and the target of the link under the pointer (Safari's status pill).
  const [scrolled, setScrolled] = useState(false);
  const linkHint = useLinkHint();
  const findRef = useRef<HTMLInputElement>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [completions, setCompletions] = useState<string[]>([]);
  const [completionPick, setCompletionPick] = useState(0);
  const [recording, setRecording] = useState(false);
  const [transcribing, setTranscribing] = useState(false);
  const [voiceError, setVoiceError] = useState("");
  // Seconds of the running recording (the Listening… clock).
  const [recordSecs, setRecordSecs] = useState(0);
  // Set by Cancel: the recorder's stop then discards the clip.
  const cancelledRef = useRef(false);
  // The last clip whose transcription failed, kept so Retry does not ask the
  // user to say it again; and the microphone permission's two visible states.
  const failedClipRef = useRef<{ base64: string; mime: string } | null>(null);
  const [clipFailed, setClipFailed] = useState(false);
  const [micAsking, setMicAsking] = useState(false);
  const [micDenied, setMicDenied] = useState(false);
  const [interrupting, setInterrupting] = useState(false);
  // How full the context window is (the ring's own reading), and the conversation whose
  // "nearly full" line the user closed; it returns once a compact brings the window under 70%.
  const [ctxFraction, setCtxFraction] = useState<number | null>(null);
  const [ctxHintOff, setCtxHintOff] = useState("");
  // The prompt in flight: shown at once as "Sending…", and kept (with Retry /
  // Edit) when the agent did not take it, so a failure never loses the text.
  const [outbox, setOutbox] = useState<Outbox | null>(null);
  const [textPrompt, setTextPrompt] = useState<{ id: string; option: string } | null>(null);
  const [textAnswer, setTextAnswer] = useState("");
  const [answering, setAnswering] = useState(false);
  const [catalog, setCatalog] = useState<ChatCatalog | null>(null);
  const [panel, setPanel] = useState<"plus" | "model" | "context" | "tasks" | "queue" | null>(null);
  // One surface above the composer is open at a time (model, + menu, context, Tasks, Queue):
  // opening one folds the others. undefined = nobody asked, the card keeps its own default.
  const dockForced = (name: "tasks" | "queue") => (panel === name ? true : panel !== null ? false : undefined);
  // Escape closes a Tasks/Queue card the user opened (the other panels bind their own Escape);
  // an auto-opened card never takes the Escape that stops the agent.
  useEffect(() => {
    if (panel !== "tasks" && panel !== "queue") return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.isComposing) return;
      // Escape inside a queued message's editor discards that edit, not the card.
      if (event.target instanceof Element && event.target.closest(".qcEdit")) return;
      event.preventDefault();
      event.stopPropagation();
      setPanel(null);
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [panel]);
  const [slashPick, setSlashPick] = useState(0);
  const [slashOff, setSlashOff] = useState(false);
  const [files, setFiles] = useState<string[]>([]);
  const [dragging, setDragging] = useState(false);
  const [switching, setSwitching] = useState(false);
  const previews = useRef(new Map<string, string>());
  const composingRef = useRef(false);
  const logRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const meterRef = useRef<HTMLCanvasElement>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const meterRafRef = useRef(0);
  const stickToBottom = useRef(true);
  // Which sent prompt ↑ is showing (0 = newest); null while typing normally.
  const recallRef = useRef<number | null>(null);
  const turnsRef = useRef(0);

  const refresh = useCallback(() => {
    chat.snapshot().then(setState).catch(() => {});
  }, []);

  useEffect(() => {
    refresh();
    // Events emitted before the DOM was ready are replayed by the runtime;
    // every later projection change arrives through chatUpdated.
    const off = events.chatUpdated.on(setState);
    const offDelta = events.chatDelta.on(onChatDelta);
    return () => { off(); offDelta(); };
  }, [refresh, setState, onChatDelta]);
  // The host hands over from the offline composer to the live Chat in place:
  // read the bound conversation at once instead of waiting for its next event.
  const wasOffline = useRef(isOffline);
  // Before a conversation exists the draft lives under the host's own key.
  const offlineKey = offline ? OFFLINE_DRAFT(offline.key) : "";
  const lastOfflineKey = useRef(offlineKey);
  if (offlineKey) lastOfflineKey.current = offlineKey;
  useEffect(() => {
    if (wasOffline.current && !isOffline) {
      refresh();
      // The draft rides on into the conversation's own key; the offline copy
      // would otherwise come back as a stale draft once the agent stops.
      if (lastOfflineKey.current) saveStored(lastOfflineKey.current, "", "");
    }
    wasOffline.current = isOffline;
  }, [isOffline, refresh]);
  useEffect(() => {
    if (offlineKey) setDraft(loadStored(offlineKey, ""));
  }, [offlineKey]);
  useEffect(() => {
    if (offlineKey) saveStored(offlineKey, draft, "");
  }, [draft, offlineKey]);

  // Follow the app-resolved appearance (Settings -> General -> Appearance,
  // carried by the bridge state once it arrives); until then, a theme the
  // entry pinned (mobile.html: the phone is dark-only), else the system
  // preference.
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const theme = stateRef.current?.theme;
      document.documentElement.dataset.theme =
        theme ?? document.documentElement.dataset.themePin ?? (media.matches ? "dark" : "light");
    };
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, []);
  useEffect(() => {
    if (state?.theme) {
      document.documentElement.dataset.theme = state.theme;
    }
  }, [state?.theme]);
  // Increase contrast / Serif chat text (Settings -> General) ride the bridge
  // state and switch the page's token sets.
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.contrast = state?.highContrast ? "high" : "std";
    root.dataset.prose = state?.serifProse ? "serif" : "sans";
  }, [state?.highContrast, state?.serifProse]);

  // Sticky bottom: follow the tail until the user scrolls up; turns added
  // while scrolled up raise the "New messages" pill instead. A layout effect,
  // so a conversation's first frame is already at its end — never painted at
  // the top and then jumped.
  useLayoutEffect(() => {
    const log = logRef.current;
    const count = state?.turns.length ?? 0;
    if (log && stickToBottom.current) {
      log.scrollTop = log.scrollHeight;
      setShowJump(false);
      setUnread(0);
    } else if (count > turnsRef.current) {
      setShowJump(true);
      setUnread((current) => current + count - turnsRef.current);
    }
    turnsRef.current = count;
  }, [state]);

  // Prepend anchoring: when a history page lands above, keep the viewport
  // pinned to the messages the user was reading.
  const headRef = useRef("");
  const heightRef = useRef(0);
  // A different conversation opens at its newest message, wherever the last
  // one was scrolled to.
  useLayoutEffect(() => {
    stickToBottom.current = true;
  }, [state?.conversationId]);
  useLayoutEffect(() => {
    const log = logRef.current;
    if (!log) return;
    const first = state?.turns[0];
    const head = first ? `${first.user ? "u" : "a"}:${first.tsMs ?? ""}:${(first.text ?? first.narration ?? "").slice(0, 32)}` : "";
    if (headRef.current && head !== headRef.current && log.scrollHeight > heightRef.current) {
      log.scrollTop += log.scrollHeight - heightRef.current;
    }
    headRef.current = head;
    heightRef.current = log.scrollHeight;
  }, [state]);

  // The reader's own upward gesture (wheel, finger drag, PageUp/↑) lets go of the
  // tail at once, even when it moved less than the 48px "at the bottom" slack: the
  // next streamed frame must not pull a re-read line back down. Until the scroll
  // settles, only the true bottom takes the tail again.
  const upAt = useRef(0);
  const touchY = useRef(0);
  const letGo = () => {
    upAt.current = performance.now();
    stickToBottom.current = false;
  };
  const onLogWheel = (event: React.WheelEvent) => {
    if (event.deltaY < 0) letGo();
  };
  const onLogTouchStart = (event: React.TouchEvent) => {
    touchY.current = event.touches[0]?.clientY ?? 0;
  };
  const onLogTouchMove = (event: React.TouchEvent) => {
    const y = event.touches[0]?.clientY ?? 0;
    if (y > touchY.current + 2) letGo(); // the finger moves down: the content moves back up
    touchY.current = y;
  };
  const onLogKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "PageUp" || event.key === "ArrowUp" || event.key === "Home") letGo();
  };

  const onLogScroll = () => {
    const log = logRef.current;
    if (!log) return;
    const slack = performance.now() - upAt.current < 700 ? 2 : 48;
    const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < slack;
    setScrolled(log.scrollTop > 1);
    stickToBottom.current = atBottom;
    if (atBottom) {
      setShowJump(false);
      setUnread(0);
    }
  };

  const jumpToLatest = () => {
    const log = logRef.current;
    if (!log) return;
    stickToBottom.current = true;
    setShowJump(false);
    setUnread(0);
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    log.scrollTo({ top: log.scrollHeight, behavior: reduced ? "auto" : "smooth" });
  };

  // Drafts persist per conversation across pane switches and page reloads
  // (local storage, keyed by conversation id; see drafts.ts).
  const conversationId = state?.conversationId ?? "";
  // A past session opened from Sessions: the transcript alone, nothing to
  // send, answer or queue.
  const readOnly = state?.readOnly === true;
  const openTerminal = () => void chat.openTerminal().catch(() => {});
  // A draft session has no pane yet, so there is no Terminal to open.
  const terminalFor = state?.draft === true ? undefined : openTerminal;
  // A fresh pane's first conversation id appears while the user may already
  // be typing: that transition keeps the draft instead of restoring one.
  const prevConversation = useRef("");
  useEffect(() => {
    const fromFresh = prevConversation.current === "";
    prevConversation.current = conversationId;
    // Another session's in-flight prompt does not follow the switch.
    if (!fromFresh) setOutbox(null);
    if (!conversationId) return;
    if (fromFresh && draft !== "") return;
    setDraft(loadStored(draftKey(conversationId), ""));
    try {
      setFiles(JSON.parse(loadStored(filesKey(conversationId), "[]")));
    } catch {
      setFiles([]);
    }
  }, [conversationId]);
  useEffect(() => {
    if (conversationId) saveStored(draftKey(conversationId), draft, "");
  }, [draft, conversationId]);
  useEffect(() => {
    if (conversationId) saveStored(filesKey(conversationId), JSON.stringify(files), "[]");
  }, [files, conversationId]);
  // The composer's menu: Skills, commands and models of the bound agent
  // (read-only listing; pi/omp model lists take a second to produce).
  const paneProvider = state?.provider ?? "";
  useEffect(() => {
    // A fresh pane has no conversation id yet but its composer is live, so
    // the menu keys on the agent's provider too.
    if ((!conversationId && !paneProvider) || readOnly) return;
    let alive = true;
    setCatalog(null);
    chat.catalog().then((next) => alive && setCatalog(next)).catch(() => {});
    return () => {
      alive = false;
    };
  }, [conversationId, paneProvider, readOnly]);

  // The `/` `$` list: while the draft's last token asks for a command or skill.
  const slashState = useMemo(() => {
    if (panel || slashOff || recording || transcribing) return { entries: [] as ChatCatalogEntry[], start: 0 };
    const locals = localCommands ?? [];
    if (locals.length === 0) return slashMatches(catalog, draft);
    const own = new Set(locals.map((c) => c.name));
    const merged = {
      commands: [
        ...locals.map((c): ChatCatalogEntry => ({ name: c.name, insert: c.name, detail: c.detail, kind: LOCAL_KIND })),
        ...(catalog?.commands ?? []).filter((c) => !own.has(c.name)),
      ],
      skills: catalog?.skills ?? [],
      models: catalog?.models ?? [],
      canSwitchModel: false,
    };
    const found = slashMatches(merged, draft);
    // A local command is a whole message, never a token in the middle of one.
    return found.start === 0 ? found : { ...found, entries: found.entries.filter((e) => e.kind !== LOCAL_KIND) };
  }, [panel, slashOff, recording, transcribing, localCommands, catalog, draft]);
  const slash = slashState.entries;
  useEffect(() => {
    setSlashPick(0);
    setSlashOff(false);
  }, [draft]);
  // Taking a row replaces the typed token with the row's text and waits.
  const acceptSlash = (entry: ChatCatalogEntry) => {
    setDraft(draft.slice(0, slashState.start) + entry.insert);
    composerRef.current?.focus();
  };
  // A command that takes nothing more (no trailing space in its text) runs at
  // once when the agent can take it; skills and commands with arguments wait
  // for the rest of the sentence.
  const runSlash = (entry: ChatCatalogEntry) => {
    if (entry.kind === LOCAL_KIND) {
      runLocal(entry.name);
      return;
    }
    const complete = slashState.start === 0 && entry.kind !== "skill" && entry.kind !== "prompt" && !entry.insert.endsWith(" ");
    if (complete && disposition === "ready" && !sending && !state?.loading) void send(entry.insert.trim());
    else acceptSlash(entry);
  };

  // @ completion: when the draft ends in an @token (no whitespace after
  // the last "@"), suggest the pane project's matching entries.
  useEffect(() => {
    const at = draft.lastIndexOf("@");
    // Offline there is no pane folder to complete from (the bridge would answer
    // for whichever conversation the shared Chat last held).
    if (at === -1 || isOffline) {
      setCompletions([]);
      return;
    }
    const token = draft.slice(at + 1);
    if (/\s/.test(token)) {
      setCompletions([]);
      return;
    }
    const timer = window.setTimeout(() => {
      chat.listFiles(token)
        .then((list) => {
          setCompletions(list ?? []);
          setCompletionPick(0);
        })
        .catch(() => setCompletions([]));
    }, 120);
    return () => window.clearTimeout(timer);
  }, [draft, isOffline]);

  const acceptCompletion = (index: number) => {
    const at = draft.lastIndexOf("@");
    if (at === -1 || completions[index] === undefined) return;
    setDraft(`${draft.slice(0, at + 1)}${completions[index]} `);
    setCompletions([]);
    composerRef.current?.focus();
  };

  useComposerAutoSize(composerRef, draft);

  const disposition = state?.disposition ?? "unknown";
  // What this host of the page offers (the phone turns some off); an absent
  // field means "on".
  const canVoice = state?.features?.voice !== false;
  const canAttach = state?.features?.attach !== false;
  const canQueueFollowUp = state?.features?.queue !== false;
  const canOpenSubagent = state?.features?.subagentDrawer !== false;
  // A fresh session has no transcript yet, so the chip shows the model the
  // agent's own config starts it on.
  const shownModel = state?.model || catalog?.defaultModel || "";
  // A fresh pane (nothing sent yet) is "starting", never "working": its
  // composer holds Send until the agent is ready and shows no Stop.
  const starting = Boolean(state?.waiting) && disposition !== "ready";
  // The Blocked card above the composer already offers the Terminal.
  const blockedCard = !readOnly && disposition === "terminal" && !state?.interactions?.length;
  const working = disposition === "queue" && !state?.waiting;
  // The waiting placeholder stands in for the assistant card between the
  // user's sent row and the first live tail content (the agent's own TUI
  // shows the same "waiting → thinking → working" progression).
  const lastTurn = state?.turns[state.turns.length - 1];
  // A just-sent prompt (echo not yet confirmed by the transcript) shows it
  // at once, even before the agent card reports the working phase.
  const showPendingAssistant = Boolean(
    (working || state?.echoPending) && !state?.loading && lastTurn && lastTurn.user,
  );
  // The task list stays while work remains; once it is finished it retires
  // at the next user message (it belonged to the work before it).
  const runningSubagents = Boolean(state?.subagents?.some((sub) => sub.status === "running"));
  const planVisible = Boolean(
    state?.plan &&
      !(
        planFinished(state.plan) &&
        state.turns.slice(state.plan.turn + 1).some((turn) => turn.user)
      ),
  );
  const hasContent = draft.trim() !== "" || files.length > 0;
  // Dictation owns the composer: typing, attaching, sending and the menus are
  // off while it records or transcribes, so voice and keys never edit the same
  // draft at once. The transcript lands in the draft only when it is done.
  const voiceBusy = recording || transcribing;
  // Phone CSS folds the composer to one row while this holds and it is not focused.
  const atRest = panel === null && !voiceBusy && files.length === 0 && !draft.includes("\n") && !(working && hasContent);
  const compactNow = disposition === "ready" && !sending && (catalog?.commands ?? []).some((c) => c.name === "/compact");
  const ctxHint = !readOnly && Boolean(state?.contextTokens) && ctxFraction !== null && ctxFraction >= 0.92 && ctxHintOff !== conversationId;
  useEffect(() => {
    if (ctxHintOff && ctxFraction !== null && ctxFraction < 0.7) setCtxHintOff("");
  }, [ctxFraction, ctxHintOff]);
  const canSend = disposition === "ready" && hasContent && !sending && !state?.loading && !voiceBusy;
  const canQueue = working && canQueueFollowUp && hasContent && !sending && !voiceBusy;
  // One composer action, as in the reference chat: while the agent works
  // with an empty box it is Stop (one Escape through the bridge); typing
  // swaps it back to Queue/Send.
  const showStop = working && !hasContent && !sending;

  // deliver hands one prompt to the bridge. The draft empties at once (the
  // text lives in the Sending… row from then on); a refusal turns that row
  // into Not delivered · Retry · Edit, an "uncertain" outcome never offers
  // Retry (the agent may already hold it), and anything else is delivered.
  const deliver = async (text: string, via: (text: string) => Promise<ChatBridgeState>, keepDraft = false) => {
    setOutbox({ text, status: "sending" });
    if (!keepDraft) {
      setDraft("");
      setFiles([]);
    }
    stickToBottom.current = true;
    try {
      const next = await via(text);
      setState(next);
      const outcome = next.outcome ?? "";
      if (outcome && !/already delivered/i.test(outcome)) {
        setOutbox({ text, status: /delivery uncertain/i.test(outcome) ? "uncertain" : "failed", reason: outcome });
      } else {
        setOutbox(null);
      }
    } catch (err) {
      setOutbox({ text, status: "failed", reason: err instanceof Error ? err.message : "The prompt could not be sent" });
      refresh();
    }
  };

  // A host command is answered here and never reaches the agent.
  const runLocal = (name: string) => {
    localCommands?.find((c) => c.name === name)?.run();
    setDraft("");
  };

  // override sends that text alone (a command taken from the `/` list).
  const send = async (override?: string) => {
    const text = override ?? composeText(draft, files);
    if (text === "" || sending) return;
    if (files.length === 0 && localCommands?.some((c) => c.name === text.trim())) {
      runLocal(text.trim());
      return;
    }
    setSending(true);
    try {
      await deliver(text, offline ? offline.send : (t) => chat.send(t));
    } finally {
      setSending(false);
    }
  };
  const retryOutbox = () => {
    if (outbox) void send(outbox.text);
  };
  // A sent prompt comes back to the composer (after what is already typed) to be edited and sent again.
  const reuseText = (text: string) => {
    setDraft((current) => (current.trim() === "" ? text : `${current.replace(/\s+$/, "")}\n${text}`));
    setPanel(null);
    composerRef.current?.focus();
  };
  const editOutbox = () => {
    if (!outbox) return;
    setDraft(outbox.text);
    setOutbox(null);
    composerRef.current?.focus();
  };

  const answerInteraction = async (id: string, option: string) => {
    if (isFreeTextOption(option)) {
      setTextPrompt({ id, option });
      setTextAnswer("");
      return;
    }
    try {
      setState(await chat.answerInteraction(id, option));
    } catch {
      refresh(); // lost a CAS race or the prompt moved on: re-read
    }
  };

  // answerMany delivers a multi-select question's ticked rows in one go.
  const answerMany = async (id: string, options: string[]) => {
    if (answering) return;
    setAnswering(true);
    try {
      setState(await chat.answerInteractionMulti(id, options));
    } catch {
      refresh(); // lost a CAS race or the prompt moved on: re-read
    } finally {
      setAnswering(false);
    }
  };

  // submitTextAnswer delivers the typed answer for a free-text option: the
  // bridge selects the option, settles for the TUI's text field, then pastes
  // and submits the text through agent.prompt.
  const submitTextAnswer = async () => {
    const text = textAnswer.trim();
    if (!textPrompt || text === "" || answering) return;
    setAnswering(true);
    try {
      setState(await chat.answerInteractionText(textPrompt.id, textPrompt.option, text));
      setTextPrompt(null);
      setTextAnswer("");
    } catch {
      refresh(); // lost a race or the delivery failed: re-read
    } finally {
      setAnswering(false);
    }
  };

  const stop = async () => {
    setSending(true);
    try {
      setState(await chat.stop());
    } catch {
      refresh();
    } finally {
      setSending(false);
    }
  };

  // interruptSend is the explicit cut-in: the bridge stops the running turn
  // (one Escape), settles briefly, then delivers this text now instead of
  // queueing it behind the turn.
  const interruptSend = async () => {
    const text = composeText(draft, files);
    if (text === "" || interrupting) return;
    setInterrupting(true);
    try {
      await deliver(text, (t) => chat.interruptAndSend(t));
    } finally {
      setInterrupting(false);
    }
  };

  // One stacked follow-up goes now: withdrawn from the queue first (so it cannot
  // also go at the turn boundary; a row the worker already claimed stays put),
  // then cut in like Interrupt & send. What is typed in the composer is untouched.
  const sendQueuedNow = async (id: string) => {
    const item = state?.queuedItems?.find((row) => row.id === id);
    if (!item || interrupting) return;
    setInterrupting(true);
    try {
      const next = await chat.cancelQueuedItem(id);
      if (next) setState(next);
      if (next?.queuedItems?.some((row) => row.id === id)) return;
      await deliver(item.text, (t) => chat.interruptAndSend(t), true);
    } catch {
      refresh();
    } finally {
      setInterrupting(false);
    }
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // The `/` `$` list owns navigation while it is open; Enter on a command
    // typed in full still sends it.
    if (slash.length > 0 && completions.length === 0 && !event.nativeEvent.isComposing) {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        const step = event.key === "ArrowDown" ? 1 : slash.length - 1;
        setSlashPick((pick) => (pick + step) % slash.length);
        return;
      }
      const entry = slash[Math.min(slashPick, slash.length - 1)];
      if (event.key === "Tab") {
        event.preventDefault();
        acceptSlash(entry);
        return;
      }
      if (event.key === "Enter" && !event.shiftKey && entry.insert.trim() !== draft.slice(slashState.start)) {
        event.preventDefault();
        runSlash(entry);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setSlashOff(true);
        return;
      }
    }
    // The @ completion list owns navigation keys while it is open.
    if (completions.length > 0) {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setCompletionPick((pick) => (pick + 1) % completions.length);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setCompletionPick((pick) => (pick - 1 + completions.length) % completions.length);
        return;
      }
      if (event.key === "Tab" || event.key === "Enter") {
        event.preventDefault();
        if (!event.nativeEvent.isComposing) acceptCompletion(completionPick);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setCompletions([]);
        return;
      }
    }
    // ↑ on an empty box recalls the previous prompt (and on, while the caret
    // sits at the top); ↓ walks back toward what was there (shell history).
    if ((event.key === "ArrowUp" || event.key === "ArrowDown") && !event.shiftKey && !event.metaKey && !event.altKey && !event.ctrlKey && !event.nativeEvent.isComposing) {
      const el = event.currentTarget;
      const sent = (state?.turns ?? []).filter((turn) => turn.user && (turn.text ?? "").trim() !== "").map((turn) => (turn.text ?? "").trim()).reverse();
      const recall = recallRef.current;
      if (event.key === "ArrowUp" && sent.length > 0 && (recall === null ? draft === "" : el.selectionStart === 0 && el.selectionEnd === 0)) {
        const next = recall === null ? 0 : Math.min(sent.length - 1, recall + 1);
        event.preventDefault();
        recallRef.current = next;
        setDraft(sent[next]);
        requestAnimationFrame(() => el.setSelectionRange(0, 0));
        return;
      }
      if (event.key === "ArrowDown" && recall !== null && el.selectionStart === el.value.length) {
        event.preventDefault();
        if (recall === 0) {
          recallRef.current = null;
          setDraft("");
        } else {
          recallRef.current = recall - 1;
          setDraft(sent[recall - 1]);
        }
        return;
      }
    }
    // Enter sends on the ready disposition (chat convention); IME
    // composition never sends — a half-typed candidate must not commit.
    if (event.key !== "Enter" || event.shiftKey) return;
    if (composingRef.current || event.nativeEvent.isComposing) return;
    event.preventDefault();
    // ⌘/Ctrl+⏎ while the agent works cuts in; plain ⏎ then queues the follow-up.
    if ((event.metaKey || event.ctrlKey) && working && hasContent && !sending && !interrupting && !voiceBusy) void interruptSend();
    else if (canSend || canQueue) void send();
  };

  const cancelQueued = async () => {
    const next = await chat.cancelQueued().catch(() => state);
    if (next) setState(next);
  };

  const cancelQueuedItem = async (id: string) => {
    const next = await chat.cancelQueuedItem(id).catch(() => state);
    if (next) setState(next);
  };

  const editQueuedItem = async (id: string, text: string) => {
    const next = await chat.editQueuedItem(id, text).catch(() => state);
    if (next) setState(next);
  };

  const reorderQueued = async (id: string, index: number) => {
    const next = await chat.reorderQueued(id, index).catch(() => state);
    if (next) setState(next);
  };

  // The turn at the viewport's top edge before an older page lands: the page
  // that follows is realigned to it (see the layout effect after `list`).
  const anchorRef = useRef<{ key: string; index: number; offset: number; at: number } | null>(null);
  const olderTimeout = useRef<number | null>(null);
  const loadOlder = async () => {
    if (loadingOlder) return;
    const log = logRef.current;
    if (log) {
      const top = log.getBoundingClientRect().top;
      for (const row of log.querySelectorAll<HTMLElement>("[data-turn]")) {
        const box = row.getBoundingClientRect();
        if (box.bottom <= top + 1) continue;
        const index = Number(row.dataset.turn);
        anchorRef.current = { key: keys[index], index, offset: box.top - top, at: Date.now() };
        break;
      }
    }
    setLoadingOlder(true);
    if (olderTimeout.current !== null) window.clearTimeout(olderTimeout.current);
    olderTimeout.current = window.setTimeout(() => {
      olderTimeout.current = null;
      setLoadingOlder(false);
    }, 4000);
    try {
      await chat.older();
      // The refreshed projection (and the anchoring effect) arrive through
      // the chat:updated event.
    } catch {
      if (olderTimeout.current !== null) {
        window.clearTimeout(olderTimeout.current);
        olderTimeout.current = null;
      }
      setLoadingOlder(false);
    }
  };

  // Reaching the top loads the earlier page by itself; the button stays as the
  // visible state (and the manual way when the page cannot tell). Observing again
  // after each page lets a short transcript fill the viewport.
  const olderRef = useRef<HTMLButtonElement>(null);
  const loadOlderRef = useRef(loadOlder);
  loadOlderRef.current = loadOlder;
  const turnCount = state?.turns.length ?? 0;
  // The transcript length the last automatic load started from: a page that added nothing is not asked for again.
  const autoOlderAt = useRef(-1);
  useEffect(() => {
    // When turn count updates or hasOlder changes, any pending older request is complete.
    if (loadingOlder) {
      if (olderTimeout.current !== null) {
        window.clearTimeout(olderTimeout.current);
        olderTimeout.current = null;
      }
      setLoadingOlder(false);
    }
  }, [turnCount, state?.hasOlder]);
  useEffect(() => {
    const button = olderRef.current;
    const log = logRef.current;
    if (!button || !log || loadingOlder || typeof IntersectionObserver === "undefined") return;
    const phone = typeof document !== "undefined" && document.documentElement.dataset.host === "phone";
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting) || autoOlderAt.current === turnCount) return;
        autoOlderAt.current = turnCount;
        void loadOlderRef.current();
      },
      { root: log, rootMargin: phone ? "360px 0px 0px 0px" : "120px 0px 0px 0px" },
    );
    observer.observe(button);
    return () => observer.disconnect();
  }, [state?.hasOlder, turnCount, loadingOlder, conversationId]);
  useEffect(() => {
    autoOlderAt.current = -1;
    if (olderTimeout.current !== null) {
      window.clearTimeout(olderTimeout.current);
      olderTimeout.current = null;
    }
    setLoadingOlder(false);
  }, [conversationId]);

  // insertAtCaret splices text into the draft at the caret (attachments,
  // dictated text) and puts the caret after the insertion.
  const insertAtCaret = useCallback((inserted: string) => {
    const el = composerRef.current;
    if (!el) {
      setDraft((current) => current + inserted);
      return;
    }
    const start = el.selectionStart ?? draft.length;
    const end = el.selectionEnd ?? draft.length;
    setDraft(draft.slice(0, start) + inserted + draft.slice(end));
    requestAnimationFrame(() => {
      el.focus();
      const caret = start + inserted.length;
      el.setSelectionRange(caret, caret);
    });
  }, [draft]);

  const addFile = (path: string) => setFiles((current) => (current.includes(path) ? current : [...current, path]));
  const attach = async () => {
    const path = await chat.pickAttachment().catch(() => "");
    if (path) addFile(path);
  };
  // Pasted and dropped files travel page → bridge as base64 and come back as
  // a saved path (the only form every agent CLI turns into an image/file).
  const takeFiles = async (list: File[]) => {
    setVoiceError("");
    for (const file of list) {
      try {
        const path = await chat.saveAttachment(await fileToBase64(file), file.type, file.name);
        if (IMAGE_EXT.test(path)) previews.current.set(path, URL.createObjectURL(file));
        addFile(path);
      } catch (err) {
        setVoiceError(err instanceof Error ? err.message : "Could not attach the file");
      }
    }
    composerRef.current?.focus();
  };
  const onPaste = (event: React.ClipboardEvent) => {
    const list = Array.from(event.clipboardData.files);
    if (list.length === 0 || !canAttach) return;
    event.preventDefault();
    void takeFiles(list);
  };
  const removeFile = (path: string) => {
    const url = previews.current.get(path);
    if (url) URL.revokeObjectURL(url);
    previews.current.delete(path);
    setFiles((current) => current.filter((item) => item !== path));
  };
  const hasFiles = (event: React.DragEvent) => Array.from(event.dataTransfer.types).includes("Files");

  // Model chip: pi/omp switch with their own typed command, the others open
  // their picker as a question card (Claude's takes "this session only").
  const switchModel = async (id: string) => {
    setSwitching(true);
    try {
      setState(await chat.switchModel(id));
    } catch (err) {
      setVoiceError(err instanceof Error ? err.message : "Could not switch model");
    } finally {
      setSwitching(false);
      setPanel(null);
    }
  };
  // A model row or effort that is one typed agent command (Antigravity's
  // "/model Name (High)", pi "/thinking low", omp/Claude "/effort low"): the
  // catalog carries the exact text, so it goes through the ordinary send.
  const sendFromPanel = async (text: string) => {
    setSwitching(true);
    try {
      setState(await chat.send(text));
    } catch (err) {
      setVoiceError(err instanceof Error ? err.message : "Could not send");
    } finally {
      setSwitching(false);
      setPanel(null);
    }
  };
  const closePanel = () => {
    setPanel(null);
    composerRef.current?.focus();
  };
  const openModelPicker = async () => {
    setPanel(null);
    if (!catalog?.modelPicker) return;
    try {
      setState(await chat.send(catalog.modelPicker));
    } catch {
      refresh();
    }
  };

  // Dictation: record in the page (WKWebView, secure context), then the
  // bridge forwards the clip to the configured OpenAI-compatible endpoint.
  // While recording, an AnalyserNode feeds the small level meter (the
  // reference chat's live visualizer) so the mic state is visible evidence,
  // not a pulsing button alone.
  // transcribeClip sends one clip and splices the text in; a failure keeps the
  // clip for Retry (the permission and the voice are the user's to spend once).
  const transcribeClip = (base64: string, mime: string) => {
    setTranscribing(true);
    setVoiceError("");
    setClipFailed(false);
    failedClipRef.current = null;
    chat.transcribe(base64, mime)
      .then((text) => {
        if (text) insertAtCaret(text + " ");
      })
      .catch((err) => {
        failedClipRef.current = { base64, mime };
        setClipFailed(true);
        setVoiceError(err instanceof Error ? err.message : "Transcription failed");
      })
      .finally(() => {
        setTranscribing(false);
        // The textarea is enabled again only after this render.
        requestAnimationFrame(() => composerRef.current?.focus());
      });
  };
  const retryClip = () => {
    const clip = failedClipRef.current;
    if (clip) transcribeClip(clip.base64, clip.mime);
  };

  const startDictation = async () => {
    if (recording || transcribing) return;
    setVoiceError("");
    setClipFailed(false);
    failedClipRef.current = null;
    setMicDenied(false);
    setPanel(null);
    setCompletions([]);
    cancelledRef.current = false;
    // The system's permission sheet may sit on another display: after a beat
    // with no answer the composer says so instead of looking dead.
    const askTimer = window.setTimeout(() => setMicAsking(true), 700);
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true }).finally(() => {
        window.clearTimeout(askTimer);
        setMicAsking(false);
      });
      const audioCtx = new AudioContext();
      if (audioCtx.state === "suspended") void audioCtx.resume();
      const analyser = audioCtx.createAnalyser();
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.75;
      audioCtx.createMediaStreamSource(stream).connect(analyser);
      audioCtxRef.current = audioCtx;
      startMeter(analyser);
      const recorder = new MediaRecorder(stream);
      const chunks: Blob[] = [];
      recorder.ondataavailable = (event) => {
        if (event.data.size > 0) chunks.push(event.data);
      };
      recorder.onstop = () => {
        stopMeter();
        stream.getTracks().forEach((track) => track.stop());
        if (cancelledRef.current) return; // Cancel: the clip is thrown away
        const blob = new Blob(chunks, { type: recorder.mimeType || "audio/webm" });
        if (blob.size < 1024) return; // an accidental tap: nothing to transcribe
        setTranscribing(true);
        void blob.arrayBuffer().then((buffer) => {
          const bytes = new Uint8Array(buffer);
          let binary = "";
          const step = 0x8000;
          for (let i = 0; i < bytes.length; i += step) {
            binary += String.fromCharCode(...bytes.subarray(i, i + step));
          }
          transcribeClip(btoa(binary), blob.type || "audio/webm");
        });
      };
      recorder.start();
      recorderRef.current = recorder;
      setRecording(true);
    } catch (err) {
      const denied = err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "SecurityError");
      setMicDenied(denied);
      setVoiceError(denied ? "Microphone access is off for Solco." : err instanceof Error ? err.message : "Microphone unavailable");
    }
  };

  const stopDictation = () => {
    stopMeter();
    recorderRef.current?.stop();
    setRecording(false);
  };

  // cancelDictation stops the recorder and discards the clip (Esc / ✕).
  const cancelDictation = () => {
    cancelledRef.current = true;
    stopDictation();
    requestAnimationFrame(() => composerRef.current?.focus());
  };

  // While recording the textarea is disabled, so the keys live on the window:
  // ⏎ finishes (and transcribes), Esc cancels.
  useEffect(() => {
    if (!recording) return;
    setRecordSecs(0);
    const started = Date.now();
    const clock = window.setInterval(() => setRecordSecs(Math.floor((Date.now() - started) / 1000)), 500);
    const onKey = (event: KeyboardEvent) => {
      if (event.isComposing) return;
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        stopDictation();
      } else if (event.key === "Escape") {
        event.preventDefault();
        cancelDictation();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.clearInterval(clock);
      window.removeEventListener("keydown", onKey);
    };
    // stop/cancel only touch refs and setters
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [recording]);

  // A recording belongs to the conversation it started in: switching away
  // discards it instead of dictating into another draft.
  useEffect(() => {
    if (recorderRef.current?.state === "recording") cancelDictation();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversationId]);

  // startMeter runs the mirrored frequency-bar canvas for as long as the
  // recording lasts; the canvas only exists while `recording`, so frames
  // before it mounts are skipped.
  const startMeter = (analyser: AnalyserNode) => {
    const data = new Uint8Array(analyser.frequencyBinCount);
    let color = "";
    const frame = () => {
      meterRafRef.current = requestAnimationFrame(frame);
      const canvas = meterRef.current;
      if (!canvas) return;
      const g = canvas.getContext("2d");
      if (!g) return;
      analyser.getByteFrequencyData(data);
      const { width, height } = canvas;
      g.clearRect(0, 0, width, height);
      if (!color) color = getComputedStyle(canvas).color;
      g.fillStyle = color;
      const bars = 14;
      const barWidth = 2;
      const gap = 2;
      for (let i = 0; i < bars; i++) {
        // Voice energy sits in the low bins; the power curve spreads the
        // bars across it instead of lighting only the first few.
        const bin = Math.min(
          data.length - 1,
          Math.floor(Math.pow(i / bars, 1.6) * (data.length * 0.5)) + 2,
        );
        const level = data[bin] / 255;
        const barHeight = Math.max(2, level * height);
        g.fillRect(i * (barWidth + gap), (height - barHeight) / 2, barWidth, barHeight);
      }
    };
    cancelAnimationFrame(meterRafRef.current);
    meterRafRef.current = requestAnimationFrame(frame);
  };

  const stopMeter = () => {
    cancelAnimationFrame(meterRafRef.current);
    void audioCtxRef.current?.close().catch(() => {});
    audioCtxRef.current = null;
  };

  // Hard cleanup if the page unloads mid-recording.
  useEffect(() => () => stopMeter(), []);

  // Page-level chat keys (listed in Help ▸ Keyboard Shortcuts): ⌘. stops the
  // running turn, ⇧⌘M starts/finishes dictation, ⌘↑/⌘↓ walk the sent prompts
  // (only while the box is empty, where the caret keys have nothing to move).
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.isComposing || readOnly || !event.metaKey || event.ctrlKey || event.altKey) return;
      if (event.key === "." && !event.shiftKey) {
        if (working) {
          event.preventDefault();
          void stop();
        }
        return;
      }
      if (event.shiftKey && event.key.toLowerCase() === "m") {
        event.preventDefault();
        if (recording) stopDictation();
        else if (!transcribing) void startDictation();
        return;
      }
      if (!event.shiftKey && (event.key === "ArrowUp" || event.key === "ArrowDown") && draft === "" && !voiceBusy) {
        const log = logRef.current;
        const prompts = turns.flatMap((turn, index) => (turn.user ? [index] : []));
        if (!log || prompts.length === 0) return;
        event.preventDefault();
        const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        // ↑ lands on the nearest prompt above the viewport's top edge, ↓ on the
        // nearest below it; past the last one ↓ is "back to the latest".
        const offsets = prompts.map((index) => (list.top(index) ?? 0) - log.scrollTop);
        const above = offsets.map((offset) => offset < -8).lastIndexOf(true);
        const below = offsets.findIndex((offset) => offset > 8);
        const at = event.key === "ArrowUp" ? above : below;
        if (at < 0 && event.key === "ArrowUp") {
          log.scrollTo({ top: 0, behavior: reduced ? "auto" : "smooth" });
        } else if (at < 0) {
          jumpToLatest();
        } else {
          stickToBottom.current = false;
          list.scrollTo(prompts[at], !reduced);
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // handlers read current state through the render closure
    // eslint-disable-next-line react-hooks/exhaustive-deps
  });

  // ⌘F opens the find bar (any session, read-only included); Esc closes it.
  const openFind = useCallback(() => {
    setFinding(true);
    requestAnimationFrame(() => {
      findRef.current?.focus();
      findRef.current?.select();
    });
  }, []);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey && event.key.toLowerCase() === "f") {
        event.preventDefault();
        openFind();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openFind]);

  // The context menu is the system's (chat.showContextMenu); the two actions
  // only the page can run come back through chatMenu.
  useEffect(
    () =>
      events.chatMenu.on((action) => {
        if (action === "find") openFind();
        if (action === "selectAll") {
          selectContents(logRef.current?.querySelector(".col"));
        }
      }),
    [openFind],
  );

  const runFind = (query: string, backwards: boolean) => {
    if (query === "") return;
    // window.find is WebKit's own search: it scrolls to and selects the match.
    const find = (wrap: boolean) => {
      // The search box holds the query itself and find-in-page matches form
      // text, so it is hidden for the duration of the call.
      const box = findRef.current;
      if (box) box.style.visibility = "hidden";
      try {
        return (window as unknown as { find: (...args: unknown[]) => boolean }).find(query, false, backwards, wrap, false, false, false);
      } finally {
        if (box) box.style.visibility = "";
      }
    };
    if (list.virtual && !find(false)) {
      // No further match among the rendered turns. A long chat renders only
      // the turns near the viewport, so look through the rest by their text,
      // bring the nearest one in, and search again from there.
      const needle = query.toLowerCase();
      const hits = turns.flatMap((turn, index) => (turnText(turn).toLowerCase().includes(needle) ? [index] : []));
      const first = list.rows[0]?.index ?? 0;
      const last = list.rows[list.rows.length - 1]?.index ?? 0;
      const outside = hits.filter((index) => index < first || index > last);
      const target = backwards
        ? (outside.filter((index) => index < first).pop() ?? outside.pop())
        : (outside.find((index) => index > last) ?? outside[0]);
      if (target === undefined) {
        find(true);
      } else {
        stickToBottom.current = false;
        list.scrollTo(target, false);
        window.getSelection()?.removeAllRanges();
        requestAnimationFrame(() => requestAnimationFrame(() => find(true)));
      }
    } else if (!list.virtual) {
      find(true);
    }
    findRef.current?.focus();
  };
  const closeFind = () => {
    setFinding(false);
    window.getSelection()?.removeAllRanges();
    composerRef.current?.focus();
  };

  const turns = state?.turns ?? [];
  const keys = useMemo(() => turnKeys(turns), [turns]);
  // Sub-agent cards open one drawer; it closes with the conversation it
  // belonged to or when the sub-agent leaves the list.
  const [openSub, setOpenSub] = useState<string | null>(null);
  // The native rows' drawer ask (chatSubagentOpen below). It can land
  // before the conversation it targeted has finished binding, so it rides
  // out one conversationId change and is spent once its drawer renders.
  const nativeSubAsk = useRef<{ id: string; at: number } | null>(null);
  useEffect(
    () =>
      chatSubagentOpen.on((id) => {
        nativeSubAsk.current = { id, at: Date.now() };
        setOpenSub(id);
      }),
    [],
  );
  const subCtx = useMemo<SubagentContextValue>(
    () => ({
      byId: new Map((state?.subagents ?? []).map((sub) => [sub.id, sub])),
      open: canOpenSubagent ? (id) => setOpenSub((current) => (current === id ? null : id)) : () => {},
      openId: openSub ?? undefined,
    }),
    [state?.subagents, openSub, canOpenSubagent],
  );
  const subOpen = openSub !== null && subCtx.byId.has(openSub);
  useEffect(() => {
    setOpenSub((current) =>
      current !== null && nativeSubAsk.current?.id === current && Date.now() - nativeSubAsk.current.at < 10000
        ? current
        : null,
    );
  }, [conversationId]);
  useEffect(() => {
    if (subOpen) nativeSubAsk.current = null;
  }, [subOpen]);
  // Drawer width (drawer.tsx grip): a local layout preference, unlike the
  // per-conversation drafts.
  const [drawerW, setDrawerW] = useState(() => {
    try {
      const saved = Number(localStorage.getItem("solco.drawerWidth"));
      return saved >= DRAWER_MIN && saved <= DRAWER_MAX ? saved : DRAWER_DEFAULT;
    } catch {
      return DRAWER_DEFAULT;
    }
  });
  const listRef = useRef<HTMLDivElement>(null);
  const [listMargin, setListMargin] = useState(0);
  const list = useTurnList(logRef, turns.length, (index) => keys[index], listMargin);
  // Older turns landed: keep the turn the user was reading where it was. Rows
  // that mount above it are measured over the next frames, so the alignment
  // is repeated until the sizes settle.
  useLayoutEffect(() => {
    const anchor = anchorRef.current;
    const log = logRef.current;
    if (!anchor || !log) return;
    if (Date.now() - anchor.at > 5000) {
      anchorRef.current = null;
      return;
    }
    const index = keys.indexOf(anchor.key);
    if (index < 0 || index === anchor.index) return; // the page has not landed yet
    let frames = 0;
    const settle = () => {
      const at = list.top(index);
      if (at !== null) log.scrollTop = at - anchor.offset;
      if (++frames < 4) requestAnimationFrame(settle);
      else anchorRef.current = null;
    };
    settle();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keys]);
  // The windowed list starts below the "Earlier messages" button.
  useLayoutEffect(() => {
    const margin = listRef.current?.offsetTop ?? 0;
    setListMargin((prev) => (prev === margin ? prev : margin));
  });
  // Rows are measured after they mount; keep the latest turn pinned as the
  // measured height settles.
  useLayoutEffect(() => {
    const log = logRef.current;
    if (log && stickToBottom.current) log.scrollTop = log.scrollHeight;
  }, [list.total]);
  const reviewChanges = (path: string) => {
    void chat.reviewChanges(path).catch(() => {});
  };
  // One line under the composer, only for what needs saying: dictation and its
  // failures. State the Working line, the placeholder and the buttons already show stays out.
  const note =
    voiceError ||
    (micAsking ? "Waiting for microphone permission — answer the system prompt (it may be on another display)." : "") ||
    (transcribing ? "Transcribing…" : recording ? "Listening… ⏎ or ■ finishes · Esc cancels" : "");
  const hasHint = note !== "" || micDenied || (clipFailed && !transcribing);

  // A browser's right-click menu (Reload, Inspect) has no place in an app:
  // the system menu below is built from what is under the pointer. Editable
  // fields keep the system's own editing menu (Cut/Copy/Paste, Spelling,
  // Writing Tools).
  const onContextMenu = (event: React.MouseEvent) => {
    const target = event.target as HTMLElement;
    if (target.closest("textarea, input")) return;
    event.preventDefault();
    const row = target.closest<HTMLElement>("[data-turn]");
    const turn = row ? turns[Number(row.dataset.turn)] : undefined;
    void chat
      .showContextMenu({
        selection: window.getSelection()?.toString() ?? "",
        link: target.closest("a[href]")?.getAttribute("href") ?? "",
        message: turn ? (turn.user ? userDisplay(turn.text ?? "").text : (turn.narration ?? "")) : "",
        path: target.closest<HTMLElement>("[data-path]")?.dataset.path ?? "",
      })
      .catch(() => {});
  };

  return (
    <SubagentContext.Provider value={subCtx}>
    <div
      className={subOpen ? "chat with-drawer" : "chat"}
      style={subOpen ? ({ "--drawer-w": `${drawerW}px` } as React.CSSProperties) : undefined}
      onContextMenu={onContextMenu}
      onDragOver={(event) => {
        if (readOnly || voiceBusy || !canAttach || !hasFiles(event)) return;
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(event) => {
        if (event.currentTarget === event.target) setDragging(false);
      }}
      onDrop={(event) => {
        if (readOnly || voiceBusy || !canAttach || !hasFiles(event)) return;
        event.preventDefault();
        setDragging(false);
        void takeFiles(Array.from(event.dataTransfer.files));
      }}
    >
      {dragging ? <div className="dropveil">Drop to attach</div> : null}
      <div className={`topline${scrolled ? " on" : ""}`} aria-hidden="true" />
      {linkHint ? (
        <div className="linkhint" aria-hidden="true">
          {linkHint}
        </div>
      ) : null}
      {finding ? (
        <div className="findbar" role="search">
          <input
            ref={findRef}
            value={findQuery}
            placeholder="Find in conversation"
            aria-label="Find in conversation"
            onChange={(event) => {
              setFindQuery(event.target.value);
              window.getSelection()?.removeAllRanges();
              runFind(event.target.value, false);
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape" && !event.nativeEvent.isComposing) {
                event.preventDefault();
                closeFind();
              } else if (event.key === "Enter" && !event.nativeEvent.isComposing) {
                event.preventDefault();
                runFind(findQuery, event.shiftKey);
              }
            }}
          />
          <button type="button" className="ib" aria-label="Previous match" title="Previous (⇧⏎)" onClick={() => runFind(findQuery, true)}>
            <Icon name="up" size={14} />
          </button>
          <button type="button" className="ib" aria-label="Next match" title="Next (⏎)" onClick={() => runFind(findQuery, false)}>
            <span style={{ display: "inline-block", transform: "rotate(180deg)" }}>
              <Icon name="up" size={14} />
            </span>
          </button>
          <button type="button" className="ib" aria-label="Close find" title="Close (Esc)" onClick={closeFind}>
            <Icon name="x" size={14} />
          </button>
        </div>
      ) : null}
      {state?.outline !== false && !state?.loading && !state?.waiting ? <TocRail turns={turns} logRef={logRef} top={list.top} scrollTo={list.scrollTo} /> : null}
      <div
        className="chat-log"
        ref={logRef}
        onScroll={onLogScroll}
        onWheel={onLogWheel}
        onTouchStart={onLogTouchStart}
        onTouchMove={onLogTouchMove}
        onKeyDown={onLogKeyDown}
      >
        <div className="col">
          {state?.hasOlder ? (
            <button ref={olderRef} type="button" className="older" onClick={() => void loadOlder()} disabled={loadingOlder}>
              {loadingOlder ? "Loading…" : "Earlier messages"}
            </button>
          ) : null}
          {state?.loading ? (
            <SessionState kind="loading" onOpenTerminal={terminalFor} />
          ) : state?.waiting ? (
            <SessionState
              kind="waiting"
              provider={state.provider}
              starting={starting}
              links={state.linkCandidates}
              onLink={(key) => void chat.linkSession(key).then(setState).catch(refresh)}
              onOpenTerminal={blockedCard ? undefined : terminalFor}
              draft={state.draft === true}
            />
          ) : turns.length === 0 ? (
            offline ? (
              // No Agent yet: the host's own card, gone once a prompt is in flight.
              outbox ? null : offline.empty
            ) : (
              // A bound session with no rows yet (the agent wrote its header
              // only) reads the same as a fresh pane: type to start.
              <SessionState kind="waiting" provider={state?.provider} draft={state?.draft === true} onOpenTerminal={blockedCard ? undefined : terminalFor} />
            )
          ) : (
            <div
              ref={listRef}
              className={list.virtual ? "vlist" : undefined}
              style={list.virtual ? { height: list.total - listMargin } : undefined}
            >
              {(list.virtual ? list.rows.map((row) => row.index) : turns.map((_, index) => index)).map((index) => {
                const turn = turns[index];
                if (!turn) return null;
                const row = list.virtual ? list.rows.find((item) => item.index === index) : undefined;
                return (
                  <div
                    key={keys[index]}
                    className={row ? "vrow" : undefined}
                    data-turn={index}
                    data-index={row ? index : undefined}
                    ref={row ? list.measure : undefined}
                    style={row ? { transform: `translateY(${row.start - listMargin}px)` } : undefined}
                  >
                    {turn.compactedBefore ? (
                      <div className="compact-divider" role="separator">
                        <span>Context compacted</span>
                      </div>
                    ) : null}
                    {turn.noteBefore ? (
                      <details className="note-chip">
                        <summary>System instructions</summary>
                        <pre>{turn.noteBefore}</pre>
                      </details>
                    ) : null}
                    <TurnBoundary>
                      <TurnRow
                        // Only Claude's streaming tail marks a turn live; the agent
                        // card's working phase covers every provider (omp, Codex),
                        // so the last assistant turn shimmers while it works.
                        turn={working && index === turns.length - 1 && !turn.user && !turn.live ? { ...turn, live: true } : turn}
                        startMs={index > 0 && turns[index - 1].user ? turns[index - 1].tsMs : undefined}
                        root={state?.root}
                        canReview={state?.canReview === true}
                        onReview={reviewChanges}
                        isLast={index === turns.length - 1}
                        onReuse={readOnly ? undefined : reuseText}
                      />
                    </TurnBoundary>
                  </div>
                );
              })}
            </div>
          )}
          {outbox ? (
            <OutboxRow outbox={outbox} onRetry={retryOutbox} onEdit={editOutbox} onDiscard={() => setOutbox(null)} />
          ) : null}
          {showPendingAssistant && !outbox ? (
            <div className="turn assistant pending" role="status" aria-live="polite">
              <WorkingLine sinceMs={lastTurn?.tsMs} />
            </div>
          ) : null}
        </div>
      </div>
      {showJump ? (
        <button
          type="button"
          className="jump"
          aria-label={unread > 1 ? `${unread} new messages, jump to latest` : "Jump to latest"}
          title={unread > 1 ? `${unread} new messages` : "Jump to latest"}
          onClick={jumpToLatest}
        >
          <Icon name="up" size={15} />
          {unread > 1 ? <span>{unread}</span> : null}
        </button>
      ) : null}
      {readOnly ? (
        dockAbove ? (
          <div className="composer">
            <div className="col">{dockAbove}</div>
          </div>
        ) : null
      ) : (
        <div className="composer">
          <div className="col">
            {dockAbove}
            {planVisible || runningSubagents ? (
              <TasksTray
                plan={planVisible ? state?.plan : undefined}
                subagents={state?.subagents}
                onOpenSubagent={canOpenSubagent ? subCtx.open : undefined}
                live={working}
                forced={dockForced("tasks")}
                onToggle={(open) => setPanel(open ? "tasks" : null)}
              />
            ) : null}
            {state?.interactions?.length ? (
              state.interactions.map((interaction) => (
                <QuestionDock
                  key={interaction.id}
                  interaction={interaction}
                  textPrompt={textPrompt}
                  textAnswer={textAnswer}
                  answering={answering}
                  composing={composingRef}
                  onPick={(option) => void answerInteraction(interaction.id, option)}
                  onPickMany={(options) => void answerMany(interaction.id, options)}
                  onTextChange={setTextAnswer}
                  onTextSubmit={() => void submitTextAnswer()}
                  onTextCancel={() => {
                    setTextPrompt(null);
                    setTextAnswer("");
                  }}
                />
              ))
            ) : disposition === "terminal" ? (
              <BlockedDock onOpenTerminal={openTerminal} reason={state?.terminalReason} />
            ) : null}
            {state?.outcome && !outbox ? <div className="outcome">{state.outcome}</div> : null}
            <QueueCard
              items={state?.queuedItems}
              canReorder={state?.features?.reorder !== false}
              canEdit={state?.features?.edit !== false}
              onCancelItem={(id) => void cancelQueuedItem(id)}
              onEditItem={(id, text) => void editQueuedItem(id, text)}
              onSendNow={(id) => void sendQueuedNow(id)}
              onReorder={(id, index) => void reorderQueued(id, index)}
              onCancelAll={() => void cancelQueued()}
              forced={dockForced("queue")}
              onToggle={(open) => setPanel(open ? "queue" : null)}
            />
            {slash.length > 0 && completions.length === 0 ? (
              <SlashPanel entries={slash} pick={slashPick} onPick={runSlash} onHover={setSlashPick} />
            ) : null}
            {panel === "plus" ? (
              <PlusPanel
                catalog={catalog}
                canAttach={canAttach}
                onAttach={() => {
                  setPanel(null);
                  void attach();
                }}
                onOpenList={(lead) => {
                  setPanel(null);
                  setDraft(lead);
                  composerRef.current?.focus();
                }}
                onClose={closePanel}
              />
            ) : null}
            {panel === "model" ? (
              <ModelPanel
                catalog={catalog}
                current={shownModel}
                busy={switching || disposition !== "ready"}
                onSwitch={(model) => (model.pick ? void sendFromPanel(model.pick) : void switchModel(model.id))}
                onEffort={(effort) => void sendFromPanel(effort.send)}
                onPicker={() => void openModelPicker()}
                onClose={closePanel}
              />
            ) : null}
            {completions.length > 0 ? (
              <div className="completions">
                {completions.map((name, index) => (
                  <button
                    key={name}
                    type="button"
                    className={index === completionPick ? "pick" : ""}
                    onMouseDown={(event) => {
                      event.preventDefault(); // keep the textarea focus
                      acceptCompletion(index);
                    }}
                  >
                    {name}
                  </button>
                ))}
              </div>
            ) : null}
            {ctxHint ? (
              <div className="ctxhint" role="status">
                <span>Context is {Math.round((ctxFraction ?? 0) * 100)}% full</span>
                {compactNow ? (
                  <button type="button" onClick={() => void send("/compact")}>
                    Compact
                  </button>
                ) : null}
                <button type="button" className="x" aria-label="Dismiss" onClick={() => setCtxHintOff(conversationId)}>
                  <Icon name="x" size={12} />
                </button>
              </div>
            ) : null}
            {state?.canReview && (state.changedFiles ?? 0) > 0 ? (
              <div className="changes-pill" role="group" aria-label="Changes in this workspace">
                <span>
                  <Icon name="filediff" size={18} />
                  {state.changedFiles} {state.changedFiles === 1 ? "file" : "files"} changed
                </span>
                <button type="button" aria-label="Review changes" onClick={() => reviewChanges("")}>
                  Review
                </button>
              </div>
            ) : null}
            <ChatComposerBox rest={atRest}>
              <AttachChips files={files} previews={previews.current} onRemove={removeFile} />
              <ChatComposerTextarea
                ref={composerRef}
                rows={1}
                value={draft}
                disabled={voiceBusy}
                placeholder={
                  recording
                    ? "Listening…"
                    : transcribing
                      ? "Transcribing…"
                      : offline
                        ? offline.placeholder
                        : starting
                        ? "The agent is starting… you can type your prompt"
                        : document.documentElement.dataset.host === "phone"
                          ? "Ask anything"
                          : state?.turns.some((turn) => turn.user)
                            ? "Message"
                            : "Send a message… (@ file · / commands · $ skills)"
                }
                aria-label="Chat prompt"
                spellCheck
                autoCorrect="on"
                autoCapitalize="sentences"
                enterKeyHint="send"
                onChange={(event) => {
                  recallRef.current = null;
                  setDraft(event.target.value);
                }}
                onKeyDown={onKeyDown}
                onPaste={onPaste}
                onFocus={offline?.onFocus}
                onCompositionStart={() => (composingRef.current = true)}
                onCompositionEnd={() => (composingRef.current = false)}
              />
              <ChatComposerToolbar>
{recording ? null : (
                <button
                  type="button"
                  className="ib add"
                  title={isOffline ? "Files, skills and commands are available once the Coordinator is running" : "Add: file or image, skills, commands"}
                  aria-label="Add to message"
                  aria-expanded={panel === "plus"}
                  disabled={voiceBusy || isOffline}
                  onClick={() => setPanel(panel === "plus" ? null : "plus")}
                >
                  <Icon name="plus" size={16} />
                </button>
)}
                {recording ? (
                  <button type="button" className="ib" title="Cancel dictation (Esc)" aria-label="Cancel dictation" onClick={cancelDictation}>
                    <Icon name="x" size={15} />
                  </button>
                ) : null}
                {canVoice ? (
                <button
                  type="button"
                  className={`ib${recording ? " recording" : ""}`}
                  title={
                    recording
                      ? "Stop and transcribe (⏎)"
                      : transcribing
                        ? "Transcribing…"
                        : `Dictate · ${state?.voiceSource === "chatgpt" ? "ChatGPT sign-in" : "API"}`
                  }
                  aria-label={recording ? "Stop dictation" : "Dictate"}
                  disabled={transcribing}
                  onClick={() => (recording ? stopDictation() : void startDictation())}
                >
                  {recording ? <StopIcon size={11} /> : transcribing ? <span className="spin" aria-hidden /> : <Icon name="mic" size={15} />}
                </button>
                ) : null}
                {recording ? <canvas ref={meterRef} className="mic-meter" width={44} height={28} aria-hidden /> : null}
                {recording ? (
                  <span className="rec-time" aria-live="off">
                    {Math.floor(recordSecs / 60)}:{String(recordSecs % 60).padStart(2, "0")}
                  </span>
                ) : null}
                <span className="sp" />
                {state?.contextTokens && !recording ? (
                  <ContextControl
                    used={state.contextTokens}
                    open={panel === "context"}
                    onToggle={() => setPanel(panel === "context" ? null : "context")}
                    onClose={() => setPanel(null)}
                    canCompact={disposition === "ready" && !sending && (catalog?.commands ?? []).some((c) => c.name === "/compact")}
                    compactHint={
                      (catalog?.commands ?? []).some((c) => c.name === "/compact")
                        ? disposition === "ready"
                          ? ""
                          : "Compact when the agent is idle"
                        : ""
                    }
                    onCompact={() => {
                      setPanel(null);
                      void send("/compact");
                    }}
                    onFraction={setCtxFraction}
                  />
                ) : null}
                {recording ? null : offline ? (
                  offline.chip ?? null
                ) : catalog?.canSwitchModel ? (
                  <button
                    type="button"
                    className="chip mono modelchip"
                    title={disposition === "ready" ? `Switch model (${shownModel})` : "Switch model when the agent is idle"}
                    aria-expanded={panel === "model"}
                    disabled={voiceBusy}
                    onClick={() => setPanel(panel === "model" ? null : "model")}
                  >
                    {shortModel(shownModel) || "Model"} <span aria-hidden>▾</span>
                  </button>
                ) : recording ? null : shownModel ? (
                  <span className="chip mono" title={shownModel}>{shortModel(shownModel)}</span>
                ) : null}
                {working && hasContent && !canQueueFollowUp ? (
                  <button
                    type="button"
                    className="intr"
                    title="Interrupt current turn and send message"
                    disabled={sending || interrupting || voiceBusy}
                    onClick={() => void interruptSend()}
                  >
                    {interrupting ? "Interrupting…" : "Interrupt & send"}
                  </button>
                ) : null}
                {showStop ? (
                  <button type="button" className="send stop" title="Stop" aria-label="Stop" onClick={() => void stop()}>
                    <StopIcon />
                  </button>
                ) : working && !canQueueFollowUp ? null : (
                  <button
                    type="button"
                    className="send"
                    title={working ? "Queue after this turn (⌘⏎ sends now)" : "Send"}
                    aria-label={working ? "Queue" : "Send"}
                    disabled={!(canSend || canQueue)}
                    onClick={() => void send()}
                  >
                    <Icon name="up" size={16} stroke={2.2} />
                  </button>
                )}
              </ChatComposerToolbar>
            </ChatComposerBox>
            {hasHint ? (
            <div className="hint">
              {note}
              {micDenied ? (
                <button type="button" className="hint-act" onClick={() => void chat.openMicrophoneSettings().catch(() => {})}>
                  Open System Settings
                </button>
              ) : null}
              {clipFailed && !transcribing ? (
                <button type="button" className="hint-act" onClick={retryClip}>
                  Retry
                </button>
              ) : null}
            </div>
            ) : null}
          </div>
        </div>
      )}
      {subOpen && openSub ? (
        <SubagentDrawer
          id={openSub}
          onWidth={setDrawerW}
          onWidthCommit={(width) => {
            setDrawerW(width);
            try {
              localStorage.setItem("solco.drawerWidth", String(width));
            } catch {
              // storage unavailable: the width just does not persist
            }
          }}
          onClose={() => setOpenSub(null)}
        />
      ) : null}
    </div>
    </SubagentContext.Provider>
  );
}

// turnKeys names each turn by its role and timestamp, so a row keeps its
// identity (and its measured height) when "Earlier messages" prepends turns;
// repeats of the same name are numbered in order.
function turnKeys(turns: ChatBridgeState["turns"]): string[] {
  const seen = new Map<string, number>();
  return turns.map((turn) => {
    const base = `${turn.user ? "u" : "a"}:${turn.tsMs ?? "x"}`;
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    return n === 0 ? base : `${base}#${n}`;
  });
}

// composeText is what goes to the agent: the typed text, then one @path
// mention per attached file (every CLI reads those as the file or image).
function composeText(draft: string, files: string[]): string {
  const text = draft.trim();
  // A path with whitespace is quoted: @"…" is read as one file by all four
  // agents (checked on real Claude Code, Codex, pi and omp sessions).
  const mentions = files.map((path) => (/\s/.test(path) ? `@"${path}"` : `@${path}`)).join(" ");
  return [text, mentions].filter(Boolean).join("\n");
}

function filesKey(conversationId: string): string {
  return `solco:chat-files:${conversationId}`;
}

function draftKey(conversationId: string): string {
  return `solco:chat-draft:${conversationId}`;
}

// TurnBoundary contains one turn's render failures: a bad turn renders as
// an inline notice and the rest of the transcript keeps working.
class TurnBoundary extends React.Component<
  { children: React.ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (this.state.failed) {
      return <div className="turn-error">This message can't be shown.</div>;
    }
    return this.props.children;
  }
}
