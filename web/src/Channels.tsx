import { useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { ago, fullTime } from "./format";
import { usePoll } from "./hooks";
import type { ChannelType, ChannelView, ProjectStatus, SecretView, TestResult } from "./types";
import { channelDocs, consoles } from "./diagnose";
import { Icon } from "./icons";
import { Banner, ChannelBadge, Empty, Mark, Modal, errorText, toast } from "./ui";

interface Props {
  project: ProjectStatus;
  onChanged: () => void;
}

export function ChannelsPanel({ project: p, onChanged }: Props) {
  const channels = usePoll(() => api.channels(p.id), null, `channels-${p.id}`);
  const types = usePoll(() => api.channelTypes(p.id), null, `channel-types-${p.id}`);
  const [editing, setEditing] = useState<{ type: ChannelType; current?: ChannelView } | null>(null);
  const [removing, setRemoving] = useState<ChannelView | null>(null);

  const changed = () => {
    channels.reload();
    onChanged();
  };

  const loadErr = (!channels.data && channels.error) || (!types.data && types.error);
  if (loadErr)
    return (
      <Banner tone="bad">
        读不到渠道配置：{errorText(loadErr)}{" "}
        <button
          type="button"
          className="btn btn-sm"
          onClick={() => {
            channels.reload();
            types.reload();
          }}
        >
          重试
        </button>
      </Banner>
    );
  const list = channels.data;
  const kinds = types.data;
  if (!list || !kinds) return <div className="conv-loading" aria-busy="true" />;

  const typeOf = (name: string) => kinds.find((t) => t.name === name);
  const unused = kinds.filter((t) => !list.some((c) => c.name === t.name));

  return (
    <div className="channels">
      <p className="lede">凭证只存在本机的 gateway 数据库里。保存后几秒内自动重连，不用重启。一个应用只能属于一个项目。</p>

      <div className="cards">
        {list.map((c) => (
          <ChannelCard
            key={c.name}
            view={c}
            type={typeOf(c.name)}
            project={p}
            onEdit={() => setEditing({ type: typeOf(c.name)!, current: c })}
            onRemove={() => setRemoving(c)}
          />
        ))}

        {unused.length > 0 && <AddCard types={unused} onPick={(type) => setEditing({ type })} />}
        {!list.length && !unused.length && <Empty title="没有可用的渠道类型" />}
      </div>

      {editing && (
        <ChannelEditor
          project={p}
          type={editing.type}
          current={editing.current}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            changed();
            toast("已保存，gateway 几秒内用新配置重新连接");
          }}
        />
      )}

      {removing && (
        <ConfirmRemove
          project={p}
          channel={removing}
          onClose={() => setRemoving(null)}
          onDone={() => {
            setRemoving(null);
            changed();
          }}
        />
      )}
    </div>
  );
}

function ChannelCard({ view, type, project: p, onEdit, onRemove }: { view: ChannelView; type?: ChannelType; project: ProjectStatus; onEdit: () => void; onRemove: () => void }) {
  const [test, setTest] = useState<TestResult | "running" | null>(null);

  const stopped = p.stopped?.includes(view.name);
  const connected = p.connected?.includes(view.name);
  const status = stopped
    ? { tone: "bad" as const, text: "已断开" }
    : connected
      ? { tone: "ok" as const, text: "已连接" }
      : p.state === "starting"
        ? { tone: "warn" as const, text: "连接中" }
        : p.state === "failed"
          ? { tone: "bad" as const, text: "没连上" }
          : { tone: "off" as const, text: "未连接" };

  const runTest = () => {
    setTest("running");
    api.testChannel(p.id, view.name).then(setTest, (e) => setTest({ ok: false, error: errorText(e) }));
  };

  return (
    <article className={`card channel-card${stopped || status.text === "没连上" ? " card-bad" : ""}`}>
      <header className="card-head">
        <ChannelBadge name={view.name} size="lg" />
        <div className="card-title">
          <h2>{view.label || view.name}</h2>
          <span className={`state-text tone-${status.tone}`}>
            <Mark tone={status.tone} live={status.tone === "warn"} />
            {status.text}
          </span>
        </div>
      </header>

      <dl className="fields">
        {(type?.fields ?? []).map((f) => {
          const v = view.fields[f.key];
          const secret = typeof v === "object" ? (v as SecretView) : undefined;

          return (
            <div key={f.key} className="field-row">
              <dt>{f.label}</dt>
              <dd className={secret || !v ? "muted" : "mono"}>{secret ? (secret.set ? `已设置 ${secret.hint ?? ""}` : "未设置") : (v as string) || "默认"}</dd>
            </div>
          );
        })}
      </dl>

      {test && test !== "running" && (
        <p className={`result ${test.ok ? "result-ok" : "result-bad"}`} role="status">
          <Icon name={test.ok ? "check" : "cross"} />
          {test.ok ? `凭证有效：${test.identity}` : test.error}
        </p>
      )}

      <div className="card-foot">
        <button type="button" className="btn" onClick={onEdit}>
          编辑
        </button>
        <button type="button" className="btn btn-ghost" onClick={runTest} disabled={test === "running"}>
          {test === "running" ? "测试中…" : "测试连接"}
        </button>
        <span className="grow" />
        <span className="card-meta" title={`${view.app_key} · ${fullTime(view.updated_at)}`}>
          {ago(view.updated_at)}更新
        </span>
        <button type="button" className="btn btn-ghost btn-danger-text btn-sm" onClick={onRemove}>
          移除
        </button>
      </div>
    </article>
  );
}

