// SPDX-License-Identifier: MIT

package sdk_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/SDK-E/agentsone/runners/conformance"
	"github.com/SDK-E/agentsone/runners/sdk"
)

const transcriptPath = "../../contract/fixtures/schemas/runner-message/transcript.ndjson"

const goalPrefix = "Goal: keep this machine useful to its humans. Now: "

// transcriptRunner does what the runner in the PROTOCOL.md transcript does,
// with no model. It proves that the SDK puts the transcript's lines on the
// wire byte for byte.
type transcriptRunner struct {
	tasks map[string]*sdk.Task
}

func (r *transcriptRunner) Start(context.Context, sdk.Welcome) error { return nil }

func (r *transcriptRunner) Sleep(context.Context, sdk.SleepRequest) (string, error) {
	return "Chatted with alice. Wrote the weekly status note (task done). watcher acknowledged.", nil
}

func (r *transcriptRunner) CanResume(turnID string) bool { _, ok := r.tasks[turnID]; return ok }

func (r *transcriptRunner) Resume(ctx context.Context, t *sdk.Turn) error {
	zero := 0
	if err := t.Usage(sdk.Usage{Model: "echo", Weight: "normal", TokensIn: 40, TokensOut: 18, CacheReadTokens: &zero}); err != nil {
		return err
	}
	return t.TaskDone(ctx, sdk.TaskDoneReport{TaskID: r.tasks[t.ID()].ID, Outcome: sdk.TaskDone,
		Summary: "Weekly status note written to common memory", Details: "Saved as memory mem_01JD8X3Q5Y6Z7A8B9C0D1E2F80."})
}

func (r *transcriptRunner) Wake(ctx context.Context, t *sdk.Turn, w sdk.Wake) error {
	switch w.Reason {
	case sdk.WakeChat:
		return r.chat(ctx, t, w)
	case sdk.WakeTask:
		r.tasks[t.ID()] = w.Task
		if err := t.ProfileUpdate(goalPrefix + "writing the weekly status note for alice."); err != nil {
			return err
		}
		if err := t.Log(sdk.LogInfo, "Calling the model gateway", map[string]string{"weight": "normal"}); err != nil {
			return err
		}
		return sdk.ErrBudgetExhausted
	case sdk.WakeTimer:
		if err := t.ProfileUpdate(goalPrefix + "reviewing the day."); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	case sdk.WakeMessage:
		if err := t.ProfileUpdate(goalPrefix + "reading a reply from watcher."); err != nil {
			return err
		}
		return t.FinishOutput("Noted.")
	}
	return errors.New("unexpected wake")
}

func (r *transcriptRunner) chat(ctx context.Context, t *sdk.Turn, w sdk.Wake) error {
	if err := t.ProfileUpdate(goalPrefix + "answering alice in chat."); err != nil {
		return err
	}
	if err := t.Output("Hello "); err != nil {
		return err
	}
	select {
	case <-t.Delivered():
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := t.Usage(sdk.Usage{Model: "echo", Weight: "quick", TokensIn: 12, TokensOut: 4}); err != nil {
		return err
	}
	if err := t.FinishOutput("founder\n\n" + t.Deliveries()[0].Text()); err != nil {
		return err
	}
	_, err := t.BusSend(ctx, sdk.BusSend{To: "ghost", Subject: "alice said hello", Body: "alice opened a chat with me."})
	var rpc *sdk.RPCError
	if !errors.As(err, &rpc) || rpc.Code != sdk.CodeUnknownRecipient {
		return errors.New("want unknown recipient for ghost")
	}
	_, err = t.BusSend(ctx, sdk.BusSend{To: "watcher", Subject: "alice said hello", Body: "alice opened a chat with me.", CorrelationID: w.Chat.ID})
	return err
}

// TestTranscriptReplay plays the PROTOCOL.md transcript (TESTING B4c) and
// compares every runner line with the fixture, byte for byte.
func TestTranscriptReplay(t *testing.T) {
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	opts := sdk.Options{
		Runner:       sdk.Info{Name: "echo", Version: "1.0.0"},
		Capabilities: sdk.Capabilities{Streaming: true, Cancel: true, Compaction: true, Resume: true},
	}
	subject := conformance.Subject{
		Name: "transcript",
		Start: conformance.InProcess(func(ctx context.Context, socket string) error {
			o := opts
			o.Socket = socket
			return sdk.Run(ctx, o, &transcriptRunner{tasks: map[string]*sdk.Task{}})
		}),
	}
	conformance.ReplayTranscript(t, subject, data)
}
