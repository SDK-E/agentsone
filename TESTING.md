# Testing standard

This page says what we test and how. We build with clean code, domain-driven design (domain, app,
adapters) and test-driven development. Tests are how we prove the rules of the system work, so they
are held to the same bar as the code.

How to read it:

- Every rule has an ID (A1, B3, D2 and so on). A review comment can cite the ID.
- **Must** and **must not** block a merge. **Should** is the default; deviate only with a reason in
  the pull request.
- Every rule can be checked by reading the diff or running one command. The checklist in section F
  is the short version.
- Examples are in Go and TypeScript. Type and function names in the examples are illustrative. The
  real fakes and builders are in `internal/core/coretest` and in the `<pkg>test` packages next to
  each context. Read them before you write new ones.

| Layer | Kind of test | Main tools | Where it runs |
| --- | --- | --- | --- |
| Domain | Pure unit, table-driven | `testing`, `go-cmp` | every `go test` |
| App (use cases) | Behaviour through ports with in-memory fakes | `testing`, `coretest` fakes | every `go test` |
| Adapters | Real SQLite, `httptest`, shared contract suites | `testing`, `httptest` | every `go test` |
| Contract | Fixtures, drift, runner conformance | schema validators, `make check-gen` | every `go test`, CI |
| Integration | Daemon in-process, API over a Unix socket | `testing`, `httptest` | every `go test` |
| System | Linux users, cgroups, systemd, nftables, PAM | `testing` with build tags | Linux CI or the booted VM |
| UI component and page | User-visible behaviour, accessibility | Vitest, Testing Library, axe | `pnpm test` |
| UI pages in a browser | Every page, every state, theme and width | Playwright, axe | CI, with screenshots |
| End to end | Boot the image and run the verification script | QEMU, `e2e/` | CI on `main` |

## A. Process

**A1. Test first, always.** Red, green, refactor, in that order, for every behaviour.

1. Red: write one failing test. Run it. It must fail because the behaviour is missing, with a
   message that names the missing behaviour. A compile error or a setup error is not red.
2. Green: write the least code that makes it pass.
3. Refactor: clean up with the tests green. Add no behaviour in this step.

Code written before its test is deleted and redone from the test.

**A2. Every bug fix starts with a failing regression test.** The test goes in the same pull
request as the fix. The pull request description names the test and quotes the failure it showed
before the fix. The reviewer can prove it by running the test against the code on `main`.

```go
// Red first: an agent could retire its own manager.
func TestRetire_RejectsRetiringAnAncestor(t *testing.T) {
	t.Parallel()
	boss := agenttest.New(agenttest.Root())
	worker := agenttest.New(agenttest.Below(boss))

	err := domain.Retire(worker, boss)

	if !errors.Is(err, domain.ErrNotBelowActor) {
		t.Fatalf("Retire(worker, boss) error = %v, want %v", err, domain.ErrNotBelowActor)
	}
}
```

**A3. One behaviour per test.** A name with "and" in it is two tests. A test may make several
assertions, but all of them describe one outcome. In a table-driven test every row is one
behaviour.

**A4. Names say the behaviour.** The name is a sentence about what the code does, not about how
the test is built.

| Language | Good | Bad |
| --- | --- | --- |
| Go | `TestHire_RejectsHireAboveSelf` | `TestHire2`, `TestHireFails`, `TestHireHappyPath` |
| Go sub-test | `t.Run("peer is not below actor", ...)` | `t.Run("case 3", ...)` |
| TypeScript | `it("shows empty state when no agents")` | `it("works")`, `it("should render")` |

In Go the pattern is `Test<Subject>_<Behaviour>`. In TypeScript use `describe("<Subject>")` and
`it("<behaviour in plain words>")`.

**A5. Tests are deterministic.** A test gives the same result on every machine, in every order,
at every hour.

