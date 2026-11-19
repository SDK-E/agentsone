// SPDX-License-Identifier: MIT

package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/sdk"
)

func TestHandshakeUnsupportedVersion(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{})
	g.line()
	g.send(`{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"Unsupported version","data":{"supported":["2.0","1.5"]}}}`)
	err := g.wait()
	var uv *sdk.UnsupportedVersionError
	if !errors.As(err, &uv) || len(uv.Supported) != 2 || uv.Supported[0] != "2.0" {
		t.Fatalf("err = %v, want UnsupportedVersionError listing 2.0 and 1.5", err)
	}
	if !strings.Contains(err.Error(), "2.0") {
		t.Fatalf("message %q does not name the versions the daemon speaks", err)
	}
}

func TestHandshakeFaults(t *testing.T) {
	cases := map[string]struct {
		reply string
		want  string
	}{
		"other error":      {`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"boom"}}`, "hello"},
		"welcome not json": {`{"jsonrpc":"2.0","id":1,"result":[1]}`, "welcome"},
		"version not offered": {`{"jsonrpc":"2.0","id":1,"result":` +
			strings.Replace(welcomeJSON, `"protocolVersion":"1.0"`, `"protocolVersion":"9.9"`, 1) + `}`, "9.9"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRig(t, echoOpts(), funcHandler{})
			g.line()
			g.send(c.reply)
			if err := g.wait(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestVersionMismatchIsErrUnsupportedVersion(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{})
	g.line()
	g.send(`{"jsonrpc":"2.0","id":1,"result":` + strings.Replace(welcomeJSON, `"protocolVersion":"1.0"`, `"protocolVersion":"9.9"`, 1) + `}`)
	if err := g.wait(); !errors.Is(err, sdk.ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestMessageBeforeWelcomeIsOutOfOrder(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{})
	g.line() // hello
	g.wakeChat(turnA)
	if l := g.line(); !strings.Contains(l, `"id":1,"error":{"code":-32000`) {
		t.Fatalf("want -32000 for a wake before the welcome, got %s", l)
	}
}

func TestStartFailureIsFatal(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{start: func(context.Context, sdk.Welcome) error { return errors.New("no disk") }})
	g.line()
	g.send(`{"jsonrpc":"2.0","id":1,"result":` + welcomeJSON + `}`)
	got := joined(g.until(`"state":"error"`))
	if !strings.Contains(got, `"fatal":true`) {
		t.Fatalf("want a fatal error before status error, got %s", got)
	}
	if err := g.wait(); err == nil || !strings.Contains(err.Error(), "no disk") {
		t.Fatalf("err = %v, want the start failure", err)
	}
}

func TestDaemonClosingTheSocketEndsRun(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{})
	g.ready()
	_ = g.d.Close()
	if err := g.wait(); !errors.Is(err, sdk.ErrDaemonClosed) {
		t.Fatalf("err = %v, want ErrDaemonClosed", err)
	}
}

func TestMalformedLinesGetProtocolErrors(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{})
	g.ready()
	cases := []struct{ send, want string }{
		{`{"jsonrpc":"1.0","id":5,"method":"wake"}`, `"id":5,"error":{"code":-32600`},
		{`{"jsonrpc":"2.0","id":"x","method":"wake"}`, `"id":null,"error":{"code":-32600`},
		{`{"jsonrpc":"2.0","id":null,"method":"wake"}`, `"id":null,"error":{"code":-32600`},
		{`{"jsonrpc":"2.0","id":6,"method":7}`, `"id":6,"error":{"code":-32600`},
		{`{"jsonrpc":"2.0"}`, `"id":null,"error":{"code":-32600`},
		{`{"jsonrpc":"2.0","id":7,"result":{},"error":{"code":1,"message":"x"}}`, `"id":7,"error":{"code":-32600`},
		{`{"jsonrpc":"2.0","id":8,"error":5}`, `"id":8,"error":{"code":-32600`},
		{`5`, `"id":null,"error":{"code":-32600`},
		{`[]`, `"id":null,"error":{"code":-32600`},
		{`not json`, `"id":null,"error":{"code":-32700`},
		{`{"jsonrpc":"2.0","id":9,"method":"nope","params":{}}`, `"id":9,"error":{"code":-32601`},
	}
	for _, c := range cases {
		g.send(c.send)
		if l := g.line(); !strings.Contains(l, c.want) {
			t.Errorf("for %s: got %s, want it to contain %s", c.send, l, c.want)
		}
	}
	// A notification and a response nobody waits for are ignored: the next
	// request is the next thing answered.
	g.send(`{"jsonrpc":"2.0","method":"whatever"}`)
	g.send(`{"jsonrpc":"2.0","id":77,"result":{}}`)
	id := g.request("cancel", `{"turnId":"`+turnA+`","reason":"human"}`)
	if l := g.line(); l != fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, id) {
		t.Fatalf("got %s", l)
	}
}

