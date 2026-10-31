import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { chat, events, type ChatBridgeState, type ChatCatalog, type ChatCatalogEntry } from "./mygo";
import { Icon, StopIcon } from "./icons";
import { OutboxRow, TurnRow, type Outbox } from "./turn";
import { TocRail } from "./toc";
import { AttachChips, IMAGE_EXT, ModelPanel, PlusPanel, SlashPanel, fileToBase64, slashMatches } from "./composer";
import { BlockedDock, QuestionDock, SessionState, TasksTray, WorkingLine, isFreeTextOption, planFinished } from "./dock";
import "./chat.css";

/**
 * The Chat body of the /workspace WorkspacePrimarySurface, hosted by the
 * native window inside ui.WebView. The conversation runtime, send path and
 * queue stay in Go (internal/agent + internal/herdr): this page renders the
 * bridge projection and calls the bound service — presentation only.
 */

// The bridge mirrors conversation.PromptDisposition as "ready" | "queue" |
// "terminal" | "unknown"; the generated client types it as string.
export function App() {
  const [state, setState] = useState<ChatBridgeState | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [showJump, setShowJump] = useState(false);
  // Turns that arrived while the user was reading above (the pill's count).
  const [unread, setUnread] = useState(0);
  // ⌘F: the in-conversation find bar (WebKit's own window.find does the work).
  const [finding, setFinding] = useState(false);
  const [findQuery, setFindQuery] = useState("");
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
  // The prompt in flight: shown at once as "Sending…", and kept (with Retry /
  // Edit) when the agent did not take it, so a failure never loses the text.
  const [outbox, setOutbox] = useState<Outbox | null>(null);
  const [textPrompt, setTextPrompt] = useState<{ id: string; option: string } | null>(null);
  const [textAnswer, setTextAnswer] = useState("");
  const [answering, setAnswering] = useState(false);
  const [catalog, setCatalog] = useState<ChatCatalog | null>(null);
  const [panel, setPanel] = useState<"plus" | "model" | null>(null);
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
    return off;
  }, [refresh]);

  // Follow the app-resolved appearance (Settings -> General -> Appearance,
  // carried by the bridge state once it arrives); until then, the system
  // preference.
  const stateRef = useRef(state);
  stateRef.current = state;
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const theme = stateRef.current?.theme;
      document.documentElement.dataset.theme =
        theme ?? (media.matches ? "dark" : "light");
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
  // while scrolled up raise the "New messages" pill instead.
  useEffect(() => {
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

  const onLogScroll = () => {
    const log = logRef.current;
    if (!log) return;
    const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 48;
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
  // (session storage, keyed by conversation id).
  const conversationId = state?.conversationId ?? "";
  // A past session opened from Sessions: the transcript alone, nothing to
  // send, answer or queue.
  const readOnly = state?.readOnly === true;
  const openTerminal = () => void chat.openTerminal().catch(() => {});
  // A starter prompt only fills the composer; sending stays the user's act.
  const fillDraft = (text: string) => {
    setDraft(text);
    composerRef.current?.focus();
  };
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
    setDraft(sessionStorage.getItem(draftKey(conversationId)) ?? "");
    try {
      setFiles(JSON.parse(sessionStorage.getItem(filesKey(conversationId)) ?? "[]"));
    } catch {
      setFiles([]);
    }
  }, [conversationId]);
  useEffect(() => {
    if (conversationId) sessionStorage.setItem(draftKey(conversationId), draft);
  }, [draft, conversationId]);
  useEffect(() => {
    if (conversationId) sessionStorage.setItem(filesKey(conversationId), JSON.stringify(files));
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

  // The `/` `$` list: only while the draft is one leading token.
  const slashState = !panel && !slashOff && !recording && !transcribing ? slashMatches(catalog, draft) : { entries: [], start: 0 };
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
    const complete = slashState.start === 0 && entry.kind !== "skill" && entry.kind !== "prompt" && !entry.insert.endsWith(" ");
    if (complete && disposition === "ready" && !sending && !state?.loading) void send(entry.insert.trim());
    else acceptSlash(entry);
  };

  // @ completion: when the draft ends in an @token (no whitespace after
  // the last "@"), suggest the pane project's matching entries.
  useEffect(() => {
    const at = draft.lastIndexOf("@");
    if (at === -1) {
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
  }, [draft]);

  const acceptCompletion = (index: number) => {
    const at = draft.lastIndexOf("@");
    if (at === -1 || completions[index] === undefined) return;
    setDraft(`${draft.slice(0, at + 1)}${completions[index]} `);
    setCompletions([]);
    composerRef.current?.focus();
  };

  // Auto-grow the composer up to the viewport cap.
  useEffect(() => {
    const el = composerRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, Math.floor(window.innerHeight * 0.4))}px`;
  }, [draft]);

  const disposition = state?.disposition ?? "unknown";
  // A fresh pane (nothing sent yet) is "starting", never "working": its
  // composer holds Send until the agent is ready and shows no Stop.
  const starting = Boolean(state?.waiting) && disposition !== "ready";
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
  const canSend = disposition === "ready" && hasContent && !sending && !state?.loading && !voiceBusy;
  const canQueue = working && hasContent && !sending && !voiceBusy;
  // One composer action, as in the reference chat: while the agent works
  // with an empty box it is Stop (one Escape through the bridge); typing
  // swaps it back to Queue/Send.
  const showStop = working && !hasContent && !sending;

  // deliver hands one prompt to the bridge. The draft empties at once (the
  // text lives in the Sending… row from then on); a refusal turns that row
  // into Not delivered · Retry · Edit, an "uncertain" outcome never offers
  // Retry (the agent may already hold it), and anything else is delivered.
  const deliver = async (text: string, via: (text: string) => Promise<ChatBridgeState>) => {
    setOutbox({ text, status: "sending" });
    setDraft("");
    setFiles([]);
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

  // override sends that text alone (a command taken from the `/` list).
  const send = async (override?: string) => {
    const text = override ?? composeText(draft, files);
    if (text === "" || sending) return;
    setSending(true);
    try {
      await deliver(text, (t) => chat.send(t));
    } finally {
      setSending(false);
    }
  };
  const retryOutbox = () => {
    if (outbox) void send(outbox.text);
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
    if (canSend) void send();
  };

  const cancelQueued = async () => {
    setState(await chat.cancelQueued().catch(() => state));
  };

  const loadOlder = async () => {
    if (loadingOlder) return;
    setLoadingOlder(true);
    try {
      await chat.older();
      // The refreshed projection (and the anchoring effect) arrive through
      // the chat:updated event.
    } finally {
      setLoadingOlder(false);
    }
  };

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
    if (list.length === 0) return;
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
        const nodes = Array.from(log?.querySelectorAll<HTMLElement>(".turn.user") ?? []);
        if (!log || nodes.length === 0) return;
        event.preventDefault();
        const top = log.getBoundingClientRect().top;
        const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        // ↑ lands on the nearest prompt above the viewport's top edge, ↓ on the
        // nearest below it; past the last one ↓ is "back to the latest".
        const offsets = nodes.map((node) => node.getBoundingClientRect().top - top);
        const above = offsets.map((offset) => offset < -8).lastIndexOf(true);
        const below = offsets.findIndex((offset) => offset > 8);
        const at = event.key === "ArrowUp" ? above : below;
        if (at < 0 && event.key === "ArrowUp") {
          log.scrollTo({ top: 0, behavior: reduced ? "auto" : "smooth" });
        } else if (at < 0) {
          jumpToLatest();
        } else {
          stickToBottom.current = false;
          nodes[at].scrollIntoView({ block: "start", behavior: reduced ? "auto" : "smooth" });
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // handlers read current state through the render closure
    // eslint-disable-next-line react-hooks/exhaustive-deps
  });

  // ⌘F opens the find bar (any session, read-only included); Esc closes it.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey && event.key.toLowerCase() === "f") {
        event.preventDefault();
        setFinding(true);
        requestAnimationFrame(() => {
          findRef.current?.focus();
          findRef.current?.select();
        });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const runFind = (query: string, backwards: boolean) => {
    if (query === "") return;
    // window.find is WebKit's own search: it scrolls to and selects the match.
    (window as unknown as { find: (...args: unknown[]) => boolean }).find(query, false, backwards, true, false, false, false);
    findRef.current?.focus();
  };
  const closeFind = () => {
    setFinding(false);
    window.getSelection()?.removeAllRanges();
    composerRef.current?.focus();
  };

  const turns = state?.turns ?? [];
  const reviewChanges = (path: string) => {
    void chat.reviewChanges(path).catch(() => {});
  };
  const note =
    voiceError ||
    (micAsking ? "Waiting for microphone permission — answer the system prompt (it may be on another display)." : "") ||
    // A pending question or a terminal hand-off already says so in its own
    // card above; the line below the composer stays quiet then.
    (state?.interactions?.length || disposition === "terminal"
      ? ""
      : starting
        ? "Starting the agent…"
        : state?.waiting
          ? "Your first message starts the conversation."
          : working
        ? "Working • Queue appends a follow-up; Interrupt sends it now."
        : transcribing
          ? "Transcribing…"
          : recording
            ? "Listening… ⏎ or ■ finishes · Esc cancels"
            : "");

  return (
    <div
      className="chat"
      onDragOver={(event) => {
        if (readOnly || voiceBusy || !hasFiles(event)) return;
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(event) => {
        if (event.currentTarget === event.target) setDragging(false);
      }}
      onDrop={(event) => {
        if (readOnly || voiceBusy || !hasFiles(event)) return;
        event.preventDefault();
        setDragging(false);
        void takeFiles(Array.from(event.dataTransfer.files));
      }}
    >
      {dragging ? <div className="dropveil">Drop to attach</div> : null}
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
      {!state?.loading && !state?.waiting ? <TocRail turns={turns} logRef={logRef} /> : null}
      <div className="chat-log" ref={logRef} onScroll={onLogScroll}>
        <div className="col">
          {state?.hasOlder ? (
            <button type="button" className="older" onClick={() => void loadOlder()} disabled={loadingOlder}>
              {loadingOlder ? "Loading…" : "Earlier messages"}
            </button>
          ) : null}
          {state?.loading ? (
            <SessionState kind="loading" onOpenTerminal={openTerminal} />
          ) : state?.waiting ? (
            <SessionState
              kind="waiting"
              provider={state.provider}
              starting={starting}
              onPick={fillDraft}
              links={state.linkCandidates}
              onLink={(key) => void chat.linkSession(key).then(setState).catch(refresh)}
              onOpenTerminal={openTerminal}
            />
          ) : turns.length === 0 ? (
            // A bound session with no rows yet (the agent wrote its header
            // only) reads the same as a fresh pane: type to start.
            <SessionState kind="waiting" provider={state?.provider} onPick={fillDraft} onOpenTerminal={openTerminal} />
          ) : (
            turns.map((turn, index) => (
              // Older turns skip layout and paint until they scroll near the
              // viewport (content-visibility), so a long chat stays light.
              <TurnBoundary key={turnKey(turn, index)} far={index < turns.length - RECENT_TURNS}>
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
                />
              </TurnBoundary>
            ))
          )}
          {outbox ? (
            <OutboxRow outbox={outbox} onRetry={retryOutbox} onEdit={editOutbox} onDiscard={() => setOutbox(null)} />
          ) : null}
          {state?.echoPending && !outbox && !state.loading && lastTurn?.user ? (
            <div className="delivery sent" role="status">
              <Icon name="check" size={13} /> Sent
            </div>
          ) : null}
          {showPendingAssistant && !outbox ? (
            <div className="turn assistant pending" role="status" aria-live="polite">
              <WorkingLine sinceMs={lastTurn?.tsMs} />
            </div>
          ) : null}
        </div>
      </div>
      {showJump ? (
        <button type="button" className="jump" onClick={jumpToLatest}>
          {unread > 1 ? `${unread} new messages ↓` : "New messages ↓"}
        </button>
      ) : null}
      {readOnly ? null : (
        <div className="composer">
          <div className="col">
            {planVisible && state?.plan ? <TasksTray plan={state.plan} live={working} compact={Boolean(state.interactions?.length)} /> : null}
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
              <BlockedDock onOpenTerminal={openTerminal} />
            ) : null}
            {state?.outcome && !outbox ? <div className="outcome">{state.outcome}</div> : null}
            {state?.queuedText ? (
              <div className="queued">
                <span>Queued after turn</span>
                <button type="button" onClick={cancelQueued}>
                  Cancel
                </button>
              </div>
            ) : null}
            {slash.length > 0 && completions.length === 0 ? (
              <SlashPanel entries={slash} pick={slashPick} onPick={runSlash} onHover={setSlashPick} />
            ) : null}
            {panel === "plus" ? (
              <PlusPanel
                catalog={catalog}
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
                current={state?.model ?? ""}
                busy={switching || disposition !== "ready"}
                onSwitch={(id) => void switchModel(id)}
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
            <div className="cmp">
              <AttachChips files={files} previews={previews.current} onRemove={removeFile} />
              <textarea
                ref={composerRef}
                rows={1}
                value={draft}
                disabled={voiceBusy}
                placeholder={
                  recording
                    ? "Listening…"
                    : transcribing
                      ? "Transcribing…"
                      : starting
                        ? "The agent is starting… you can type your prompt"
                        : state?.waiting
                          ? "Type a prompt to start the conversation"
                          : "Send a message… (@ file · / commands · $ skills)"
                }
                aria-label="Chat prompt"
                onChange={(event) => {
                  recallRef.current = null;
                  setDraft(event.target.value);
                }}
                onKeyDown={onKeyDown}
                onPaste={onPaste}
                onCompositionStart={() => (composingRef.current = true)}
                onCompositionEnd={() => (composingRef.current = false)}
              />
              <div className="cbar">
                <button
                  type="button"
                  className="ib"
                  title="Add: file or image, skills, commands"
                  aria-label="Add to message"
                  aria-expanded={panel === "plus"}
                  disabled={voiceBusy}
                  onClick={() => setPanel(panel === "plus" ? null : "plus")}
                >
                  <Icon name="plus" size={16} />
                </button>
                {recording ? (
                  <button type="button" className="ib" title="Cancel dictation (Esc)" aria-label="Cancel dictation" onClick={cancelDictation}>
                    <Icon name="x" size={15} />
                  </button>
                ) : null}
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
                {recording ? <canvas ref={meterRef} className="mic-meter" width={44} height={28} aria-hidden /> : null}
                {recording ? (
                  <span className="rec-time" aria-live="off">
                    {Math.floor(recordSecs / 60)}:{String(recordSecs % 60).padStart(2, "0")}
                  </span>
                ) : null}
                <span className="sp" />
                {state?.contextTokens ? <ContextRing used={state.contextTokens} /> : null}
                {catalog?.canSwitchModel ? (
                  <button
                    type="button"
                    className="chip mono modelchip"
                    title={disposition === "ready" ? "Switch model" : "Switch model when the agent is idle"}
                    aria-expanded={panel === "model"}
                    disabled={voiceBusy}
                    onClick={() => setPanel(panel === "model" ? null : "model")}
                  >
                    {state?.model || "Model"} <span aria-hidden>▾</span>
                  </button>
                ) : state?.model ? (
                  <span className="chip mono">{state.model}</span>
                ) : null}
                {working && hasContent ? (
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
                ) : (
                  <button
                    type="button"
                    className="send"
                    title={working ? "Queue after this turn" : "Send"}
                    aria-label={working ? "Queue" : "Send"}
                    disabled={!(canSend || canQueue)}
                    onClick={() => void send()}
                  >
                    <Icon name="up" size={16} stroke={2.2} />
                  </button>
                )}
              </div>
            </div>
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
          </div>
        </div>
      )}
    </div>
  );
}

function turnKey(turn: ChatBridgeState["turns"][number], index: number): string {
  return `${index}:${turn.user ? "u" : "a"}`;
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
  { children: React.ReactNode; far?: boolean },
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
    return this.props.far ? <div className="far">{this.props.children}</div> : this.props.children;
  }
}

// The newest turns always render in full; older ones are paint-skipped.
const RECENT_TURNS = 24;

// ContextRing shows the provider-recorded context fill against the
// standard window, switching to the long window past it (web-ui's
// heuristic; hidden when the transcript records no usage).
function ContextRing({ used }: { used: number }) {
  const window = used > 200000 ? 1000000 : 200000;
  const fraction = Math.min(1, used / window);
  const low = fraction > 0.8;
  const radius = 7;
  const circumference = 2 * Math.PI * radius;
  return (
    <span
      className="ring-wrap"
      title={`Context ~${Math.round(used / 1000)}k of ${window / 1000}k tokens`}
    >
      <svg width="18" height="18" viewBox="0 0 18 18" aria-hidden>
        <circle cx="9" cy="9" r={radius} fill="none" stroke="var(--hair2)" strokeWidth="2.5" />
        <circle
          cx="9" cy="9" r={radius} fill="none"
          stroke={low ? "var(--danger)" : "var(--i3)"}
          strokeWidth="2.5"
          strokeDasharray={`${fraction * circumference} ${circumference}`}
          strokeLinecap="round"
          transform="rotate(-90 9 9)"
        />
      </svg>
    </span>
  );
}
