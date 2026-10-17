/**
 * markdown.ts 渲染 assistant 文本：代码块转 <pre><code>，行内代码与粗体
 * 转义后替换。只产出已知安全的标签结构（React 元素），不使用
 * innerHTML，模型输出中的任何 HTML 都按字面文本展示。
 */
import type { ReactNode } from "react";

interface Segment {
  kind: "text" | "code";
  text: string;
  lang?: string;
}

/** splitFences 把文本切成普通段与围栏代码块。 */
export function splitFences(input: string): Segment[] {
  const segments: Segment[] = [];
  let buffer = "";
  let inFence = false;
  let fenceLang = "";
  for (const line of input.split("\n")) {
    const fence = /^```(.*)$/.exec(line.trimEnd());
    if (fence) {
      if (!inFence) {
        if (buffer) segments.push({ kind: "text", text: buffer });
        buffer = "";
        inFence = true;
        fenceLang = fence[1].trim();
      } else {
        segments.push({ kind: "code", text: buffer, lang: fenceLang });
        buffer = "";
        inFence = false;
        fenceLang = "";
      }
      continue;
    }
    buffer += (buffer ? "\n" : "") + line;
  }
  if (inFence) segments.push({ kind: "code", text: buffer, lang: fenceLang });
  else if (buffer) segments.push({ kind: "text", text: buffer });
  return segments;
}

/** renderInline 处理行内 `code` 与 **bold**，其余为字面文本。 */
export function renderInline(text: string): ReactNode[] {
  const parts: ReactNode[] = [];
  let rest = text;
  let key = 0;
  const inline = /`([^`\n]+)`|\*\*([^*\n]+)\*\*/g;
  let match: RegExpExecArray | null;
  let last = 0;
  while ((match = inline.exec(rest)) !== null) {
    if (match.index > last) parts.push(rest.slice(last, match.index));
    if (match[1] !== undefined) {
      parts.push(<code key={key++}>{match[1]}</code>);
    } else {
      parts.push(<strong key={key++}>{match[2]}</strong>);
    }
    last = match.index + match[0].length;
  }
  if (last < rest.length) parts.push(rest.slice(last));
  return parts;
}