func TestBadParamsGet32602(t *testing.T) {
	chat := chatPayloadJSON
	bus := `{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","from":"agent:watcher","to":"founder","subject":"s","body":"b","sentAt":"2026-11-19T19:00:01Z"}`
	cases := map[string][2]string{
		"wake not object":       {"wake", `[]`},
		"wake unknown field":    {"wake", `{"reason":"chat","turnId":"` + turnA + `","payload":` + chat + `,"x":1}`},
		"wake bad turn id":      {"wake", `{"reason":"chat","turnId":"nope","payload":` + chat + `}`},
		"wake reason unknown":   {"wake", `{"reason":"bogus","turnId":"` + turnA + `","payload":{}}`},
		"wake reason type":      {"wake", `{"reason":5,"turnId":"` + turnA + `","payload":{}}`},
		"chat payload missing":  {"wake", `{"reason":"chat","turnId":"` + turnA + `","payload":{"id":"x"}}`},
		"chat payload type":     {"wake", `{"reason":"chat","turnId":"` + turnA + `","payload":` + strings.Replace(chat, `"Hello"`, `5`, 1) + `}`},
		"message payload":       {"wake", `{"reason":"message","turnId":"` + turnA + `","payload":[]}`},
		"task payload":          {"wake", `{"reason":"task","turnId":"` + turnA + `","payload":{}}`},
		"timer payload":         {"wake", `{"reason":"timer","turnId":"` + turnA + `","payload":{}}`},
		"event payload":         {"wake", `{"reason":"event","turnId":"` + turnA + `","payload":{}}`},
		"deliver kind":          {"deliver", `{"kind":"pigeon","turnId":"` + turnA + `","message":{}}`},
		"deliver not object":    {"deliver", `5`},
		"deliver bad turn":      {"deliver", `{"kind":"chat","turnId":"x","message":` + chat + `}`},
		"deliver type":          {"deliver", `{"kind":5,"turnId":"` + turnA + `","message":` + chat + `}`},
		"deliver bus invalid":   {"deliver", `{"kind":"bus","turnId":"` + turnA + `","message":{}}`},
		"cancel not object":     {"cancel", `"x"`},
		"cancel type":           {"cancel", `{"turnId":5,"reason":"human"}`},
		"cancel bad turn":       {"cancel", `{"turnId":"x","reason":"human"}`},
		"cancel bad reason":     {"cancel", `{"turnId":"` + turnA + `","reason":"because"}`},
		"sleep not object":      {"sleep", `1`},
		"sleep type":            {"sleep", `{"reason":"idle","maxSummaryChars":"x"}`},
		"sleep bad reason":      {"sleep", `{"reason":"nap","maxSummaryChars":256}`},
		"sleep range low":       {"sleep", `{"reason":"idle","maxSummaryChars":255}`},
		"sleep range high":      {"sleep", `{"reason":"idle","maxSummaryChars":65537}`},
		"shutdown not object":   {"shutdown", `1`},
		"shutdown type":         {"shutdown", `{"reason":"stop","graceMs":"x"}`},
		"shutdown bad reason":   {"shutdown", `{"reason":"nap","graceMs":1}`},
		"shutdown negative":     {"shutdown", `{"reason":"stop","graceMs":-1}`},
		"deliver bus ok shape":  {"deliver", `{"kind":"bus","turnId":"` + turnA + `","message":` + strings.Replace(bus, `"to"`, `"too"`, 1) + `}`},
		"wake bus message type": {"wake", `{"reason":"message","turnId":"` + turnA + `","payload":` + strings.Replace(bus, `"s"`, `5`, 1) + `}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRig(t, echoOpts(), funcHandler{})
			g.ready()
			id := g.request(c[0], c[1])
			want := fmt.Sprintf(`"id":%d,"error":{"code":-32602`, id)
			if l := g.line(); !strings.Contains(l, want) {
				t.Fatalf("got %s, want %s", l, want)
			}
		})
	}
}

// collect runs one chat turn whose handler body is fn and returns the lines
// from the ack to the closing status idle.
func collect(t *testing.T, fn func(context.Context, *sdk.Turn, sdk.Wake) error) []string {
	t.Helper()
	g := newRig(t, echoOpts(), funcHandler{wake: fn})
	g.ready()
	g.wakeChat(turnA)
	return g.until(`"state":"idle","turnId":"` + turnA)
}

func TestTurnHelpersRejectBadInput(t *testing.T) {
	var got []error
	collect(t, func(_ context.Context, tn *sdk.Turn, _ sdk.Wake) error {
		got = append(got,
			tn.ProfileUpdate(""),
			tn.ProfileUpdate(strings.Repeat("x", 256)),
			tn.ProfileUpdate("two\nlines"),
			tn.Usage(sdk.Usage{TokensIn: -1}),
			tn.FinishOutput("done"),
			tn.Output("more"),
		)
		return tn.ProfileUpdate("Goal: test. Now: done.")
	})
	for i, want := range []error{sdk.ErrInvalidOneLine, sdk.ErrInvalidOneLine, sdk.ErrInvalidOneLine} {
		if !errors.Is(got[i], want) {
			t.Errorf("call %d: err = %v, want %v", i, got[i], want)
		}
	}
	if got[3] == nil {
		t.Error("negative usage was accepted")
	}
	if got[4] != nil || !errors.Is(got[5], sdk.ErrOutputClosed) {
		t.Errorf("output after the final chunk: %v, %v; want nil, ErrOutputClosed", got[4], got[5])
	}
}

func TestTurnHelpersWriteTheirMessages(t *testing.T) {
	lines := collect(t, func(_ context.Context, tn *sdk.Turn, _ sdk.Wake) error {
		if tn.Resumed() {
			return errors.New("a fresh turn is not resumed")
		}
		_ = tn.Log(sdk.LogWarn, "careful", nil)
		_ = tn.Error(sdk.CodeModelUnavailable, "model down", false)
		return tn.ProfileUpdate("Goal: test. Now: logging.")
	})
	got := joined(lines)
	for _, want := range []string{
		`"method":"log","params":{"level":"warn","message":"careful","turnId":"` + turnA + `"}`,
		`"method":"error","params":{"code":1020,"message":"model down","turnId":"` + turnA + `","fatal":false}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
}

func TestHandlerErrorsEndTheTurnWithAnError(t *testing.T) {
	coded := joined(collect(t, func(context.Context, *sdk.Turn, sdk.Wake) error {
		return &sdk.CodedError{Code: sdk.CodeModelUnavailable, Message: "model down"}
	}))
	if !strings.Contains(coded, `"code":1020`) || !strings.Contains(coded, `"detail":"Failed"`) {
		t.Fatalf("want error 1020 and detail Failed, got\n%s", coded)
	}
	if !strings.Contains(coded, `"method":"profile.update"`) {
		t.Fatalf("a failed turn still needs a profile.update (T3), got\n%s", coded)
	}
	plain := joined(collect(t, func(context.Context, *sdk.Turn, sdk.Wake) error { return errors.New("boom") }))
	if !strings.Contains(plain, `"code":-32603`) {
		t.Fatalf("want error -32603 for a plain error, got\n%s", plain)
	}
	if (&sdk.CodedError{Code: 1, Message: "m"}).Error() != "m" {
		t.Fatal("CodedError.Error is the message")
	}
}

func TestOutputIsSplitBelowTheLineLimit(t *testing.T) {
	lines := collect(t, func(_ context.Context, tn *sdk.Turn, _ sdk.Wake) error {
		if err := tn.ProfileUpdate("Goal: test. Now: long."); err != nil {
			return err
		}
		return tn.FinishOutput(strings.Repeat("é", 100_000))
	})
	chunks := 0
	for _, l := range lines {
		if strings.Contains(l, `"method":"output"`) {
			chunks++
			if len(l) > 65536 {
				t.Fatalf("output line is %d bytes, over maxLineBytes", len(l))
			}
		}
	}
	if chunks < 3 || !strings.Contains(joined(lines), `"final":true`) {
		t.Fatalf("want at least 3 chunks with a final one, got %d", chunks)
	}
}

func TestBusSendAndTaskDone(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{wake: func(ctx context.Context, tn *sdk.Turn, _ sdk.Wake) error {
		if err := tn.ProfileUpdate("Goal: test. Now: sending."); err != nil {
			return err
		}
		// Too big for one line: refused here, and the id is not used up.
		if _, err := tn.BusSend(ctx, sdk.BusSend{To: "watcher", Subject: "s", Body: strings.Repeat("x", 70_000)}); !errors.Is(err, sdk.ErrLineTooLarge) {
			return fmt.Errorf("want ErrLineTooLarge: %w", err)
		}
		id, err := tn.BusSend(ctx, sdk.BusSend{To: "watcher", Subject: "s", Body: "b", ReplyTo: "msg_1", CorrelationID: "msg_2"})
		if err != nil || id != "msg_01JD8X3Q5Y6Z7A8B9C0D1E2F43" {
			return fmt.Errorf("bus.send = %q: %w", id, err)
		}
		if _, err := tn.BusSend(ctx, sdk.BusSend{To: "x", Subject: "s", Body: "b"}); err == nil {
			return errors.New("want the daemon's refusal")
		}
		return tn.TaskDone(ctx, sdk.TaskDoneReport{TaskID: "tsk_1", Outcome: sdk.TaskDeclined, Summary: "no"})
	}})
	g.ready()
	g.wakeChat(turnA)
	var all []string
	for {
		l := g.line()
		all = append(all, l)
		switch {
		case strings.Contains(l, `"method":"bus.send"`) && strings.Contains(l, `"to":"watcher"`):
			g.send(`{"jsonrpc":"2.0","id":2,"result":{"messageId":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F43"}}`)
		case strings.Contains(l, `"method":"bus.send"`):
			g.send(`{"jsonrpc":"2.0","id":3,"error":{"code":1002,"message":"Unknown recipient"}}`)
		case strings.Contains(l, `"method":"task.done"`):
			g.send(`{"jsonrpc":"2.0","id":4,"result":{}}`)
		}
		if strings.Contains(l, `"state":"idle","turnId"`) {
			break
		}
	}
	got := joined(all)
	for _, want := range []string{`"id":2,"method":"bus.send"`, `"replyTo":"msg_1","correlationId":"msg_2"`, `"id":3,"method":"bus.send"`, `"id":4,"method":"task.done"`, `"outcome":"declined"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Contains(got, `"detail":"Failed"`) {
		t.Errorf("turn failed:\n%s", got)
	}
}

func TestSleep(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{sleep: func(context.Context, sdk.SleepRequest) (string, error) {
		return strings.Repeat("ü", 500), nil
	}})
	g.ready()
	id := g.request("sleep", `{"reason":"idle","maxSummaryChars":256}`)
	l := g.line()
	var res struct {
		ID     int
		Result struct{ Summary string }
	}
	if err := json.Unmarshal([]byte(l), &res); err != nil || res.ID != id || len([]rune(res.Result.Summary)) != 256 {
		t.Fatalf("want a summary cut to 256 characters, got %s", l)
	}
	if l := g.line(); !strings.Contains(l, `"state":"sleeping"`) {
		t.Fatalf("want status sleeping, got %s", l)
	}
}

func TestSleepFailure(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{sleep: func(context.Context, sdk.SleepRequest) (string, error) {
		return "", errors.New("no model")
	}})
	g.ready()
	g.request("sleep", `{"reason":"idle","maxSummaryChars":256}`)
	if l := g.line(); !strings.Contains(l, `"code":-32603`) {
		t.Fatalf("want -32603, got %s", l)
	}
}

func TestShutdownWaitsForTheTurnWithinGrace(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{wake: func(ctx context.Context, tn *sdk.Turn, _ sdk.Wake) error {
		if err := tn.ProfileUpdate("Goal: test. Now: waiting."); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}})
	g.ready()
	g.wakeChat(turnA)
	g.until(`"state":"working"`)
	g.request("shutdown", `{"reason":"stop","graceMs":10000}`)
	if err := g.wait(); err != nil {
		t.Fatalf("Run after shutdown = %v, want nil", err)
	}
}

func TestResumeAfterRestartFromWelcome(t *testing.T) {
	h := resumer{paused: turnA}
	h.resume = func(_ context.Context, tn *sdk.Turn) error {
		if !tn.Resumed() {
			return errors.New("the turn must say it is resumed")
		}
		return tn.FinishOutput("resumed")
	}
	g := newRig(t, sdk.Options{Runner: sdk.Info{Name: "t", Version: "1"}, Capabilities: sdk.Capabilities{Resume: true}}, h)
	g.handshake(strings.Replace(welcomeJSON, `"pausedTurnId":null`, `"pausedTurnId":"`+turnA+`"`, 1))
	g.request("wake", `{"reason":"event","turnId":"`+turnA+`","payload":{"specversion":"1.0","id":"evt_1","source":"/s","type":"`+sdk.BudgetRestoredEvent+`",`+
		`"time":"2026-11-19T19:05:00Z","datacontenttype":"application/json","data":{},"sequence":"1","actor":"host:agentsone","chartscope":"agent:founder"}}`)
	got := joined(g.until(`"state":"idle","turnId"`))
	if !strings.Contains(got, `"text":"resumed"`) || strings.Contains(got, `"method":"profile.update"`) {
		t.Fatalf("want resumed output and no new profile.update, got\n%s", got)
	}
}

func TestPausedTurnPausesAgainAndCancelClearsIt(t *testing.T) {
	h := resumer{paused: turnA}
	h.resume = func(context.Context, *sdk.Turn) error { return sdk.ErrBudgetExhausted }
	h.wake = func(_ context.Context, tn *sdk.Turn, _ sdk.Wake) error {
		if err := tn.ProfileUpdate("Goal: test. Now: waiting for budget."); err != nil {
			return err
		}
		return sdk.ErrBudgetExhausted
	}
	g := newRig(t, sdk.Options{Runner: sdk.Info{Name: "t", Version: "1"}, Capabilities: sdk.Capabilities{Resume: true}}, h)
	g.ready()
	restored := `{"reason":"event","turnId":"` + turnA + `","payload":{"specversion":"1.0","id":"evt_1","source":"/s","type":"` + sdk.BudgetRestoredEvent + `",` +
		`"time":"2026-11-19T19:05:00Z","datacontenttype":"application/json","data":{},"sequence":"1","actor":"host:agentsone","chartscope":"agent:founder"}}`
	g.wakeChat(turnA)
	g.until(`"pausedTurnId":"` + turnA + `"`)
	g.request("wake", restored)
	g.until(`"pausedTurnId":"` + turnA + `"`) // paused again
	g.request("cancel", `{"turnId":"`+turnA+`","reason":"human"}`)
	g.until(`"id":3,"result"`)
	id := g.request("wake", restored)
	if l := g.line(); !strings.Contains(l, fmt.Sprintf(`"id":%d,"error":{"code":1011`, id)) {
		t.Fatalf("want 1011 after the paused turn was cancelled, got %s", l)
	}
}

func TestDeliverToUnknownTurn(t *testing.T) {
	g := newRig(t, echoOpts(), funcHandler{})
	g.ready()
	id := g.request("deliver", `{"kind":"bus","turnId":"`+turnB+`","message":{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","from":"agent:w","to":"founder","subject":"s","body":"b","sentAt":"2026-11-19T19:00:01Z"}}`)
	if l := g.line(); !strings.Contains(l, fmt.Sprintf(`"id":%d,"error":{"code":1001`, id)) {
		t.Fatalf("got %s", l)
	}
}

func TestDeliveryReachesTheTurn(t *testing.T) {
	got := make(chan sdk.Delivery, 1)
	g := newRig(t, echoOpts(), funcHandler{wake: func(ctx context.Context, tn *sdk.Turn, _ sdk.Wake) error {
		if err := tn.ProfileUpdate("Goal: test. Now: waiting."); err != nil {
			return err
		}
		select {
		case <-tn.Delivered():
			got <- tn.Deliveries()[0]
		case <-ctx.Done():
		}
		return nil
	}})
	g.ready()
	g.wakeChat(turnA)
	g.until(`"state":"working"`)
	g.request("deliver", `{"kind":"bus","turnId":"`+turnA+`","message":{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F41","from":"agent:w","to":"founder","subject":"s","body":"from the bus","sentAt":"2026-11-19T19:00:01Z"}}`)
	d := <-got
	if d.Kind != sdk.DeliverBus || d.Text() != "from the bus" {
		t.Fatalf("delivery = %+v", d)
	}
	if (sdk.Delivery{Chat: &sdk.ChatMessage{Text: "hi"}}).Text() != "hi" || (sdk.Delivery{}).Text() != "" {
		t.Fatal("Delivery.Text")
	}
}

func TestWakeTextAndFrom(t *testing.T) {
	note := "remember"
	cases := []struct {
		w          sdk.Wake
		text, from string
	}{
		{sdk.Wake{Chat: &sdk.ChatMessage{Text: "c", From: "human:a"}}, "c", "human:a"},
		{sdk.Wake{Message: &sdk.BusMessage{Body: "m", From: "agent:b"}}, "m", "agent:b"},
		{sdk.Wake{Task: &sdk.Task{Body: "t", From: "human:c"}}, "t", "human:c"},
		{sdk.Wake{Timer: &sdk.Timer{Note: &note}}, "remember", ""},
		{sdk.Wake{Timer: &sdk.Timer{}}, "", ""},
		{sdk.Wake{Event: &sdk.Event{}}, "", ""},
	}
	for _, c := range cases {
		if c.w.Text() != c.text || c.w.From() != c.from {
			t.Errorf("%+v: Text %q From %q, want %q %q", c.w, c.w.Text(), c.w.From(), c.text, c.from)
		}
	}
}

func TestSecretsStayOutOfLogs(t *testing.T) {
	var w sdk.Welcome
	if err := json.Unmarshal([]byte(welcomeJSON), &w); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{fmt.Sprint(w.MCP.Token), fmt.Sprintf("%v %+v %#v", w.Model.Token, w, w.MCP.Token)} {
		if strings.Contains(s, "secret") {
			t.Fatalf("a token leaked: %s", s)
		}
	}
	b, err := json.Marshal(w.Model.Token)
	if err != nil || strings.Contains(string(b), "secret") {
		t.Fatalf("a token leaked through JSON: %s %v", b, err)
	}
	if w.Model.Token.Reveal() != "model-secret" {
		t.Fatal("Reveal returns the value")
	}
	var s sdk.Secret
	if err := json.Unmarshal([]byte(`5`), &s); err == nil {
		t.Fatal("a secret must be a string")
	}
}

func TestOneLine(t *testing.T) {
	if got := sdk.OneLine("  a\n b\t c  "); got != "a b c" {
		t.Fatalf("OneLine = %q", got)
	}
	long := sdk.OneLine(strings.Repeat("word ", 100))
	if len([]rune(long)) != sdk.MaxOneLine || !strings.HasSuffix(long, "…") {
		t.Fatalf("OneLine of long text = %d runes, %q", len([]rune(long)), long)
	}
	if sdk.OneLine("  \n ") != "" {
		t.Fatal("blank text gives an empty line")
	}
	if sdk.ValidateOneLine("ok") != nil || sdk.ValidateOneLine(strings.Repeat("x", 255)) != nil {
		t.Fatal("valid lines are accepted")
	}
}

func TestErrorsReadWell(t *testing.T) {
	if got := (&sdk.RPCError{Code: 1010, Message: "Budget exhausted"}).Error(); got != "rpc error 1010: Budget exhausted" {
		t.Fatal(got)
	}
	if !strings.Contains((&sdk.UnsupportedVersionError{}).Error(), "version") {
		t.Fatal("UnsupportedVersionError names the problem")
	}
	if !errors.Is(&sdk.UnsupportedVersionError{}, sdk.ErrUnsupportedVersion) {
		t.Fatal("UnsupportedVersionError unwraps to ErrUnsupportedVersion")
	}
}

func TestRunNeedsASocket(t *testing.T) {
	t.Setenv(sdk.EnvSocket, "")
	if err := sdk.Run(context.Background(), echoOpts(), funcHandler{}); err == nil || !strings.Contains(err.Error(), sdk.EnvSocket) {
		t.Fatalf("err = %v, want it to name %s", err, sdk.EnvSocket)
	}
	o := echoOpts()
	o.Socket = t.TempDir() + "/missing.sock"
	if err := sdk.Run(context.Background(), o, funcHandler{}); err == nil {
		t.Fatal("a missing socket is an error")
	}
}

func mainSubject() conformance.Subject {
	return conformance.Subject{
		Name: "main",
		Start: conformance.InProcess(func(_ context.Context, socket string) error {
			o := echoOpts()
			o.Socket = socket
			if code := sdk.Main(o, funcHandler{}); code != 0 {
				return fmt.Errorf("exit %d", code)
			}
			return nil
		}),
	}
}

// TestMainExitStatus proves X1 and X2 for the process wrapper: 0 after
// shutdown, non-zero when the socket closes, and non-zero when it cannot
// connect.
func TestMainExitStatus(t *testing.T) {
	h := conformance.Ready(t, mainSubject(), nil)
	id := h.Request("shutdown", map[string]any{"reason": "stop", "graceMs": 1000})
	h.ExpectResult(id, "shutdown")
	if code := h.WaitExit(10e9); code != 0 {
		t.Fatalf("exit status after shutdown = %d, want 0", code)
	}
	h = conformance.Ready(t, mainSubject(), nil)
	h.CloseSocket()
	if code := h.WaitExit(10e9); code == 0 {
		t.Fatal("exit status after the daemon went away = 0, want non-zero")
	}
	t.Setenv(sdk.EnvSocket, "")
	if code := sdk.Main(echoOpts(), funcHandler{}); code == 0 {
		t.Fatal("Main without a socket must fail")
	}
}
