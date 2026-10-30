// A small Markdown renderer for the bot's replies: fenced code, headings,
// lists, quotes, paragraphs; inline code, bold and links. It builds React
// elements (never HTML strings), so nothing in a reply can inject markup.

import { Fragment, type ReactNode } from "react";

type Block =
  | { kind: "code"; lang: string; text: string }
  | { kind: "heading"; level: number; text: string }
  | { kind: "list"; ordered: boolean; items: string[] }
  | { kind: "quote"; text: string }
  | { kind: "para"; text: string };

const LIST_ITEM = /^\s*(?:[-*+]|(\d+)[.)])\s+(.*)$/;

function blocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    const fence = /^\s*(`{3,}|~{3,})\s*([\w+-]*)/.exec(line);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trimStart().startsWith(fence[1])) body.push(lines[i++]);
      i++; // the closing fence
      out.push({ kind: "code", lang: fence[2], text: body.join("\n") });
      continue;
    }

    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      out.push({ kind: "heading", level: heading[1].length, text: heading[2] });
      i++;
      continue;
    }

    const item = LIST_ITEM.exec(line);
    if (item) {
      const ordered = item[1] !== undefined;
      const items: string[] = [];
      while (i < lines.length) {
        const m = LIST_ITEM.exec(lines[i]);
        if (m && (m[1] !== undefined) === ordered) {
          items.push(m[2]);
        } else if (lines[i].trim() !== "" && /^\s{2,}/.test(lines[i]) && items.length) {
          items[items.length - 1] += "\n" + lines[i].trim(); // a continuation or nested line
        } else {
          break;
        }
        i++;
      }
      out.push({ kind: "list", ordered, items });
      continue;
    }

    if (/^\s*>/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) body.push(lines[i++].replace(/^\s*>\s?/, ""));
      out.push({ kind: "quote", text: body.join("\n") });
      continue;
    }

    if (line.trim() === "" || /^\s*(?:-{3,}|\*{3,})\s*$/.test(line)) {
      i++;
      continue;
    }

    const body: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^\s*(`{3,}|~{3,})/.test(lines[i]) &&
      !/^#{1,6}\s/.test(lines[i]) &&
      !LIST_ITEM.test(lines[i]) &&
      !/^\s*>/.test(lines[i])
    ) {
      body.push(lines[i++]);
    }
    out.push({ kind: "para", text: body.join("\n") });
  }

  return out;
}

const INLINE = /(`+)([\s\S]+?)\1|\*\*([\s\S]+?)\*\*|\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?，。；：！？）])/g;

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;

  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));

    if (m[2] !== undefined) {
      out.push(<code key={at}>{m[2]}</code>);
    } else if (m[3] !== undefined) {
      out.push(<strong key={at}>{inline(m[3])}</strong>);
    } else if (m[4] !== undefined) {
      out.push(
        <a key={at} href={m[5]} target="_blank" rel="noreferrer noopener">
          {m[4]}
        </a>,
      );
    } else {
      out.push(
        <a key={at} href={m[6]} target="_blank" rel="noreferrer noopener">
          {m[6]}
        </a>,
      );
    }
    last = at + m[0].length;
  }

  if (last < text.length) out.push(text.slice(last));

  return out;
}

/** Inline text with line breaks kept. */
function lines(text: string): ReactNode {
  return text.split("\n").map((l, i) => (
    <Fragment key={i}>
      {i > 0 && <br />}
      {inline(l)}
    </Fragment>
  ));
}

export function Markdown({ text }: { text: string }) {
  return (
    <div className="md">
      {blocks(text).map((b, i) => {
        switch (b.kind) {
          case "code":
            return (
              <pre key={i} data-lang={b.lang || undefined}>
                <code>{b.text}</code>
              </pre>
            );
          case "heading":
            return (
              <p key={i} className={`md-h md-h${Math.min(b.level, 3)}`}>
                {inline(b.text)}
              </p>
            );
          case "list": {
            const items = b.items.map((it, j) => <li key={j}>{lines(it)}</li>);

            return b.ordered ? <ol key={i}>{items}</ol> : <ul key={i}>{items}</ul>;
          }
          case "quote":
            return <blockquote key={i}>{lines(b.text)}</blockquote>;
          case "para":
            return <p key={i}>{lines(b.text)}</p>;
        }
      })}
    </div>
  );
}
