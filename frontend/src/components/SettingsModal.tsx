import { useEffect, useState } from "react";

export interface SettingsView {
  baseUrl: string;
  model: string;
  hasApiKey: boolean;
  keyHint: string;
}

interface SettingsModalProps {
  open: boolean;
  settings: SettingsView | null;
  onClose: () => void;
  onSave: (baseUrl: string, apiKey: string, model: string) => Promise<unknown>;
}

/** SettingsModal：baseURL / API key / 模型。key 只写入不回显。 */
export function SettingsModal({ open, settings, onClose, onSave }: SettingsModalProps) {
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open && settings) {
      setBaseUrl(settings.baseUrl);
      setModel(settings.model);
      setApiKey("");
      setError("");
    }
  }, [open, settings]);

  if (!open) return null;

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await onSave(baseUrl.trim(), apiKey.trim(), model.trim());
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      className="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal" role="dialog" aria-label="设置">
        <h2>设置</h2>
        <div className="field">
          <label htmlFor="base-url">API Base URL（Anthropic 兼容端点）</label>
          <input
            id="base-url"
            value={baseUrl}
            placeholder="https://api.anthropic.com"
            onChange={(e) => setBaseUrl(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="api-key">
            API Key{settings?.hasApiKey ? `（已配置 ${settings.keyHint}，留空保持不变）` : ""}
          </label>
          <input
            id="api-key"
            type="password"
            value={apiKey}
            placeholder={settings?.hasApiKey ? "" : "sk-…"}
            onChange={(e) => setApiKey(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="model">模型 ID</label>
          <input
            id="model"
            value={model}
            placeholder="claude-sonnet-4-5 / glm-5.2 / …"
            onChange={(e) => setModel(e.target.value)}
          />
        </div>
        {error && <div className="error-banner">{error}</div>}
        <div className="actions">
          <button className="btn ghost" onClick={onClose}>
            取消
          </button>
          <button className="btn primary" onClick={save} disabled={saving || !model.trim()}>
            {saving ? "保存中…" : "保存"}
          </button>
        </div>
      </div>
    </div>
  );
}
