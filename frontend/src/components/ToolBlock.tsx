import { useState } from "react";
import type { ToolView } from "../agent";

/** summarizeArgs 把 JSON 参数压成一行摘要。 */
function summarizeArgs(args: string): string {
  const text = args.trim();
  if (!text) return "";
  try {
    const parsed: unknown = JSON.parse(text);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return Object.entries(parsed as Record<string, unknown>)
        .slice(0, 3)
        .map(([k, v]) => `${k}: ${preview(v)}`)
        .join("  ");
    }
  } catch {
    // 非对象 JSON（或截断的参数）直接展示原文。
  }
  return text.slice(0, 80);
}

function preview(v: unknown): string {
  if (typeof v === "string") return v.length > 24 ? v.slice(0, 23) + "…" : v;
  return String(v);
}

/** ToolBlock：折叠卡片 —— 名称 · 参数摘要 · 状态点，展开看完整输出。 */
export function ToolBlock({ tool }: { tool: ToolView }) {
  const [open, setOpen] = useState(false);
  const status = tool.running ? "running" : tool.isError ? "error" : "ok";
  return (
    <div className={`tool-block${open ? " open" : ""}`}>
      <button className="tool-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className={`status-dot ${status}`} title={status} />
        <span className="tool-name mono">{tool.name}</span>
        <span className="tool-args mono">{summarizeArgs(tool.args)}</span>
        <span className="tool-chevron" aria-hidden>
          ›
        </span>
      </button>
      {(open || tool.running || tool.isError) && (
        <div className="tool-body">
          <pre>{tool.result || (tool.running ? "运行中…" : "(无输出)")}</pre>
        </div>
      )}
    </div>
  );
}
