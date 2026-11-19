<!-- SPDX-License-Identifier: MIT -->

# End-to-end coverage

The project plan ends with a Verification list: what must be proven before Agents One is done.
This page maps each item on that list to the end-to-end stages in [`e2e/run.sh`](../e2e/run.sh)
that check it, and lists what CI cannot check.

## The live state

Run `e2e/run.sh list`. It needs no stack and prints two tables:

- **Stages, in run order.** Each stage says what it checks and is either `real` (a check that
  can fail) or `not yet`, with the reason. A stage that is `not yet` reports SKIP with that
  reason when it runs. It never reports PASS.
- **The plan's Verification list.** Each item is `covered` (every stage that checks it is real),
  `not yet` (it names the stages still missing), or `manual` (CI cannot check it).

The pull request checks run `e2e/run.sh list`, and it fails when a stage has no check and no
reason, has both, is not run by the e2e job in
[`.github/workflows/images.yml`](../.github/workflows/images.yml), or when an item names a stage
that does not exist. So the list cannot quietly drift from the script or from CI.

To make a stage real, delete its line in `PENDING` in `e2e/run.sh` and write its `stage_<name>`
function in the same pull request.

## Checked by CI on `main`

CI on `main` builds both images for amd64 and arm64, pushes them to ghcr.io, starts the stack on
an Ubuntu runner with `e2e/run.sh up`, and then runs every stage.

| Verification item                | Stages                         | What the stages check                                                                 |
| -------------------------------- | ------------------------------ | ------------------------------------------------------------------------------------- |
| Setup page                       | `setup-page`                   | The first human is made on the setup page, only through the web door.                |
| Login                            | `login`                        | The first human signs in and out through the web door.                               |
| Small Seed                       | `small-seed`                   | On first boot the Small Seed installs and the founder, router and waker start.       |
| Founder hires                    | `hire`                         | The founder hires an agent below itself.                                              |
| Bus message                      | `bus`                          | A message sent on the bus reaches its channel.                                        |
| Live list and router pick        | `router`                       | The live list and the router pick an agent for a human's request.                     |
| Echo chat                        | `echo-chat`                    | A human chats with an echo agent.                                                     |
| Event to task                    | `event-to-task`                | An event fires a trigger that makes a task.                                           |
| Memory                           | `memory`                       | Memory is written and found again.                                                    |
| Plugin install                   | `plugin-install`               | A plugin installs and its parts appear.                                               |
| `agentctl` parity                | `agentctl-parity`              | `agentctl` answers as the API does.                                                   |
| Allow list blocks and passes     | `wall-blocked`, `wall-allowed` | A name that is not on the allow list is blocked; a name that is on it passes.         |
| Budget wall and defence slice    | `budget-wall`, `defence-slice` | Past the total budget no model call reaches a provider; the defence slice still pays. |
| Freeze all and release           | `freeze-all`                   | Freeze all cuts model calls and egress, holds over an edge restart, and releases.     |

More stages check the walls the plan relies on, beyond the list: `wall-route`, `wall-direct` and
`wall-outer` (the network wall), `gateway` and `model-turn` (model calls through the gateway,
ending at a fake provider), `audit-sink` (audit records from the daemon only), `rescue` (a
restart keeps the machine, a recreate resets it), `web-door` (a header is never an identity) and
`control` (the control page and its passphrase).

## Not covered by CI: manual

These items need a person, a real server or a real model key. CI never has any of them.

- **The Mac walk-through.** On a Mac with colima, run `docker compose up` and walk every screen,
  the Desktop panel included: light and dark, phone and desktop width, and each screen empty,
  loaded, in error and with long text. Install an IDE extension. Prove that a root agent's change
  to the system survives a restart through the machine recipe, that a custom agent is held to its
  limits, and that the control page works. The interface page tests cover each page against the
  fake daemon in pull requests, and the `control` stage covers the control page's API, but
  neither replaces a person looking at the real stack.
- **The Coolify deploy.** Deploy from the public repository as a new app, as
  [the Coolify guide](deploy/coolify.md) says, and reach it through the platform's own front
  door. TLS and any sign-in in front are set up on the platform side, not in this repository.
- **The real-model run.** The owner enters a model key in the control page and an agent takes a
  real turn. The `model-turn` stage proves the same path in CI with a fake provider and a key
  made for each run.

The last item on the plan's list, the final report, is a report to the owner, not a check.
