import { useState, type FormEvent, type ReactNode } from "react";
import { api } from "./api";
import { shortPath } from "./format";
import { usePoll } from "./hooks";
import type { GitHubProject, GitHubToken, Overview, SecretView, Settings } from "./types";
import { Icon } from "./icons";
import { Banner, Mark, Modal, errorText, toast } from "./ui";

export function SettingsView({ overview }: { overview: Overview }) {
  const settings = usePoll(api.settings, null, "settings");

  if (settings.error && !settings.data)
    return (
      <div className="page page-narrow">
        <Banner tone="bad">读不到设置：{errorText(settings.error)}</Banner>
      </div>
    );
  if (!settings.data) return <div className="page page-narrow loading" aria-busy="true" />;

  return (
    <div className="page page-narrow settings">
      <header className="phead">
        <h1>共用设置</h1>
        <p className="muted">
          所有项目共用，只配一次。存在 <span className="mono wrap-any">{shortPath(overview.home)}/gateway.db</span>，只有你能读。
        </p>
      </header>

      <SecretSetting
        title="TypeSafe API key"
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
        save={(v) => api.saveSettings({ typesafe_key: v })}
        onSaved={settings.reload}
      />

      <GitHubTokens settings={settings.data} onSaved={settings.reload} />
    </div>
  );
}

function SecretSetting({
  title,
  value,
  help,
  missing,
  save,
  onSaved,
}: {
  title: string;
  value: SecretView;
  help: ReactNode;
  missing: string;
  save: (v: string) => Promise<unknown>;
  onSaved: () => void;
}) {
  return (
    <section className="card setting">
      <header className="card-head">
        <div className="card-title">
          <h2>{title}</h2>
          <SecretState value={value} missing={missing} />
        </div>
      </header>
      <p className="muted">{help}</p>
      <ReplaceSecret label={title} value={value} save={save} onSaved={onSaved} />
    </section>
  );
}

function SecretState({ value, missing }: { value: SecretView; missing: string }) {
  return (
    <span className={`state-text tone-${value.set ? "ok" : "bad"}`}>
      <Mark tone={value.set ? "ok" : "bad"} />
      {value.set ? `已设置 ${value.hint ?? ""}` : missing}
    </span>
  );
}

/** A password field that verifies and saves a new value. */
function ReplaceSecret({ label, value, save, onSaved }: { label: string; value: SecretView; save: (v: string) => Promise<unknown>; onSaved: () => void }) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    save(input.trim()).then(
      () => {
        setBusy(false);
        setInput("");
        onSaved();
        toast(`${label} 已验证并保存`);
      },
      (err) => {
        setBusy(false);
        setError(errorText(err));
      },
    );
  };

  return (
    <>
      <form className="inline-form" onSubmit={submit}>
        <input
          type="password"
          autoComplete="off"
          spellCheck={false}
          placeholder={value.set ? "输入新的值来替换" : "粘贴到这里"}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          aria-label={label}
        />
        <button type="submit" className={`btn${input.trim() ? " btn-primary" : ""}`} disabled={!input.trim() || busy}>
          {busy ? "验证中…" : "验证并保存"}
        </button>
      </form>
      {error && (
        <p className="result result-bad">
          <Icon name="cross" />
          {error}
        </p>
      )}
    </>
  );
}

const hint = (v: SecretView) => (v.hint ? v.hint : "已设置");

