// SPDX-License-Identifier: MIT
//
// A small, safe Markdown view for memory bodies. Agents write the text, so it is never trusted:
// nothing is turned into HTML. Every piece becomes a React element, written HTML stays text,
// and only web links (http and https) are clickable.
import { createElement, type ReactNode } from "react";

type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "code"; text: string }
  | { kind: "list"; ordered: boolean; items: string[] }
  | { kind: "paragraph"; text: string };

const BULLET = /^\s*[-*+]\s+(.*)$/;
const NUMBER = /^\s*\d+[.)]\s+(.*)$/;
const HEADING = /^(#{1,6})\s+(.*)$/;

function parseBlocks(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i] ?? "";
    if (line.trim() === "") {
      i += 1;
    } else if (line.trimStart().startsWith("```")) {
      const code: string[] = [];
      i += 1;
      while (i < lines.length && !(lines[i] ?? "").trimStart().startsWith("```")) {
        code.push(lines[i] ?? "");
        i += 1;
      }
      i += 1; // the closing fence, if there is one
      blocks.push({ kind: "code", text: code.join("\n") });
    } else if (HEADING.test(line)) {
      const match = HEADING.exec(line);
      blocks.push({ kind: "heading", level: match?.[1]?.length ?? 1, text: match?.[2] ?? "" });
      i += 1;
    } else if (BULLET.test(line) || NUMBER.test(line)) {
      const ordered = NUMBER.test(line);
      const pattern = ordered ? NUMBER : BULLET;
      const items: string[] = [];
      while (i < lines.length && pattern.test(lines[i] ?? "")) {
        items.push(pattern.exec(lines[i] ?? "")?.[1] ?? "");
        i += 1;
      }
      blocks.push({ kind: "list", ordered, items });
    } else {
      const text: string[] = [];
      while (
        i < lines.length &&
        (lines[i] ?? "").trim() !== "" &&
        !(lines[i] ?? "").trimStart().startsWith("```") &&
        !HEADING.test(lines[i] ?? "") &&
        !BULLET.test(lines[i] ?? "") &&
        !NUMBER.test(lines[i] ?? "")
      ) {
        text.push(lines[i] ?? "");
        i += 1;
      }
      blocks.push({ kind: "paragraph", text: text.join("\n") });
    }
  }
  return blocks;
}

/** The earliest of: `code`, **bold**, *italic*, [text](url). */
const INLINE = /`([^`]+)`|\*\*([^*]+)\*\*|\*([^*\n]+)\*|\[([^\]]+)\]\(([^)\s]+)\)/;

function isWebUrl(value: string): boolean {
  try {
    const { protocol } = new URL(value);
    return protocol === "https:" || protocol === "http:";
  } catch {
    return false;
  }
}

function inline(text: string, keyPrefix = "i"): ReactNode[] {
  const out: ReactNode[] = [];
  let rest = text;
  let n = 0;
  while (rest !== "") {
    const match = INLINE.exec(rest);
    if (!match) {
      out.push(rest);
      break;
    }
    if (match.index > 0) out.push(rest.slice(0, match.index));
    const key = `${keyPrefix}${String(n)}`;
    n += 1;
    const [, code, bold, italic, label, href] = match;
    if (code !== undefined) {
      out.push(<code key={key}>{code}</code>);
    } else if (bold !== undefined) {
      out.push(<strong key={key}>{inline(bold, `${key}b`)}</strong>);
    } else if (italic !== undefined) {
      out.push(<em key={key}>{inline(italic, `${key}e`)}</em>);
    } else if (label !== undefined && href !== undefined) {
      out.push(
        isWebUrl(href) ? (
          <a key={key} href={href} target="_blank" rel="noopener noreferrer">
            {label}
          </a>
        ) : (
          <span key={key}>{label}</span>
        ),
      );
    }
    rest = rest.slice(match.index + match[0].length);
  }
  return out;
}

/**
 * Shows Markdown as plain page elements. `headingLevel` is the level a top heading gets, so the
 * body fits under the heading of the page around it.
 */
export function Markdown({ text, headingLevel = 4 }: { text: string; headingLevel?: number }) {
  return (
    <div className="markdown">
      {parseBlocks(text).map((block, index) => {
        const key = `b${String(index)}`;
        switch (block.kind) {
          case "heading":
            return createElement(
              `h${String(Math.min(6, headingLevel + block.level - 1))}`,
              { key },
              inline(block.text),
            );
          case "code":
            return (
              <pre key={key}>
                <code>{block.text}</code>
              </pre>
            );
          case "list": {
            const items = block.items.map((item, at) => (
              <li key={`${key}-${String(at)}`}>{inline(item)}</li>
            ));
            return block.ordered ? <ol key={key}>{items}</ol> : <ul key={key}>{items}</ul>;
          }
          case "paragraph":
            return <p key={key}>{inline(block.text)}</p>;
        }
      })}
    </div>
  );
}