- Time: code under test takes the `core.Clock` port. Tests use the fake clock and move it by hand.
  No `time.Now()`, `Date.now()` or real timers in the code under test or in the test.
- Identifiers and randomness: code takes ID entropy as an input (`core.NewIDs(clock, entropy)`).
  Tests use `coretest.NewIDs()`. Any other random source gets a fixed seed.
- No sleeping. Wait on a condition, a channel or an event. A timeout may guard against a hang and
  fail the test. It is never the thing the test waits for.
- No real network. Use `httptest` or a local listener on `127.0.0.1:0` or a Unix socket.
- Files go in `t.TempDir()` (Go) or a temp directory made in the test (TypeScript). A test never
  writes into the repository or the home directory.
- Do not depend on map order, time zone or locale. Build times with `time.UTC`. Sort before you
  compare.

```go
func TestWakeLimiter_AllowsWakeAgainAfterWindow(t *testing.T) {
	t.Parallel()
	clock := coretest.NewClock(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC))
	limiter := domain.NewWakeLimiter(clock, 2, time.Minute)
	limiter.Allow("agt_a")
	limiter.Allow("agt_a")
	if limiter.Allow("agt_a") {
		t.Fatal("third wake inside the window was allowed")
	}

	clock.Advance(time.Minute)

	if !limiter.Allow("agt_a") {
		t.Fatal("wake after the window was refused")
	}
}
```

```go
// Waiting for an event: wait on the channel; the timeout only turns a hang into a failure.
select {
case got := <-sub.C:
	if got.Type != "agent.hired" {
		t.Fatalf("event type = %q, want agent.hired", got.Type)
	}
case <-time.After(5 * time.Second):
	t.Fatal("no event within 5s")
}
```

```tsx
// Time in the interface: fake timers, never a real wait.
afterEach(() => vi.useRealTimers());

it("hides the toast after five seconds", async () => {
  vi.useFakeTimers();
  render(<Toast message="Saved" />);

  await vi.advanceTimersByTimeAsync(5_000);

  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});
```

**A6. A flaky test is a failing test.** Fix it the day it appears, or delete it. Do not retry it,
skip it or mark it as allowed to fail. Playwright runs with `retries: 0`.

**A7. Concurrency code is proven under the race detector.** Before you open a pull request that
touches goroutines, channels, locks or shared state, run the package with `-race -count=3` and
`-shuffle=on`, and write the command in the pull request. CI runs `go test -race ./...`.
Every goroutine a test starts must end before the test ends. Use `t.Context()` and `t.Cleanup`.

**A8. Tests are independent.** A test builds its own state, shares no mutable global, and passes
alone, in any order, and in parallel with others. Use `t.Parallel()` wherever it is safe. It is not
safe with `t.Setenv`, a fixed port, a shared file or a package-level variable.

## B. What to test, by layer

### B1. Domain

The domain holds the rules of the system. Every invariant and every rule has a test. Domain tests
are pure unit tests: no database, no network, no files, no clock except the fake one. A domain test
must not import an adapter.

- **B1a.** Rules with several cases are table-driven. Each row is one behaviour (A3).
- **B1b.** Each rule must be backed by a test that fails when the rule is removed. Check it by
  deleting the rule and watching the test go red.
- **B1c.** Test the edges: the value just inside the limit, on it, and just outside.

These cases must exist. More are welcome.

| Rule | Cases that must exist |
| --- | --- |
| Only root agents create root agents | root creates root: allowed. Non-root creates root: refused. Root creates non-root: allowed |
| Hire, edit and retire only below yourself | For each of the three: child and grandchild allowed. Self, peer, ancestor and an unrelated branch refused |
| One-line is at most 255 characters | Lengths 254, 255 and 256. A multi-byte character across the limit. The contract says whether the limit counts characters or bytes; the test pins it |
| Memory scope access | Reader (the owner, a team member, someone outside the chart branch, root) by scope (private, team = the chart branch, common). Root reads all. Every cell of the table is a case |
| Budget at zero refuses model calls | Budget above zero: allowed. Exactly zero: refused. Below zero: refused. Refusal records no model call |
| Trigger wake-rate limit | At the limit: allowed. One over: refused. Allowed again after the window, using the fake clock. The window edge |
| Audit chain hash links | Each record's hash covers the one before it. An untouched chain verifies. Changing, deleting, reordering or inserting any record fails verification and names the first bad record. An empty chain is valid |

