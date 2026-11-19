// SPDX-License-Identifier: MIT
//
// A small Server-Sent Events reader over fetch. EventSource cannot send the fixture header in
// tests or report a refused stream quietly, so the interface reads the stream itself.

export interface SseMessage {
  id?: string;
  event: string;
  data: string;
}

function parseBlock(block: string): SseMessage | undefined {
  let event = "message";
  let id: string | undefined;
  const data: string[] = [];
  for (const line of block.split("\n")) {
    if (line === "" || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    const value = colon === -1 ? "" : line.slice(colon + 1).replace(/^ /, "");
    if (field === "data") data.push(value);
    else if (field === "event") event = value;
    else if (field === "id") id = value;
  }
  if (data.length === 0) return undefined;
  return { event, data: data.join("\n"), ...(id !== undefined && { id }) };
}

/** Yield each message of an event stream body until it ends. */
export async function* readSse(body: ReadableStream<Uint8Array>): AsyncGenerator<SseMessage> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true }).replace(/\r\n?/g, "\n");
    let end = buffer.indexOf("\n\n");
    while (end !== -1) {
      const message = parseBlock(buffer.slice(0, end));
      buffer = buffer.slice(end + 2);
      if (message) yield message;
      end = buffer.indexOf("\n\n");
    }
  }
  const last = parseBlock(buffer);
  if (last) yield last;
}