function AddCard({ types, onPick }: { types: ChannelType[]; onPick: (t: ChannelType) => void }) {
  return (
    <article className="card card-add">
      <div className="card-title">
        <h2>添加渠道</h2>
        <p className="muted small">同一个 bot 可以同时接多个渠道，选一个开始：</p>
      </div>
      <div className="add-grid">
        {types.map((t) => (
          <button key={t.name} type="button" className="add-option" onClick={() => onPick(t)} disabled={!!t.unavailable} title={t.unavailable}>
            <ChannelBadge name={t.name} size="lg" />
            <span className="add-label">
              {t.label}
              {t.unavailable && <span className="add-why">{t.unavailable}</span>}
            </span>
          </button>
        ))}
      </div>
    </article>
  );
}

function ChannelEditor({ project, type, current, onClose, onSaved }: { project: ProjectStatus; type: ChannelType; current?: ChannelView; onClose: () => void; onSaved: () => void }) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      type.fields.map((f) => [f.key, current ? (typeof current.fields[f.key] === "string" ? (current.fields[f.key] as string) : "") : (f.default ?? "")]),
    ),
  );
  const [test, setTest] = useState<TestResult | "running" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const set = (key: string, v: string) => {
    setValues((xs) => ({ ...xs, [key]: v }));
    setTest(null);
  };

  const runTest = () => {
    setTest("running");
    api.testChannel(project.id, type.name, values).then(setTest, (e) => setTest({ ok: false, error: errorText(e) }));
  };

  const save = (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    api.saveChannel(project.id, type.name, values).then(onSaved, (err) => {
      setSaving(false);
      setError(err instanceof ApiError && err.status === 409 ? `这个应用已经属于另一个项目：${err.message}` : errorText(err));
    });
  };

  return (
    <Modal
      title={`${current ? "编辑" : "添加"} ${type.label}`}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="btn btn-ghost" onClick={runTest} disabled={test === "running"}>
            {test === "running" ? "测试中…" : "测试连接"}
          </button>
          <span className="grow" />
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="submit" form="channel-form" className="btn btn-primary" disabled={saving}>
            {saving ? "保存中…" : "保存"}
          </button>
        </>
      }
    >
      <form id="channel-form" className="form" onSubmit={save}>
        <div className="where">
          <ChannelBadge name={type.name} size="lg" />
          <div>
            <p className="where-title">凭证在哪里拿</p>
            <p className="where-links">
              {consoles[type.name] && (
                <a href={consoles[type.name].href} target="_blank" rel="noreferrer noopener">
                  {consoles[type.name].label}
                  <Icon name="external" />
                </a>
              )}
              <a href={channelDocs(type.name)} target="_blank" rel="noreferrer noopener">
                {type.label} 接入说明
                <Icon name="external" />
              </a>
            </p>
          </div>
        </div>
        {type.note && <p className="note">{type.note}</p>}
        {!current && type.add_note && <p className="note">{type.add_note}</p>}
        {type.fields.map((f) => {
          const secret = current?.fields[f.key] as SecretView | undefined;
          const id = `f-${f.key}`;

          return (
            <label key={f.key} className="form-row" htmlFor={id}>
              <span className="form-label">
                {f.label}
                {f.required && !(f.secret && secret?.set) && <span className="req"> *</span>}
              </span>
              {f.select || f.options ? (
                <select id={id} value={values[f.key]} onChange={(e) => set(f.key, e.target.value)}>
                  <option value="">默认</option>
                  {(f.options ?? []).map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  id={id}
                  type={f.secret ? "password" : "text"}
                  autoComplete="off"
                  spellCheck={false}
                  value={values[f.key]}
                  onChange={(e) => set(f.key, e.target.value)}
                  placeholder={f.secret && secret?.set ? `已设置 ${secret.hint ?? ""}，留空则不修改` : f.placeholder}
                  required={f.required && !(f.secret && secret?.set)}
                />
              )}
            </label>
          );
        })}
        <p className="muted small">先点「测试连接」：它只问平台「我是谁」来核对凭证，不建立长连接，不会抢走正在运行的连接的消息。</p>
        {test && test !== "running" && (
          <p className={`result ${test.ok ? "result-ok" : "result-bad"}`} role="status">
            <Icon name={test.ok ? "check" : "cross"} />
            {test.ok ? `凭证有效：${test.identity}` : test.error}
          </p>
        )}
        {error && <p className="result result-bad">{error}</p>}
      </form>
    </Modal>
  );
}

function ConfirmRemove({ project: p, channel: c, onClose, onDone }: { project: ProjectStatus; channel: ChannelView; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const confirm = () => {
    setBusy(true);
    api
      .removeChannel(p.id, c.name)
      .then(() => toast(`已移除 ${c.label}`))
      .then(onDone, (e) => {
        setBusy(false);
        setError(errorText(e));
      });
  };

  return (
    <Modal
      title={`移除 ${c.label}？`}
      onClose={onClose}
      footer={
        <>
          <span className="grow" />
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="button" className="btn btn-danger" onClick={confirm} disabled={busy}>
            {busy ? "移除中…" : "移除"}
          </button>
        </>
      }
    >
      <p>gateway 会断开 {c.label}，删掉它的凭证。之后发给这个渠道的消息不会再进来。</p>
      {error && <p className="result result-bad">{error}</p>}
    </Modal>
  );
}
