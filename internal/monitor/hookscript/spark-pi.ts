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
// Writing on every keystroke would be silly, so state changes are folded together
const FLUSH_MS = 700;
const ASK_TOOL = "ask_user_question";

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
  let lastWritten = "";

  function payload(state?: string): string {
    const block = blocks[0];
    return JSON.stringify({
      sessionId,
      sessionPath,
      pid: process.pid,
      state: state ?? (block ? "blocked" : active ? "working" : "idle"),
      blockKind: block?.kind ?? "",
      tool: block?.tool ?? "",
      updatedAtMs: Date.now(),
      lastInputAtMs: lastInputAt,
    });
  }

  function flush(state?: string): void {
    if (!usable || !sessionPath) return;
    const body = payload(state);
    // The turn state repeats on every tool result; only a real change is worth a write
    if (body === lastWritten) return;
    const target = join(DIR, `${sessionId || sessionPath.replace(/[^A-Za-z0-9_-]/g, "_")}.json`);
    try {
      mkdirSync(DIR, { recursive: true });
      writeFileSync(`${target}.tmp`, body);
      renameSync(`${target}.tmp`, target);
      lastWritten = body;
    } catch {
      // spark's own directory is best effort; failing to write must not disturb pi
    }
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
    lastWritten = "";
    flush();
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
    if (!usable) return;
    // A reload is followed by a fresh instance for the same session, which reports again
    if (event?.reason === "reload") return;
    active = false;
    blocks = [];
    flush("exited");
  });
}
