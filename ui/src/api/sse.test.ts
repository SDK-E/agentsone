// SPDX-License-Identifier: MIT
import { describe, expect, it } from "vitest";

import { readSse } from "./sse";

function bodyOf(...chunks: string[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
}

async function collect(body: ReadableStream<Uint8Array>) {
  const out = [];
  for await (const message of readSse(body)) out.push(message);
  return out;
}

describe("readSse", () => {
  it("reads messages split across chunks", async () => {
    const messages = await collect(
      bodyOf("id: 1\nevent: entry\nda", 'ta: {"a":1}\n\n', 'data: {"b":2}\n\n'),
    );

    expect(messages).toEqual([
      { id: "1", event: "entry", data: '{"a":1}' },
      { event: "message", data: '{"b":2}' },
    ]);
  });

  it("skips comments and joins multi-line data", async () => {
    const messages = await collect(bodyOf(": keep-alive\n\ndata: one\ndata: two\r\n\r\n"));

    expect(messages).toEqual([{ event: "message", data: "one\ntwo" }]);
  });

  it("yields a final message without a trailing blank line", async () => {
    const messages = await collect(bodyOf("data: last"));

    expect(messages).toEqual([{ event: "message", data: "last" }]);
  });
});
