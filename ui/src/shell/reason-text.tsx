// SPDX-License-Identifier: MIT
//
// The daemon's reason text, in the contract's small format (AgentLimitsHold.reason): lines split
// by a newline, a line that starts with "- " is a list item, and text between two backticks is
// code. Nothing else has a meaning. It is never parsed as HTML or Markdown: every other character
// is shown as it is, and a backtick without its pair is just a backtick.
import { Fragment } from "react";

export interface Span {
  code: boolean;
  text: string;
}
export type Block = { kind: "line"; spans: Span[] } | { kind: "list"; items: Span[][] };

/** Splits one line into plain text and code. A last backtick without its pair stays text. */
export function spansOf(line: string): Span[] {
  const parts = line.split("`");
  const spans: Span[] = [];
  const paired = parts.length % 2 === 1 ? parts.length : parts.length - 1;
  for (let i = 0; i < paired; i++) {
    const text = parts[i] ?? "";
    if (text !== "") spans.push({ code: i % 2 === 1, text });
  }
  if (paired < parts.length) {
    const rest = "`" + (parts[paired] ?? "");
    const last = spans[spans.length - 1];
    if (last && !last.code) last.text += rest;
    else spans.push({ code: false, text: rest });
  }
  return spans;
}

/** Reads the reason format into lines and lists. Blank lines are dropped. */
export function blocksOf(text: string): Block[] {
  const blocks: Block[] = [];
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (line === "") continue;
    const last = blocks[blocks.length - 1];
    if (line.startsWith("- ")) {
      const item = spansOf(line.slice(2).trim());
      if (last?.kind === "list") last.items.push(item);
      else blocks.push({ kind: "list", items: [item] });
    } else {
      blocks.push({ kind: "line", spans: spansOf(line) });
    }
  }
  return blocks;
}

function Spans({ spans }: { spans: Span[] }) {
  return spans.map((s, i) => (
    <Fragment key={i}>{s.code ? <code className="reason__code">{s.text}</code> : s.text}</Fragment>
  ));
}

/** The reason as paragraphs, lists and code. */
export function ReasonText({ text }: { text: string }) {
  return (
    <div className="reason stack-sm">
      {blocksOf(text).map((b, i) =>
        b.kind === "line" ? (
          <p key={i}>
            <Spans spans={b.spans} />
          </p>
        ) : (
          <ul key={i} className="reason__list">
            {b.items.map((item, j) => (
              <li key={j}>
                <Spans spans={item} />
              </li>
            ))}
          </ul>
        ),
      )}
    </div>
  );
}