```go
func TestHire_RejectsHireAboveSelf(t *testing.T) {
	t.Parallel()
	boss := agenttest.New(agenttest.Root())
	worker := agenttest.New(agenttest.Below(boss))
	stranger := agenttest.New(agenttest.Root())

	tests := []struct {
		name    string
		actor   domain.Agent
		target  domain.Agent
		wantErr error
	}{
		{"actor hires below itself", boss, worker, nil},
		{"actor hires itself", worker, worker, domain.ErrNotBelowActor},
		{"actor hires its manager", worker, boss, domain.ErrNotBelowActor},
		{"actor hires in an unrelated branch", boss, stranger, domain.ErrNotBelowActor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := domain.CanHire(tt.actor, tt.target)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CanHire() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
```

### B2. App (use cases)

A use case is tested through its ports, with the in-memory fakes from `coretest` (for example
`coretest.NewPorts()`). The test asserts what the outside world can see:

1. the returned result, or the kind of error,
2. the state read back through a port,
3. the events that were published,
4. the audit records that were written.

**B2a.** An app test must not assert on internal calls. "Save was called once" is not a behaviour.
If a refactor keeps the outcomes and breaks the test, the test was wrong.

**B2b.** Every use case has at least: the success path, one test for each domain refusal as the
caller sees it, the acting identity (who is allowed), and, for a refusal, proof that nothing was
saved, published or audited.

```go
func TestHireAgent_PublishesEventAndWritesAuditRecord(t *testing.T) {
	t.Parallel()
	ports := coretest.NewPorts()
	founder := agenttest.New(agenttest.Root())
	ports.Agents.Seed(founder)
	hire := app.NewHireAgent(ports)

	got, err := hire.Do(t.Context(), app.HireInput{
		Actor:   founder.ID,
		Name:    "mira",
		OneLine: "Reviews pull requests.",
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}

	if diff := cmp.Diff([]string{"agent.hired"}, ports.Events.Types()); diff != "" {
		t.Errorf("published events mismatch (-want +got):\n%s", diff)
	}
	if rec := ports.Audit.Last(); rec.Action != "agent.hire" || rec.Subject != got.ID {
		t.Errorf("audit record = %+v, want agent.hire for %s", rec, got.ID)
	}
}
```

### B3. Adapters

Adapters connect the system to the outside world. Test them against the real thing where the real
thing runs anywhere.

- **B3a. SQLite repositories** run against real SQLite in a temp directory (the pure-Go driver).
  Never mock the database.
- **B3b. One shared repository contract suite** per port, in `internal/<ctx>/<ctx>test`, runs against
  **both** the real repository and the in-memory fake. This is what keeps the fakes honest. A
  new repository behaviour goes into the suite first.
- **B3c. Migrations:** a test opens a database at every released schema version, migrates up, and
  checks that the data survived.
- **B3d. HTTP adapters** use `httptest` and the real handlers. Assert the status code, the
  `application/problem+json` body (RFC 9457: `type`, `title`, `status` equal to the HTTP status,
  and a `detail` that leaks no internals) and the headers.
- **B3e. Every response, success or error, is validated against the OpenAPI contract** with
  `contracttest.CheckResponse`. A handler test that skips it is incomplete.

