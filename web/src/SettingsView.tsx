import { useState, type FormEvent, type ReactNode } from "react";
import { api } from "./api";
import { shortPath } from "./format";
import { usePoll } from "./hooks";
import type { Overview, SecretView, Settings } from "./types";
import { Banner, Dot, errorText, toast } from "./ui";

export function SettingsView({ overview }: { overview: Overview }) {
  const settings = usePoll(api.settings, null, "settings");

  if (settings.error && !settings.data) return <Banner tone="bad">读不到设置：{errorText(settings.error)}</Banner>;
  if (!settings.data) return <div className="conv-loading" aria-busy="true" />;

  const githubProjects = overview.projects.filter((p) => p.channels.includes("github"));

  return (
    <div className="settings">
      <header className="project-head">
        <div className="project-title">
          <h1>共用设置</h1>
        </div>
        <p className="muted">所有项目共用，只配一次。存在 {shortPath(overview.home)}/gateway.db，只有你能读。</p>
      </header>

      <SecretSetting
        title="TypeSafe API key"
        what="typesafe_key"
        value={settings.data.typesafe_key}
        help={
          <>
            每条消息先交给 TypeSafe 的 Jev 裁剪、归组、拦下非法请求，再交给 Claude。没有 key 时 gateway 不连接任何项目。在{" "}
            <a href="https://console.typesafe.ai/keys" target="_blank" rel="noreferrer noopener">
              console.typesafe.ai
            </a>{" "}
            创建。
          </>
        }
        missing="没有 key，所有项目都不会连接"
        onSaved={settings.reload}
      />

      <SecretSetting
        title="GitHub token"
        what="github_token"
        value={settings.data.github_token}
        help={
          githubProjects.length ? (
            <>保存前会用它访问每个接了 GitHub 渠道的项目的仓库（{githubProjects.map((p) => p.name).join("、")}），都能访问才保存。</>
          ) : (
            <>还没有项目接 GitHub 渠道。接了以后，这些项目都用这个 token 收 issue、PR 和评论并回复。</>
          )
        }
        missing={githubProjects.length ? "没有 token，GitHub 渠道连不上" : "未设置"}
        onSaved={settings.reload}
      />
    </div>
  );
}

function SecretSetting({
  title,
  what,
  value,
  help,
  missing,
  onSaved,
}: {
  title: string;
  what: keyof Settings;
  value: SecretView;
  help: ReactNode;
  missing: string;
  onSaved: () => void;
}) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    api.saveSettings({ [what]: input }).then(
      () => {
        setBusy(false);
        setInput("");
        onSaved();
        toast(`${title} 已验证并保存`);
      },
      (err) => {
        setBusy(false);
        setError(errorText(err));
      },
    );
  };

  return (
    <section className="card setting">
      <header className="card-head">
        <div className="card-title">
          <h3>{title}</h3>
          <span className={`state state-${value.set ? "ok" : "warn"}`}>
            <Dot tone={value.set ? "ok" : "warn"} />
            {value.set ? `已设置 ${value.hint ?? ""}` : missing}
          </span>
        </div>
      </header>
      <p className="muted">{help}</p>
      <form className="inline-form" onSubmit={save}>
        <input
          type="password"
          autoComplete="off"
          spellCheck={false}
          placeholder={value.set ? "输入新的值来替换" : "粘贴到这里"}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          aria-label={title}
        />
        <button type="submit" className="btn btn-primary" disabled={!input.trim() || busy}>
          {busy ? "验证中…" : "验证并保存"}
        </button>
      </form>
      {error && <p className="test test-bad">✗ {error}</p>}
    </section>
  );
}