/** The default GitHub token and the named ones, each with the projects it serves. */
function GitHubTokens({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [editing, setEditing] = useState<GitHubToken | "new" | null>(null);
  const [removing, setRemoving] = useState<GitHubToken | null>(null);
  const [def, ...named] = settings.github_tokens;
  const projects = settings.github_projects;

  return (
    <section className="card setting">
      <header className="card-head">
        <div className="card-title">
          <h2>GitHub token</h2>
          <SecretState value={def.token} missing={projects.length ? "没有默认 token，GitHub 渠道连不上" : "未设置"} />
        </div>
      </header>
      <p className="muted">接了 GitHub 的项目都用默认 token，一般一个就够。保存前会用它访问每个要服务的仓库，都能访问才保存。</p>

      <div className="token-block">
        <div className="token-line">
          <span className="token-name">默认 token</span>
          <Serves names={def.projects} empty={projects.length ? "没有项目用它" : "还没有项目接 GitHub"} />
        </div>
        <ReplaceSecret label="默认 GitHub token" value={def.token} save={(v) => api.saveSettings({ github_token: v })} onSaved={onSaved} />
      </div>

      <div className="token-block">
        <div className="token-line">
          <span className="token-name">其他账号的 token</span>
          <span className="muted small">有项目属于另一个账号（比如公司组织和个人仓库）时，加一个 token 并指定它服务哪些项目。</span>
        </div>
        {named.length > 0 && (
          <ul className="token-list">
            {named.map((t) => (
              <li key={t.name} className="token-row">
                <div className="token-main">
                  <span className="mono strong">{t.name}</span>
                  <span className={`small ${t.token.set ? "muted" : "tone-bad"}`}>{t.token.set ? hint(t.token) : "token 丢了，请重新填"}</span>
                </div>
                <Serves names={t.projects} empty="没有项目用它" />
                <div className="token-actions">
                  <button type="button" className="btn btn-sm" onClick={() => setEditing(t)}>
                    编辑
                  </button>
                  <button type="button" className="btn btn-sm btn-ghost btn-danger-text" onClick={() => setRemoving(t)}>
                    删除
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
        <div>
          <button type="button" className="btn" onClick={() => setEditing("new")} disabled={!projects.length} title={projects.length ? undefined : "还没有项目接 GitHub 渠道"}>
            <Icon name="plus" />
            添加 token
          </button>
        </div>
      </div>

      {editing && (
        <TokenEditor
          token={editing === "new" ? undefined : editing}
          projects={projects}
          taken={named.map((t) => t.name)}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            onSaved();
          }}
        />
      )}

      {removing && (
        <ConfirmRemoveToken
          token={removing}
          onClose={() => setRemoving(null)}
          onDone={() => {
            setRemoving(null);
            onSaved();
          }}
        />
      )}
    </section>
  );
}

function Serves({ names, empty }: { names: string[]; empty: string }) {
  return <span className="token-serves small">{names.length ? `服务：${names.join("、")}` : <span className="muted">{empty}</span>}</span>;
}

function TokenEditor({
  token,
  projects,
  taken,
  onClose,
  onSaved,
}: {
  token?: GitHubToken;
  projects: GitHubProject[];
  taken: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(token?.name ?? "");
  const [value, setValue] = useState("");
  const [serve, setServe] = useState<number[]>(() => projects.filter((p) => token && p.token_name === token.name).map((p) => p.id));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const nameOK = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,39}$/.test(name);
  const nameTaken = !token && taken.includes(name);

  const save = (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    api.saveGitHubToken(name, value.trim(), serve).then(
      () => {
        toast(`GitHub token「${name}」已验证并保存`);
        onSaved();
      },
      (err) => {
        setSaving(false);
        setError(errorText(err));
      },
    );
  };

  const toggle = (id: number) => setServe((xs) => (xs.includes(id) ? xs.filter((x) => x !== id) : [...xs, id]));

  return (
    <Modal
      title={token ? `编辑 GitHub token「${token.name}」` : "添加 GitHub token"}
      onClose={onClose}
      footer={
        <>
          <span className="grow" />
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="submit" form="token-form" className="btn btn-primary" disabled={saving || !nameOK || nameTaken || (!token && !value.trim())}>
            {saving ? "验证中…" : "验证并保存"}
          </button>
        </>
      }
    >
      <form id="token-form" className="form" onSubmit={save}>
        <label className="form-row" htmlFor="t-name">
          <span className="form-label">
            名字<span className="req"> *</span>
          </span>
          <input id="t-name" type="text" value={name} onChange={(e) => setName(e.target.value)} disabled={!!token} placeholder="比如 work、oss" autoComplete="off" spellCheck={false} />
          {name && !nameOK && <span className="small state-bad">只能用字母、数字、_ . -，最多 40 个</span>}
          {nameTaken && <span className="small state-bad">已经有叫这个名字的 token</span>}
        </label>
        <label className="form-row" htmlFor="t-token">
          <span className="form-label">
            Token{!token && <span className="req"> *</span>}
          </span>
          <input
            id="t-token"
            type="password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder={token?.token.set ? `已设置 ${token.token.hint ?? ""}，留空则不修改` : "ghp_… 或 github_pat_…"}
            autoComplete="off"
            spellCheck={false}
          />
        </label>
        <fieldset className="form-row token-projects">
          <legend className="form-label">服务哪些项目</legend>
          {projects.map((p) => (
            <label key={p.id} className="check">
              <input type="checkbox" checked={serve.includes(p.id)} onChange={() => toggle(p.id)} />
              <span>
                {p.name} <span className="muted mono small">{p.repo}</span>
              </span>
              <span className="muted small">现在用：{p.token_name ? `「${p.token_name}」` : "默认"}</span>
            </label>
          ))}
          <span className="muted small">不勾的项目用默认 token；原来用这个 token、现在不勾的项目，改回默认 token。</span>
        </fieldset>
        {error && (
          <p className="result result-bad">
            <Icon name="cross" />
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}

function ConfirmRemoveToken({ token, onClose, onDone }: { token: GitHubToken; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const confirm = () => {
    setBusy(true);
    api.removeGitHubToken(token.name).then(
      () => {
        toast(`已删除 GitHub token「${token.name}」`);
        onDone();
      },
      (e) => {
        setBusy(false);
        setError(errorText(e));
      },
    );
  };

  return (
    <Modal
      title={`删除 GitHub token「${token.name}」？`}
      onClose={onClose}
      footer={
        <>
          <span className="grow" />
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="button" className="btn btn-danger" onClick={confirm} disabled={busy || token.projects.length > 0}>
            {busy ? "删除中…" : "删除"}
          </button>
        </>
      }
    >
      {token.projects.length ? (
        <p>它还在服务 {token.projects.join("、")}。先在「编辑」里取消勾选，让它们改用默认 token，再删除。</p>
      ) : (
        <p>没有项目在用它，删除后 gateway 里就没有这个 token 了。</p>
      )}
      {error && <p className="result result-bad">{error}</p>}
    </Modal>
  );
}
