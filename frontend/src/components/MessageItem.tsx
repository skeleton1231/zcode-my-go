import type { MessageView } from "../agent";
import { renderInline, splitFences } from "../markdown";

/** AssistantMessage：无气泡直接排版，代码块独立成块。 */
function AssistantMessage({ msg }: { msg: MessageView }) {
  const segments = splitFences(msg.text);
  return (
    <div className="msg-assistant">
      {segments.map((seg, i) =>
        seg.kind === "code" ? (
          <pre key={i}>
            <code>{seg.text}</code>
          </pre>
        ) : (
          <p key={i} style={{ margin: 0 }}>
            {renderInline(seg.text)}
            {msg.streaming && i === segments.length - 1 && <span className="cursor" aria-hidden />}
          </p>
        ),
      )}
      {msg.streaming && segments.length === 0 && <span className="cursor" aria-hidden />}
      {msg.error && <div className="error-banner">{msg.error}</div>}
    </div>
  );
}

/** MessageItem 渲染一条聊天消息。 */
export function MessageItem({ msg }: { msg: MessageView }) {
  if (msg.role === "user") {
    return <div className="msg-user">{msg.text}</div>;
  }
  return <AssistantMessage msg={msg} />;
}
