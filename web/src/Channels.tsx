import { useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { ago, fullTime, shortPath } from "./format";
import { usePoll } from "./hooks";
import { go } from "./route";
import type { ChannelType, ChannelView, ProjectStatus, SecretView, TestResult } from "./types";
import { Banner, ChannelBadge, Dot, Empty, Modal, errorText, toast } from "./ui";

interface Props {
  project: ProjectStatus;
  onChanged: () => void;
}

export function ChannelsPanel({ project: p, onChanged }: Props) {
  const channels = usePoll(() => api.channels(p.id), null, `channels-${p.id}`);
  const types = usePoll(() => api.channelTypes(p.id), null, `channel-types-${p.id}`);
  const [editing, setEditing] = useState<{ type: ChannelType; current?: ChannelView } | null>(null);
  const [removing, setRemoving] = useState<ChannelView | "project" | null>(null);

  const changed = () => {
    channels.reload();
    onChanged();
  };

  if (channels.error && !channels.data) return <Banner tone="bad">读不到渠道配置：{errorText(channels.error)}</Banner>;
  if (!channels.data || !types.data) return <div className="conv-loading" aria-busy="true" />;

  const typeOf = (name: string) => types.data!.find((t) => t.name === name);
  const unused = types.data.filter((t) => !channels.data!.some((c) => c.name === t.name));

  return (
    <div className="channels">
      <p className="lede">
        这个项目的 bot 接了哪些渠道。凭证存在本机的 gateway 数据库里，保存后 gateway 几秒内用新配置重新连接，不用重启。一个应用只能属于一个项目。
      </p>

      <div className="cards">
        {channels.data.map((c) => (
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
        {!channels.data.length && !unused.length && <Empty title="没有可用的渠道类型" />}
      </div>

      <section className="danger">
        <div>
          <h3>从 gateway 移除这个项目</h3>
          <p className="muted">
            gateway 不再服务它；配置写回 <code>{shortPath(p.dir)}/.claude-bot/config.json</code>，对话记录都保留，之后可以用 <code>claude-bot start</code> 单独运行，或者再 <code>claude-bot gateway import</code> 回来。
          </p>
        </div>
        <button type="button" className="btn btn-danger" onClick={() => setRemoving("project")}>
          移除项目
        </button>
      </section>

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
          what={removing}
          onClose={() => setRemoving(null)}
          onDone={() => {
            setRemoving(null);
            changed();
            if (removing === "project") go({ page: "home" });
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
    ? { tone: "bad" as const, text: "已停止" }
    : connected
      ? { tone: "ok" as const, text: "已连接" }
      : p.state === "starting"
        ? { tone: "warn" as const, text: "连接中" }
        : { tone: "off" as const, text: "未连接" };

  const runTest = () => {
    setTest("running");
    api.testChannel(p.id, view.name, {}).then(setTest, (e) => setTest({ ok: false, error: errorText(e) }));
  };

  return (
    <article className="card">
      <header className="card-head">
        <ChannelBadge name={view.name} />
        <div className="card-title">
          <h3>{view.label || view.name}</h3>
          <span className={`state state-${status.tone}`}>
            <Dot tone={status.tone} pulse={status.tone === "warn"} />
            {status.text}
          </span>
        </div>
      </header>

      {stopped && <p className="card-error">{p.errors?.[view.name]}</p>}

      <dl className="fields">
        {(type?.fields ?? []).map((f) => {
          const v = view.fields[f.key];
          const secret = typeof v === "object" ? (v as SecretView) : undefined;

          return (
            <div key={f.key} className="field-row">
              <dt>{f.label}</dt>
              <dd className={secret || !v ? "muted" : "mono"}>
                {secret ? (secret.set ? `已设置 ${secret.hint ?? ""}` : "未设置") : (v as string) || "默认"}
              </dd>
            </div>
          );
        })}
      </dl>

      <div className="card-foot">
        <span className="muted small" title={fullTime(view.updated_at)}>
          {view.app_key} · {ago(view.updated_at)}更新
        </span>
      </div>

      {test && test !== "running" && (
        <p className={`test ${test.ok ? "test-ok" : "test-bad"}`}>{test.ok ? `✓ 凭证有效：${test.identity}` : `✗ ${test.error}`}</p>
      )}

      <div className="card-actions">
        <button type="button" className="btn" onClick={onEdit}>
          编辑
        </button>
        <button type="button" className="btn btn-ghost" onClick={runTest} disabled={test === "running"}>
          {test === "running" ? "测试中…" : "测试连接"}
        </button>
        <span className="grow" />
        <button type="button" className="btn btn-ghost btn-danger-text" onClick={onRemove}>
          移除
        </button>
      </div>
    </article>
  );
}

function AddCard({ types, onPick }: { types: ChannelType[]; onPick: (t: ChannelType) => void }) {
  return (
    <article className="card card-add">
      <h3>添加渠道</h3>
      <p className="muted small">同一个 bot 可以同时接多个渠道。</p>
      <div className="add-grid">
        {types.map((t) => (
          <button key={t.name} type="button" className="add-option" onClick={() => onPick(t)} disabled={!!t.unavailable} title={t.unavailable}>
            <ChannelBadge name={t.name} />
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
        <p className="muted small">测试连接只核对凭证（问平台「我是谁」），不建立长连接，不会抢走正在运行的连接的消息。</p>
        {test && test !== "running" && (
          <p className={`test ${test.ok ? "test-ok" : "test-bad"}`}>{test.ok ? `✓ 凭证有效：${test.identity}` : `✗ ${test.error}`}</p>
        )}
        {error && <p className="test test-bad">{error}</p>}
      </form>
    </Modal>
  );
}

function ConfirmRemove({ project: p, what, onClose, onDone }: { project: ProjectStatus; what: ChannelView | "project"; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isProject = what === "project";

  const confirm = () => {
    setBusy(true);
    const done = isProject ? api.removeProject(p.id).then((r) => toast(`已移除 ${p.name}，配置写回 ${shortPath(r.config)}`)) : api.removeChannel(p.id, what.name).then(() => toast(`已移除 ${what.label}`));
    done.then(onDone, (e) => {
      setBusy(false);
      setError(errorText(e));
    });
  };

  return (
    <Modal
      title={isProject ? `移除项目 ${p.name}？` : `移除 ${what.label}？`}
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
      {isProject ? (
        <p>gateway 会断开这个项目的所有渠道，不再服务它。配置写回项目的 config.json，对话记录保留。</p>
      ) : (
        <p>gateway 会断开 {what.label}，删掉它的凭证。之后发给这个渠道的消息不会再进来。</p>
      )}
      {error && <p className="test test-bad">{error}</p>}
    </Modal>
  );
}
