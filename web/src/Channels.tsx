import { useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { channelDocs, channelFacts } from "./channelKinds";
import { ago, errorText, fullTime } from "./format";
import { useAction, usePoll } from "./hooks";
import { channelStatus } from "./status";
import { isSecret, secretText, type ChannelType, type ChannelView, type ProjectStatus, type TestResult } from "./types";
import { Icon } from "./icons";
import { Banner, ChannelBadge, ConfirmModal, Empty, Modal, Result, StateText, toast } from "./ui";

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
        {list.map((c) => {
          const type = typeOf(c.name);

          return (
            <ChannelCard
              key={c.name}
              view={c}
              type={type}
              project={p}
              // A channel this gateway no longer offers has no form to edit it with.
              onEdit={type && (() => setEditing({ type, current: c }))}
              onRemove={() => setRemoving(c)}
            />
          );
        })}

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
        <ConfirmModal
          title={`移除 ${removing.label}？`}
          confirm="移除"
          busyLabel="移除中…"
          onConfirm={() => api.removeChannel(p.id, removing.name).then(() => toast(`已移除 ${removing.label}`))}
          onDone={() => {
            setRemoving(null);
            changed();
          }}
          onClose={() => setRemoving(null)}
        >
          <p>gateway 会断开 {removing.label}，删掉它的凭证。之后发给这个渠道的消息不会再进来。</p>
        </ConfirmModal>
      )}
    </div>
  );
}

type Test = TestResult | "running" | null;

/** A credentials check: run it, and what it said; reset forgets the answer. */
function useTest() {
  const [test, setTest] = useState<Test>(null);

  const run = (check: () => Promise<TestResult>) => {
    setTest("running");
    check().then(setTest, (e) => setTest({ ok: false, error: errorText(e) }));
  };

  return { test, run, reset: () => setTest(null) };
}

function TestButton({ test, onClick }: { test: Test; onClick: () => void }) {
  return (
    <button type="button" className="btn btn-ghost" onClick={onClick} disabled={test === "running"}>
      {test === "running" ? "测试中…" : "测试连接"}
    </button>
  );
}

function TestLine({ test }: { test: Test }) {
  if (!test || test === "running") return null;

  return <Result tone={test.ok ? "ok" : "bad"}>{test.ok ? `凭证有效：${test.identity}` : test.error}</Result>;
}

function ChannelCard({ view, type, project: p, onEdit, onRemove }: { view: ChannelView; type?: ChannelType; project: ProjectStatus; onEdit?: () => void; onRemove: () => void }) {
  const test = useTest();
  const status = channelStatus(p, view.name);

  return (
    <article className={`card channel-card${status.tone === "bad" ? " card-bad" : ""}`}>
      <header className="card-head">
        <ChannelBadge name={view.name} size="lg" />
        <div className="card-title">
          <h2>{view.label || view.name}</h2>
          <StateText tone={status.tone} live={status.tone === "warn"}>
            {status.label}
          </StateText>
        </div>
      </header>

      <dl className="fields">
        {(type?.fields ?? []).map((f) => {
          const v = view.fields[f.key];

          return (
            <div key={f.key} className="field-row">
              <dt>{f.label}</dt>
              <dd className={isSecret(v) || !v ? "muted" : "mono"}>{isSecret(v) ? secretText(v) : v || "默认"}</dd>
            </div>
          );
        })}
      </dl>

      <TestLine test={test.test} />

      <div className="card-foot">
        <button type="button" className="btn" onClick={onEdit} disabled={!onEdit} title={onEdit ? undefined : "这个 gateway 不认识这种渠道，没法在这里编辑"}>
          编辑
        </button>
        <TestButton test={test.test} onClick={() => test.run(() => api.testChannel(p.id, view.name))} />
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
      type.fields.map((f) => {
        const v = current?.fields[f.key];

        return [f.key, current ? (typeof v === "string" ? v : "") : (f.default ?? "")];
      }),
    ),
  );
  const test = useTest();
  const saving = useAction();
  const where = channelFacts(type.name)?.console;

  const set = (key: string, v: string) => {
    setValues((xs) => ({ ...xs, [key]: v }));
    test.reset();
  };

  const save = (e: FormEvent) => {
    e.preventDefault();
    saving.run(
      () =>
        api.saveChannel(project.id, type.name, values).catch((err) => {
          throw err instanceof ApiError && err.status === 409 ? new Error(`这个应用已经属于另一个项目：${err.message}`) : err;
        }),
      onSaved,
    );
  };

  return (
    <Modal
      title={`${current ? "编辑" : "添加"} ${type.label}`}
      onClose={onClose}
      footer={
        <>
          <TestButton test={test.test} onClick={() => test.run(() => api.testChannel(project.id, type.name, values))} />
          <span className="grow" />
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="submit" form="channel-form" className="btn btn-primary" disabled={saving.busy}>
            {saving.busy ? "保存中…" : "保存"}
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
              {where && (
                <a href={where.href} target="_blank" rel="noreferrer noopener">
                  {where.label}
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
          const v = current?.fields[f.key];
          const kept = f.secret && isSecret(v) && v.set ? v : undefined;
          const id = `f-${f.key}`;

          return (
            <label key={f.key} className="form-row" htmlFor={id}>
              <span className="form-label">
                {f.label}
                {f.required && !kept && <span className="req"> *</span>}
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
                  placeholder={kept ? `${secretText(kept)}，留空则不修改` : f.placeholder}
                  required={f.required && !kept}
                />
              )}
            </label>
          );
        })}
        <p className="muted small">先点「测试连接」：它只问平台「我是谁」来核对凭证，不建立长连接，不会抢走正在运行的连接的消息。</p>
        <TestLine test={test.test} />
        {saving.error && <Result tone="bad">{saving.error}</Result>}
      </form>
    </Modal>
  );
}