```go
func TestSQLiteRepository(t *testing.T) {
	audittest.RunRepositoryContract(t, func(t *testing.T) domain.Repository {
		repo, err := sqlite.Open(filepath.Join(t.TempDir(), "audit.db"))
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		return repo
	})
}

func TestMemoryRepository(t *testing.T) {
	audittest.RunRepositoryContract(t, func(*testing.T) domain.Repository {
		return memory.New()
	})
}
```

```go
// In audittest: the suite both repositories must pass.
func RunRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	t.Helper()
	t.Run("returns not found for an unknown id", func(t *testing.T) {
		t.Parallel()
		repo := newRepo(t)

		_, err := repo.Get(t.Context(), "aud_missing")

		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get() error = %v, want %v", err, domain.ErrNotFound)
		}
	})
}
```

```go
func TestPostAgents_ReturnsProblemWhenOneLineIsTooLong(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t) // real handlers, fake ports
	body := `{"name":"mira","one_line":"` + strings.Repeat("x", 256) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity { // the status the contract names
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	contracttest.CheckResponse(t, req, rec)
}
```

### B4. Contract

- **B4a.** Every fixture under `contract/fixtures` validates against its schema, and every fixture
  is claimed by a schema. An unclaimed fixture fails the test.
- **B4b.** Drift check: generated Go and TypeScript match the contract. `make check-gen` fails the
  build when they do not.
- **B4c.** Runner protocol conformance: one suite drives a runner through the JSON-RPC protocol
  (welcome, wake, deliver, cancel, sleep, shutdown and the messages back). The echo runner passes it,
  and **every runner** (sdk, openai, claude) runs the same suite in its own tests.
- **B4d.** A contract change is a version bump and a changed fixture in the same pull request.

### B5. Integration

The daemon runs in process with real stores. The API is called over its Unix socket. Integration
tests need no root and no Linux features, so they run with every `go test` and carry no build tag.

- **B5a.** Cover each caller identity path: a root agent, a custom agent, a human session, and an
  unknown caller. Each gets the right access, or the right 401 or 403.
- **B5b.** The identity resolver is a port. In-process tests inject a fake one. The real
  socket peer credentials and cgroup lookup are Linux-only and belong to the system tests (B6).
- **B5c.** Put the socket in a short directory (`os.MkdirTemp("", "ao")`). The path limit for a Unix
  socket is about 100 characters and `t.TempDir()` can exceed it on macOS.

### B6. System (Linux only)

Anything that depends on the Linux kernel or its services is proven by a real test: users, cgroup
limits, systemd units, nftables rules, PAM.

- **B6a.** System tests start with both build constraints and live in a package of their own:

  ```go
  //go:build linux && integration

  package system_test
  ```

- **B6b.** They run in CI on Linux, or inside the booted VM, with
  `go test -tags integration -race ./...`.
- **B6c.** They are never replaced by a mock as proof. A fake behind a port is fine for testing
  logic. The claim "this agent cannot exceed its memory limit" is only proven by a test that tries
  it on a real cgroup.
- **B6d.** They need root. When they do not have it they fail with a clear message. They do not
  skip, because a skipped test proves nothing.
- **B6e.** They remove what they create (users, cgroups, tables) in `t.Cleanup`.

### B7. UI

- **B7a. Components:** Vitest with Testing Library. Query by role and label, the way a person finds
  things (`getByRole`, `getByLabelText`, `getByText`). Interact with `@testing-library/user-event`.
  Assert what the user sees and can do, never component state, props or CSS class names.
  `getByTestId` needs a comment that says why no role or label works.
- **B7b. Accessibility:** every component and every page has an axe check (`vitest-axe` or
  `axe-core` for components, `@axe-core/playwright` for pages) against WCAG 2.2 AA. jsdom cannot
  compute colour contrast, so contrast is only proven in the Playwright run.
- **B7c. Pages in a browser:** Playwright runs against the fake daemon, for every page, in each of
  these states: empty, loaded, error, long text. Each state runs in light and dark, at phone width
  (390 px) and desktop width (1440 px). That is 16 runs per page.
- **B7d.** The screenshots from the Playwright run are kept as CI artifacts for human review. They
  are not compared pixel by pixel.
- **B7e.** No large markup snapshots. `toMatchSnapshot()` is not used on DOM or components. A
  `toMatchInlineSnapshot()` of at most 10 lines is allowed for a small serialised value.
- **B7f.** MSW only for a case the fake daemon cannot serve, such as a dropped connection. Say why
  in a comment.

```tsx
describe("AgentList", () => {
  it("shows empty state when no agents", () => {
    render(<AgentList agents={[]} />);

    expect(screen.getByRole("heading", { name: "No agents yet" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Hire an agent" })).toBeEnabled();
  });

  it("opens the hire form from the empty state", async () => {
    const user = userEvent.setup();
    render(<AgentList agents={[]} />);

    await user.click(screen.getByRole("button", { name: "Hire an agent" }));

    expect(screen.getByRole("dialog", { name: "Hire an agent" })).toBeVisible();
  });

  it("has no accessibility violations", async () => {
    const { container } = render(<AgentList agents={agents} />);

    expect(await axe(container)).toHaveNoViolations();
  });
});
```

```ts
// playwright.config.ts: the theme and width matrix, 4 projects.
const sizes = {
  phone: { width: 390, height: 844 },
  desktop: { width: 1440, height: 900 },
};

export default defineConfig({
  retries: 0,
  projects: (["light", "dark"] as const).flatMap((colorScheme) =>
    Object.entries(sizes).map(([size, viewport]) => ({
      name: `${size}-${colorScheme}`,
      use: { viewport, colorScheme },
    })),
  ),
});
```

```ts
// One spec per page. The four states run in each of the four projects. The fake daemon
// (cmd/agentsoned-fake) serves contract/fixtures/api/<operationId>[.<variant>].json; the
// X-Fixture-Variant header picks the variant for every request of the page, and an operation
// without that variant serves its default (loaded) fixture. ?fixture=<variant> picks it for one
// request and is strict: a missing variant is a 404. X-Fixture-Variant-Served says what was served.
for (const state of ["empty", "loaded", "error", "long-text"] as const) {
  test(`agents page, ${state}`, async ({ page }, testInfo) => {
    if (state !== "loaded") {
      await page.setExtraHTTPHeaders({ "X-Fixture-Variant": state });
    }
    await page.goto("/agents");
    await expect(page.getByRole("main")).toBeVisible();

    const axe = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
      .analyze();
    expect(axe.violations).toEqual([]);

    await testInfo.attach("screenshot", {
      body: await page.screenshot({ fullPage: true }),
      contentType: "image/png",
    });
  });
}
```

### B8. End to end

CI on `main` boots the image in QEMU and runs the verification script in `e2e/`: first-boot user,
login, a bus message, an echo chat, the allow list, the budget wall, freeze and release, and the
rest of the list in the project specification. A change to behaviour that this script covers
updates the script in the same pull request. Full end-to-end runs only on `main`, not on every
pull request.

## C. What not to test

- **C1. Generated code.** Test its inputs instead: the contract and the generator, through the drift
  check (B4b).
- **C2. Third-party libraries.** Test how we use them, at the adapter that wraps them. Do not test
  that SQLite stores rows or that React renders.
- **C3. Trivial accessors.** A getter, a setter, a struct that only holds fields, a one-line
  delegation with no branch.
- **C4. Log text.** Logs are not an interface. If a test needs to see that something happened, it
  looks at an event or an audit record, which are interfaces.
- **C5. Private functions directly.** Test them through the exported behaviour. If a private
  function is complex enough to need its own tests, it is a missing type or package: extract it,
  then test it as public.
- **C6. The fakes' own call counts, and the mocks themselves.** A test that only proves a mock
  returned what it was told proves nothing.

## D. How: tools and conventions

### D1. Go

- **D1a.** Standard library `testing`. Table-driven tests with `t.Run`. `t.Parallel()` where safe
  (A8). `t.Helper()` in helpers. `t.Cleanup` for teardown.
- **D1b.** Compare values with `go-cmp` (`cmp.Diff(want, got)`, and `cmpopts` for the odd field). The
  failure message follows the form `got ..., want ...` or prints the diff with `(-want +got)`.
- **D1c.** Tests live in the external package `<pkg>_test`. They can only reach what a caller
  can reach, which enforces C5. An internal test file (`package <pkg>`) needs a comment that says why.
- **D1d.** Golden files live in `testdata/` next to the test. They are rewritten only with the
  `-update` flag. A golden diff is read in review like code. Golden files are for output formats
  (API JSON, rendered unit files, generated configuration), not for large rendered markup.

  ```go
  var update = flag.Bool("update", false, "rewrite golden files")

  func TestRenderUnit_MatchesGolden(t *testing.T) {
  	got := unit.Render(agenttest.New(agenttest.Root()))
  	golden := filepath.Join("testdata", "agent-unit.golden")
  	if *update {
  		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
  			t.Fatal(err)
  		}
  	}
  	want, err := os.ReadFile(golden)
  	if err != nil {
  		t.Fatal(err)
  	}
  	if diff := cmp.Diff(string(want), got); diff != "" {
  		t.Errorf("unit mismatch (-want +got):\n%s", diff)
  	}
  }
  ```

- **D1e.** No mocking frameworks and no assertion libraries: not `gomock`, `mockery`,
  `counterfeiter`, and not `testify`. Write the fake by hand (D2).
- **D1f.** For code that starts goroutines and waits on timers, `testing/synctest` may be used
  to run it on virtual time. Time that comes in through `core.Clock` still uses the fake clock.
- **D1g.** Tests are quiet. A passing test prints nothing. Use the discard logger
  (`coretest.NewDeps` already does).

### D2. Fakes

- **D2a.** A fake is a hand-written, in-memory implementation of a port. It lives in
  `internal/core/coretest` when several contexts share it, and in `<pkg>test` when one context owns
  it.
- **D2b.** A fake behaves like the real thing, including its errors: not found, conflict, closed.
  Its behaviour is held in place by the shared contract suite (B3b).
- **D2c.** A fake may offer ways to read outcomes (`Events.Types()`, `Audit.Last()`) and to inject a
  failure (`FailNext(err)`). It does not count calls to judge the code under test (B2a).

### D3. Test data builders

Build test data with a builder that takes options and starts from a valid default. A test names
only the fields that matter to its case.

```go
agent := agenttest.New()                                       // a valid agent
boss := agenttest.New(agenttest.Root())                        // a root agent
worker := agenttest.New(agenttest.Below(boss), agenttest.WithOneLine(strings.Repeat("x", 256)))
```

Builders take their IDs from `coretest.NewIDs()`, so the values are the same on every run.

### D4. TypeScript

- **D4a.** Vitest, `@testing-library/react`, `@testing-library/user-event`, `vitest-axe` or
  `axe-core`, Playwright with `@axe-core/playwright`. MSW only as B7f says.
- **D4b.** A component test sits next to its component as `<name>.test.tsx`.
- **D4c.** Wait with `findBy*`, `waitFor` or `expect.poll`. Never `setTimeout` or
  `page.waitForTimeout`. Playwright's web-first assertions already wait.
- **D4d.** Use `userEvent.setup()` and `await` every call.

### D5. Commands

Run heavy commands through `scripts/heavy`, as [CONTRIBUTING.md](CONTRIBUTING.md) says.

| Goal | Command |
| --- | --- |
| All tests, as CI runs them | `make test` |
| One package, concurrency check (A7) | `scripts/heavy go test -race -count=3 -shuffle=on ./internal/bus/...` |
| Rewrite golden files, one package at a time | `go test ./internal/api/ -update` |
| System tests, on Linux | `go test -tags integration -race ./...` |
| Coverage gate for Go | see section E |
| Web tests | `pnpm test` |

## E. Coverage gate

Coverage is a floor. It catches the package nobody tested. It does not tell you the tests are good.

**E1. The floors** (statement coverage):

| Code | Floor | Path pattern |
| --- | --- | --- |
| Domain packages | 90% | `*/domain/*` |
| App packages | 90% | `*/app/*` |
| Adapters | 70% | `*/adapters/*` |
| UI components | 80% | the `ui/` component folders |

**E2. An untested rule is a review blocker at any coverage number.** 100% with no assertion proves
nothing. A rule from section B with no test is blocked even when the package is above its floor.

**E3. Go is measured per package, from that package's own tests.** Do not use `-coverpkg`. A
domain package must earn its number without help from the app tests. A package with no tests
shows up at 0% and fails.

```sh
mkdir -p coverage
go test -race -coverprofile=coverage/cover.out ./...
scripts/check-coverage.sh coverage/cover.out
```

[scripts/check-coverage.sh](scripts/check-coverage.sh) prints one row per package and exits 1 when
a package is below its floor, 2 for bad input. Its own test is `scripts/test-check-coverage.sh`.
Packages that match no layer are shown as `unchecked` and are not gated.

**E4. Excluded from the gate:** `cmd/*`, generated code (`gen` and `generated` folders, `*.gen.go`,
`*_gen.go`, `*.pb.go`, `zz_generated*.go`, `internal/contract/*`), `testdata`, and test-support
packages (any folder whose name ends in `test`, such as `coretest`). A new exclusion needs a
reason in the pull request and a change to the script and its test.
`v8 ignore` and `istanbul ignore` comments in TypeScript need a reason on the same line.

**E5. UI components** use the Vitest coverage thresholds, so `pnpm test` fails below the floor.
The UI workspace's `test` script runs `vitest run --coverage`.

```ts
// vitest.config.ts
test: {
  coverage: {
    provider: "v8",
    exclude: ["**/*.test.*", "**/generated/**"],
    thresholds: { "src/components/**": { statements: 80 } },
  },
},
```

**E6. Floors go up, not down.** Lowering a floor needs the project owner's approval, in the pull request
that changes this page and the script together.

**E7. CI wiring.** The gate belongs in the pull request workflow after `go test`, using the three
lines in E3. Until it is wired there, run it locally before you open a pull request.

## F. Review checklist

Tick every line, or say in the pull request why it does not apply.

- [ ] The test came first. A bug fix names its regression test and the failure it showed (A1, A2).
- [ ] One behaviour per test, and the names read as behaviour (A3, A4).
- [ ] Every new rule or branch has a test that fails when the rule is deleted (B1b).
- [ ] No `time.Sleep`, `waitForTimeout` or timer waits. No `time.Now()` or `Date.now()` in the
  code under test. No real network. No unseeded random. Files only in temp directories (A5).
- [ ] No mocking framework and no `testify`. Fakes pass the shared contract suite (D1e, B3b).
- [ ] App tests assert results, events and audit records, not calls (B2a).
- [ ] Adapters: real SQLite in a temp directory. Handler tests check status, problem body and
  `contracttest.CheckResponse` on success and error paths (B3).
- [ ] Linux-only behaviour has a system test behind the build tags, not a mock (B6).
- [ ] UI: queries by role or label, axe check present, page runs cover states, themes and widths,
  screenshots kept, no large snapshots (B7).
- [ ] Nothing from section C is tested.
- [ ] Concurrency code: the `-race -count=3 -shuffle=on` run is in the pull request (A7).
- [ ] `scripts/check-coverage.sh` passes and no exclusion or ignore comment was added without a
  reason (E).
- [ ] The test output is clean: no warnings, no log noise, no skipped tests (A6).
