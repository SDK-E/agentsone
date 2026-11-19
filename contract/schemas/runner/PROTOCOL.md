# Runner protocol 1.0

A runner is the program that thinks for one agent. The daemon, `agentsoned`, starts one runner process per
agent (in the agent's cgroup `/agents/<id>`) and talks to it over this protocol. Tools never travel over this
protocol: the runner reaches the machine through the MCP endpoint it receives in the welcome.

The exact message shapes are in [`messages.schema.json`](messages.schema.json). The method list, in
OpenRPC 1.3 form, is in [`runner.openrpc.json`](runner.openrpc.json). This guide explains how the pieces
fit together. The echo runner is the conformance test: it must produce the transcript at the end of this
guide byte for byte, apart from timing values.

## Transport and framing

- The daemon listens on a Unix stream socket and passes its path to the runner in the environment variable
  `AGENTSONE_RUNNER_SOCKET`. By convention the path is `/run/agentsone/runners/<agent>.sock`. The runner
  connects once at start. One connection carries one runner.
- Every message is one JSON-RPC 2.0 object encoded as UTF-8 on one line, ended by a single `\n` (byte
  0x0A). JSON escapes newlines inside strings, so a message never contains a raw newline. Senders must not
  pretty-print and must not send a byte order mark.
- A line is at most `config.maxLineBytes` bytes including the newline (1 MiB, 1048576, by default). A
  receiver that sees a longer line discards it up to the next newline and answers with error -32001 and
  `id: null`. Split long output into several `output` chunks instead.
- Batches (JSON arrays) are not used. A receiver answers a batch with -32600 and `id: null`.
- Both sides send requests. Each side numbers its own requests 1, 2, 3 and so on for the life of the
  connection; ids are integers. A response carries the id of the request it answers, so the two id
  sequences never get mixed up even when they overlap.
- A request expects exactly one response, either `result` or `error`. A notification has no `id` and gets
  no response.

## Messages

| Method | Direction | Kind | Purpose |
| --- | --- | --- | --- |
| `hello` | runner to daemon | request | Introduce the runner. The result is the welcome. |
| `wake` | daemon to runner | request | Start a turn for a message, task, event, timer or chat. |
| `deliver` | daemon to runner | request | Hand a bus or chat message to a turn that is running. |
| `cancel` | daemon to runner | request | Stop a turn. |
| `sleep` | daemon to runner | request | Compact the session to a summary and go idle. |
| `shutdown` | daemon to runner | request | Stop the process. |
| `bus.send` | runner to daemon | request | Send a message to another agent. |
| `task.done` | runner to daemon | request | Report that work on a task ended. |
| `status` | runner to daemon | notification | State changed: idle, working, sleeping or error. |
| `output` | runner to daemon | notification | A chunk of turn or chat text. |
| `usage` | runner to daemon | notification | Tokens and cost estimate for model calls. |
| `profile.update` | runner to daemon | notification | The one-line profile, mandatory every turn. |
| `log` | runner to daemon | notification | A diagnostic line. |
| `error` | runner to daemon | notification | A failure outside a request. |
| `heartbeat` | runner to daemon | notification | Liveness signal. |

The welcome is the result of `hello`; it is not a separate method.

## Handshake

1. The runner connects and sends `hello` with every protocol version it speaks, its name and version, and
   its capabilities. It must send `hello` within 10 seconds of connecting, or the daemon closes the
   socket.
2. The daemon picks the highest version both sides speak and answers with the welcome. The welcome holds
   the agent's identity, the chosen `protocolVersion`, the MCP endpoint and scoped token, the model
   gateway (base URL, the model name `autoagents/one`, weight hints, scoped token), the compacted session
   summary, and the config (heartbeat interval and timeout, line limit, and runner-specific settings).
3. If no version matches, the daemon answers with error -32002, `data: {"supported": [...]}`, and closes
   the socket. The runner exits with a non-zero status.
4. After the welcome the runner sends `status` with state `idle`. Only then may the daemon send `wake`.

Before the handshake ends, any message other than `hello` from the runner, or a second `hello`, gets
error -32000 (out of order). The daemon never sends a request before it has sent the welcome.

Tokens in the welcome are secrets scoped to this agent. Runners must never log them, store them in memory
records, or send them on the bus.

## Version negotiation

Versions are `<major>.<minor>`. Within one major version, a newer minor only adds optional fields, new
notifications or new error codes. Every object is closed (unknown fields are invalid), so a sender only
sends fields and methods that exist in the version chosen for the connection. A new
major version may change anything. The daemon supports the current and the previous major version.

## Turns

A turn is one unit of work with an id (`trn_<ULID>`) chosen by the daemon.

1. The daemon sends `wake` with a reason (`message`, `task`, `event`, `timer` or `chat`), the turn id and
   the payload for that reason. The runner answers at once with an empty result.
2. The runner sends `status` `working` with the turn id.
3. During the turn the runner may send `output` chunks (index from 0, `final: true` on the last), `usage`,
   `log`, `bus.send` and `task.done`. The daemon may `deliver` more messages into the running turn.
4. **The runner must send at least one `profile.update` in every turn**, before the status that ends the
   turn. The line is at most 255 characters with no line break and says the agent's goal and what it is
   doing now, for example `Goal: keep the machine safe. Now: checking failed root logins.` The router
   reads these lines. A turn that ends without one is recorded as a fault in the agent's audit trail.
5. The runner ends the turn with `status` `idle` and the turn id.

A runner works on one turn at a time. A `wake` that arrives during a turn is acknowledged and queued; the
runner starts it after the current turn ends.

## Cancel, sleep and shutdown

- `cancel` stops the named turn. The runner answers with an empty result, stops as soon as it can, and
  sends `status` `idle` with the turn id. Cancelling a turn that already ended is not an error.
- `sleep` asks the runner to compact its session into a Markdown summary of at most `maxSummaryChars`
  characters. The runner answers with the summary and sends `status` `sleeping`. The daemon stores the
  summary and passes it back in `session.summary` of the next welcome, so the daemon may stop a sleeping
  runner process; sleeping costs nothing.
- `shutdown` asks the runner to stop. The runner answers with an empty result, ends any turn, closes the
  socket and exits with status 0 within `graceMs` milliseconds. After that the daemon stops it by force.

## Heartbeats

The runner sends `heartbeat` every `config.heartbeatIntervalMs` (15 seconds by default), including during
turns. If the daemon hears nothing at all from the runner for `config.heartbeatTimeoutMs` (45 seconds by
default, three intervals), it records error -32003, stops the process and starts it again. The runner
detects a dead daemon when the socket closes; it then exits with a non-zero status and systemd restarts
it.

## Budget and resume

The model gateway refuses calls when the agent's budget share is zero. When that happens:

1. The runner keeps the turn's state (conversation so far, the call it was making).
2. It sends `error` with code 1010 and `fatal: false`, then `status` `idle` with `pausedTurnId` set to the
   paused turn.
3. When budget returns, the daemon sends `wake` with reason `event`, the event
   `agentsone.agents.budget.restored`, and `turnId` equal to the paused turn. The runner resumes that turn
   where it stopped, repeating the refused call, and finishes it as usual.
4. If the runner process was restarted in between, the welcome carries `session.pausedTurnId`. A runner
   that cannot resume (capability `resume: false`, or state lost) answers the resume `wake` with error
   1011; the daemon then treats the paused turn as ended and wakes the runner again with the original
   reason when it has one.

## Errors

| Code | Name | Meaning |
| --- | --- | --- |
| -32700 | Parse error | The line is not valid JSON. |
| -32600 | Invalid request | Not a valid JSON-RPC 2.0 object, or a batch. |
| -32601 | Method not found | Unknown method, or a method sent in the wrong direction. |
| -32602 | Invalid params | Params do not match the schema. |
| -32603 | Internal error | The receiver failed while handling a valid request. |
| -32000 | Out of order | Message before the handshake, second `hello`, or a request for a capability the runner does not have. |
| -32001 | Line too large | The line was longer than `maxLineBytes`. |
| -32002 | Unsupported protocol version | No version in common; `data.supported` lists the daemon's versions. |
| -32003 | Heartbeat timeout | Recorded by the daemon when a runner goes silent; never sent on the wire. |
| 1001 | Unknown turn | The turn id is not the current or a queued turn. |
| 1002 | Unknown recipient | `bus.send` named an agent that does not exist. |
| 1003 | Not permitted | The agent may not do this (for example, finishing another agent's task). |
| 1004 | Unknown task | `task.done` named a task that does not exist. |
| 1010 | Budget exhausted | The gateway refused a model call for budget. |
| 1011 | Resume unavailable | The runner cannot resume a paused turn. |
| 1020 | Model unavailable | The gateway could not reach any allowed model. |
| 1030 | Rate limited | The agent sent too many bus messages or model calls. |

The codes from -32768 to -32000 follow JSON-RPC 2.0; Agents One uses -32000 to -32003 from the range
JSON-RPC leaves to implementations. Codes from 1000 to 1999 are Agents One application errors. Error
responses carry `id: null` only when the request could not be read at all.

## Conformance rules

Each rule is one test in the echo runner conformance suite. "Runner" rules are checked against a runner
driven by a fake daemon; "daemon" rules are checked against the daemon driven by a fake runner.

| Id | Side | Rule |
| --- | --- | --- |
| F1 | both | Every line is one JSON object that validates against `messages.schema.json`, ends with one `\n`, and has no other raw newline. |
| F2 | both | No line is longer than `maxLineBytes`, newline included. |
| F3 | both | A line longer than `maxLineBytes` is answered with error -32001 and `id: null`, and the connection stays open. |
| F4 | both | A line that is not JSON is answered with -32700 and `id: null`; a JSON array with -32600 and `id: null`. |
| F5 | both | Request ids from one sender are integers that start at 1 and rise by 1. |
| F6 | both | Every request gets exactly one response with its id; notifications get none. |
| F7 | both | A request for an unknown method, or a method sent in the wrong direction, gets -32601. |
| F8 | both | A request whose params fail the schema gets -32602. |
| H1 | runner | The first line the runner sends is `hello`, within 10 seconds of connecting. |
| H2 | runner | `hello.protocolVersions` lists versions highest first, without repeats. |
| H3 | daemon | The daemon sends nothing before `hello`, and its first line is the response to `hello`. |
| H4 | daemon | The welcome's `protocolVersion` is the highest version in both the runner's list and the daemon's. |
| H5 | daemon | With no common version the daemon answers -32002 with `data.supported`, then closes the socket. |
| H6 | daemon | A second `hello`, or any other message before the welcome, gets -32000. |
| H7 | runner | Right after the welcome the runner sends `status` with state `idle`. |
| H8 | daemon | The daemon sends no request other than the `hello` response before that first `status`. |
| T1 | runner | `wake` is answered with an empty result before any other message about that turn. |
| T2 | runner | After acknowledging `wake`, the runner's next message about that turn is `status` `working` with its `turnId`. |
| T3 | runner | Every turn has at least one `profile.update` with its `turnId`, sent before the `status` that ends the turn. |
| T4 | runner | Every turn ends with `status` `idle` (with `turnId`, or with `pausedTurnId` when paused for budget), or `error`. |
| T5 | runner | `output` chunks of one turn are numbered 0, 1, 2 and so on, and the last one has `final: true`; nothing follows it in that turn. |
| T6 | runner | Every `output`, `usage`, `profile.update`, `bus.send` and `task.done` carries the `turnId` of the turn that is running. |
| T7 | runner | One turn at a time: a `wake` during a turn is acknowledged at once and its `status` `working` comes only after the current turn ended. |
| T8 | runner | `deliver` is answered with an empty result; a `deliver` for a turn that is not running gets 1001. |
| C1 | runner | `cancel` is answered with an empty result, also for a turn that already ended. |
| C2 | runner | After `cancel` for the running turn, the runner sends `status` `idle` for that turn within 5 seconds. |
| S1 | runner | `sleep` is answered with a `summary` of at most `maxSummaryChars` characters, followed by `status` `sleeping`. |
| S2 | runner | When a welcome carries `session.summary`, the runner starts its session from it (the echo runner repeats it in its first `output` of the first turn). |
| X1 | runner | `shutdown` is answered with an empty result, after which the runner closes the socket and exits 0 within `graceMs`. |
| X2 | runner | When the socket closes without `shutdown`, the runner exits with a non-zero status within 5 seconds. |
| B1 | runner | The runner sends `heartbeat` at least once per `heartbeatIntervalMs`, also during turns. |
| B2 | daemon | A runner silent for `heartbeatTimeoutMs` is stopped and restarted, and error -32003 is recorded. |
| R1 | runner | On a budget refusal the runner sends `error` 1010 with `fatal: false`, then `status` `idle` with `pausedTurnId`. |
| R2 | runner | A `wake` with reason `event`, type `agentsone.agents.budget.restored` and the paused `turnId` resumes that turn: same `turnId`, no new `profile.update` required, and it ends as in T4. |
| R3 | runner | A runner with capability `resume: false` answers that `wake` with error 1011. |
| P1 | daemon | `profile.update.line` is stored as the agent's one-line in the live list; a turn without one is recorded as a fault. |

## Example session

The echo runner, playing the founder agent, in protocol 1.0. `R>` is a line from the runner, `D>` a line
from the daemon. The same lines, without the prefixes, are in
[`../../fixtures/schemas/runner-message/transcript.ndjson`](../../fixtures/schemas/runner-message/transcript.ndjson).

Handshake, then a chat turn in which a second chat message arrives, a bus message fails and is retried:

```text
R> {"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocolVersions":["1.0"],"runner":{"name":"echo","version":"1.0.0"},"capabilities":{"streaming":true,"cancel":true,"compaction":true,"resume":true}}}
D> {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"1.0","agent":{"name":"founder","kind":"always-on","isRoot":true,"uid":0,"team":"founder","manager":null,"oneLine":"Goal: keep this machine useful to its humans. Now: starting up.","charter":"I found and grow the society on this machine.","goals":["Keep the machine healthy","Hire agents for the work humans ask for"]},"mcp":{"transport":"streamable-http","url":"http://127.0.0.1:7700/mcp","token":"test-mcp-token-founder-0001","tokenExpiresAt":"2026-11-20T19:00:00Z"},"model":{"baseUrl":"http://192.168.127.254:7710/v1","name":"autoagents/one","token":"test-gateway-token-founder-0001","weights":["quick","normal","deep"],"defaultWeight":"normal"},"session":{"summary":null,"compactedAt":null,"pausedTurnId":null},"config":{"heartbeatIntervalMs":15000,"heartbeatTimeoutMs":45000,"maxLineBytes":1048576,"runner":{}}}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}
R> {"jsonrpc":"2.0","method":"heartbeat","params":{"uptimeMs":15002}}
D> {"jsonrpc":"2.0","id":1,"method":"wake","params":{"reason":"chat","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","payload":{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"human:alice","text":"Hello founder","sentAt":"2026-11-19T19:00:01Z"}}}
R> {"jsonrpc":"2.0","id":1,"result":{}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G"}}
R> {"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","line":"Goal: keep this machine useful to its humans. Now: answering alice in chat."}}
R> {"jsonrpc":"2.0","method":"output","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","chunk":0,"text":"Hello ","final":false}}
D> {"jsonrpc":"2.0","id":2,"method":"deliver","params":{"kind":"chat","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","message":{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F42","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"human:alice","text":"Are you there?","sentAt":"2026-11-19T19:00:02Z"}}}
R> {"jsonrpc":"2.0","id":2,"result":{}}
R> {"jsonrpc":"2.0","method":"usage","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","model":"echo","weight":"quick","tokensIn":12,"tokensOut":4,"costMicroUsd":0}}
R> {"jsonrpc":"2.0","method":"output","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","chunk":1,"text":"founder\n\nAre you there?","final":true}}
R> {"jsonrpc":"2.0","id":2,"method":"bus.send","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","to":"ghost","subject":"alice said hello","body":"alice opened a chat with me."}}
D> {"jsonrpc":"2.0","id":2,"error":{"code":1002,"message":"Unknown recipient","data":{"to":"ghost"}}}
R> {"jsonrpc":"2.0","id":3,"method":"bus.send","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","to":"watcher","subject":"alice said hello","body":"alice opened a chat with me.","correlationId":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41"}}
D> {"jsonrpc":"2.0","id":3,"result":{"messageId":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F43"}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"idle","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G"}}
```

A task turn that runs out of budget, pauses, and resumes when budget returns:

```text
D> {"jsonrpc":"2.0","id":3,"method":"wake","params":{"reason":"task","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","payload":{"id":"tsk_01JD8X3Q5Y6Z7A8B9C0D1E2F50","title":"Write the weekly status note","body":"Summarise what the society did this week.","from":"human:alice","createdAt":"2026-11-19T19:01:00Z"}}}
R> {"jsonrpc":"2.0","id":3,"result":{}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H"}}
R> {"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","line":"Goal: keep this machine useful to its humans. Now: writing the weekly status note for alice."}}
R> {"jsonrpc":"2.0","method":"log","params":{"level":"info","message":"Calling the model gateway","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","fields":{"weight":"normal"}}}
R> {"jsonrpc":"2.0","method":"error","params":{"code":1010,"message":"Budget exhausted","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","fatal":false}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"idle","pausedTurnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","detail":"Waiting for budget"}}
D> {"jsonrpc":"2.0","id":4,"method":"wake","params":{"reason":"event","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","payload":{"specversion":"1.0","id":"evt_01JD8X3Q5Y6Z7A8B9C0D1E2F60","source":"/emitters/agents","type":"agentsone.agents.budget.restored","subject":"founder","time":"2026-11-19T19:05:00Z","datacontenttype":"application/json","data":{"agent":"founder","pausedTurnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H"},"sequence":"00000000000000000042","actor":"host:agentsone","chartscope":"agent:founder"}}}
R> {"jsonrpc":"2.0","id":4,"result":{}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H"}}
R> {"jsonrpc":"2.0","method":"usage","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","model":"echo","weight":"normal","tokensIn":40,"tokensOut":18,"cacheReadTokens":0,"costMicroUsd":0}}
R> {"jsonrpc":"2.0","id":4,"method":"task.done","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","taskId":"tsk_01JD8X3Q5Y6Z7A8B9C0D1E2F50","outcome":"done","summary":"Weekly status note written to common memory","details":"Saved as memory mem_01JD8X3Q5Y6Z7A8B9C0D1E2F80."}}
D> {"jsonrpc":"2.0","id":4,"result":{}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"idle","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H"}}
```

A timer turn that a human cancels, a bus message turn, then sleep and shutdown:

```text
D> {"jsonrpc":"2.0","id":5,"method":"wake","params":{"reason":"timer","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3J","payload":{"scheduleId":"sch_01JD8X3Q5Y6Z7A8B9C0D1E2F70","name":"nightly-review","due":"2026-11-19T19:10:00Z","firedAt":"2026-11-19T19:10:00.012Z","note":"Review the day"}}}
R> {"jsonrpc":"2.0","id":5,"result":{}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3J"}}
R> {"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3J","line":"Goal: keep this machine useful to its humans. Now: reviewing the day."}}
D> {"jsonrpc":"2.0","id":6,"method":"cancel","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3J","reason":"human"}}
R> {"jsonrpc":"2.0","id":6,"result":{}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"idle","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3J","detail":"Cancelled"}}
D> {"jsonrpc":"2.0","id":7,"method":"wake","params":{"reason":"message","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3K","payload":{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F44","from":"agent:watcher","to":"founder","subject":"Re: alice said hello","body":"Noted.","sentAt":"2026-11-19T19:11:00Z","replyTo":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F43","correlationId":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41"}}}
R> {"jsonrpc":"2.0","id":7,"result":{}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"working","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3K"}}
R> {"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3K","line":"Goal: keep this machine useful to its humans. Now: reading a reply from watcher."}}
R> {"jsonrpc":"2.0","method":"output","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3K","chunk":0,"text":"Noted.","final":true}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"idle","turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3K"}}
D> {"jsonrpc":"2.0","id":8,"method":"sleep","params":{"reason":"idle","maxSummaryChars":4096}}
R> {"jsonrpc":"2.0","id":8,"result":{"summary":"Chatted with alice. Wrote the weekly status note (task done). watcher acknowledged."}}
R> {"jsonrpc":"2.0","method":"status","params":{"state":"sleeping"}}
D> {"jsonrpc":"2.0","id":9,"method":"shutdown","params":{"reason":"stop","graceMs":5000}}
R> {"jsonrpc":"2.0","id":9,"result":{}}
```

The tokens shown are test values. Values that depend on time (`uptimeMs`, timestamps) and the gateway
address may differ in a real run; everything else is fixed.
