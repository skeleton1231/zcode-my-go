import { useEffect, useRef, useState } from "react";

interface ComposerProps {
  running: boolean;
  disabled: boolean;
  onSend: (text: string) => void;
  onStop: () => void;
}

/** Composer：多行自动增高，Enter 发送 / Shift+Enter 换行，运行时变 Stop。 */
export function Composer({ running, disabled, onSend, onStop }: ComposerProps) {
  const [text, setText] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }, [text]);

  const send = () => {
    const value = text.trim();
    if (!value || running || disabled) return;
    setText("");
    onSend(value);
  };

  return (
    <div className="composer">
      <div className="composer-inner">
        <div className="composer-box">
          <textarea
            ref={ref}
            rows={1}
            value={text}
            placeholder={disabled ? "先选择或创建一个会话" : "描述任务… (Shift+Enter 换行)"}
            disabled={disabled}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                send();
              }
            }}
          />
          {running ? (
            <button className="stop" onClick={onStop}>
              停止
            </button>
          ) : (
            <button className="send" onClick={send} disabled={disabled || !text.trim()}>
              发送
            </button>
          )}
        </div>
        <div className="composer-hint">
          {running ? "Agent 运行中，可随时停止" : "Agent 在工作区内读写文件并运行命令"}
        </div>
      </div>
    </div>
  );
}
