/**
 * Report pi's live state to spark, one small JSON file per session.
 *
 * Installed by spark ("Install for pi" in its banner) into ~/.pi/agent/extensions/. It only
 * writes files: it never changes a prompt, never blocks a tool, and never decides anything.
 *
 * The state comes from pi's own events, so a session spark can't otherwise see -- one
 * running in an IDE or a bare terminal -- still counts as working, blocked or idle. Two
 * things are reported: the state, including what a blocking dialog is asking for, and the
 * time of the user's last submitted prompt, which is how spark tells that they came back to
 * the session.
 */
import { mkdirSync, renameSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const DIR = join(process.env.HOME || homedir(), ".config", "spark", "pi");
// State changes are folded together: a run of tool results is one state, not ten
const FLUSH_MS = 700;
// A session can sit on one state for days -- a prompt left open over a weekend -- and spark
// treats an old file as a process that is gone, so say it is still here. The environment
// variable exists so the test can watch a beat without waiting five minutes
const HEARTBEAT_MS = Number(process.env.SPARK_PI_HEARTBEAT_MS) || 5 * 60 * 1000;
const ASK_TOOL = "ask_user_question";
// The identity of the copy that is running. Spark replaces this when it installs the file and
// compares what a session reports with what is on disk, which is what tells a session that
// loaded the current copy from one still running an older one. A runtime cannot read its own
// source reliably, so the identity has to be written into it. Bump the version in this marker
// with the file, since a hand-copied install keeps it as it is
const INSTALL_ID = "__SPARK_INSTALL_V1__";

type Block = { key: string; kind: string; tool: string };

export default function (pi: any): void {
  // A sub-agent session is a child of another session's file and spark doesn't show those
  if (process.env.PI_SUBAGENT_CHILD === "1") return;

  let sessionId = "";
  let sessionPath = "";
  let usable = false;
  let active = false;
  let lastInputAt = 0;
  let blocks: Block[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  let heartbeat: ReturnType<typeof setInterval> | undefined;
  let lastKey = "";

  function currentState(): string {
    const block = blocks[0];
    return block ? "blocked" : active ? "working" : "idle";
  }

  // When this process started, which lets spark tell a pid it still owns from one the system
  // has handed to somebody else since
  const startedAtMs =
    typeof process.uptime === "function" ? Date.now() - Math.round(process.uptime() * 1000) : 0;

  function flush(state?: string, force = false): void {
    if (!usable || !sessionPath) return;
    const block = blocks[0];
    const effective = state ?? currentState();
    const key = `${effective}|${block?.kind ?? ""}|${block?.tool ?? ""}|${lastInputAt}`;
    // The turn state repeats on every tool result; only a change is worth a write, unless
    // this is the heartbeat saying the process is still here
    if (!force && key === lastKey) return;
    const body = JSON.stringify({
      install: INSTALL_ID,
      sessionId,
      sessionPath,
      pid: process.pid,
      state: effective,
      blockKind: block?.kind ?? "",
      tool: block?.tool ?? "",
      updatedAtMs: Date.now(),
      startedAtMs,
      lastInputAtMs: lastInputAt,
    });
    const target = join(DIR, `${sessionId || sessionPath.replace(/[^A-Za-z0-9_-]/g, "_")}.json`);
    try {
      mkdirSync(DIR, { recursive: true });
      writeFileSync(`${target}.tmp`, body);
      renameSync(`${target}.tmp`, target);
      lastKey = key;
    } catch {
      // spark's own directory is best effort; failing to write must not disturb pi
    }
  }

  // stop cancels everything this instance has scheduled, so a replaced one cannot write over
  // what its successor reports
  function stop(): void {
    if (timer) clearTimeout(timer);
    timer = undefined;
    if (heartbeat) clearInterval(heartbeat);
    heartbeat = undefined;
  }

  function later(): void {
    if (timer) return;
    timer = setTimeout(() => {
      timer = undefined;
      flush();
    }, FLUSH_MS);
    (timer as any).unref?.();
  }

  pi.on("session_start", (_event: any, ctx: any) => {
    // Print, json and headless runs have no one looking at them, so they are not sessions
    // anyone will be told about
    const mode = ctx?.mode;
    usable = mode === "tui" || mode === "rpc";
    if (!usable) return;
    try {
      const file = ctx?.sessionManager?.getSessionFile?.();
      const id = ctx?.sessionManager?.getSessionId?.();
      sessionPath = typeof file === "string" ? file : "";
      sessionId = typeof id === "string" ? id : "";
    } catch {
      usable = false;
      return;
    }
    // An ephemeral session has no file for spark to scan
    if (!sessionPath) {
      usable = false;
      return;
    }
    // A reload replaces this extension mid-turn without another agent_start, so ask pi
    active = ctx?.isIdle?.() === false;
    blocks = [];
    lastKey = "";
    flush();
    stop();
    heartbeat = setInterval(() => flush(undefined, true), HEARTBEAT_MS);
    (heartbeat as any).unref?.();
  });

  pi.on("agent_start", () => {
    active = true;
    flush();
  });

  pi.on("agent_settled", () => {
    active = false;
    flush();
  });

  pi.on("ui_prompt_start", (event: any) => {
    blocks.push({ key: `ui:${event?.kind}`, kind: String(event?.kind ?? ""), tool: "" });
    flush();
  });

  pi.on("ui_prompt_end", (event: any) => {
    const key = `ui:${event?.kind}`;
    blocks = blocks.filter((b) => b.key !== key);
    flush();
  });

  pi.on("tool_call", (event: any) => {
    if (event?.toolName !== ASK_TOOL || event?.parentToolCallId) return;
    blocks.push({ key: `tool:${event.toolCallId}`, kind: "", tool: ASK_TOOL });
    flush();
  });

  pi.on("tool_result", (event: any) => {
    const key = `tool:${event?.toolCallId}`;
    if (!blocks.some((b) => b.key === key)) return;
    blocks = blocks.filter((b) => b.key !== key);
    flush();
  });

  // pi reports input when a prompt is submitted, not on each keystroke, so this is the
  // moment the user came back rather than a running record of them watching the output
  pi.on("input", (event: any) => {
    const source = event?.source;
    if (source !== "interactive" && source !== "rpc") return;
    lastInputAt = Date.now();
    later();
  });

  pi.on("session_shutdown", (event: any) => {
    // Whichever way the session ends, this instance stops reporting: a reload is followed by a
    // fresh instance, and a pending write from this one would overwrite its identity
    stop();
    if (!usable) return;
    if (event?.reason === "reload") return;
    active = false;
    blocks = [];
    flush("exited");
  });
}
