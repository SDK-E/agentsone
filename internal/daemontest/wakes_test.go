// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// Integration tests: one per wake reason. Each goes from the API through
// the real modules, the supervisor and the runner socket to the real echo
// runner, and back. A wake payload that breaks the runner contract makes
// the runner refuse the wake, and the test sees no answer.

func TestChatWake_TheRunnerAnswersAndTheReplyIsStored(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireEcho("echo")

	reply := alice.Chat("echo", "hello there")

	if reply.Text != "echo: hello there" {
		t.Fatalf("reply = %q, want %q", reply.Text, "echo: hello there")
	}
	if reply.Author != "agent:echo" {
		t.Fatalf("reply author = %q, want agent:echo", reply.Author)
	}
}

func TestBusWake_TheRunnerGetsTheMessageBody(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireEcho("echo")
	out := alice.Output("echo")

	alice.SendBus("echo", "Status please\nHow is the report going?")

	out.WaitText("echo: Status please\nHow is the report going?")
}

func TestTaskWake_TheRunnerGetsTheTask(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	founder := d.AsAgent("founder")
	founder.HireEcho("echo")
	out := alice.Output("echo")
	assignee, detail := "echo", "Two pages, plain words."

	founder.CreateTask(api.TaskCreate{Title: "Write the report", Detail: &detail, Assignee: &assignee})

	out.WaitText("echo: task Write the report: Two pages, plain words.")
}

func TestTimerWake_TheRunnerGetsTheScheduleWhenItFires(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	founder := d.AsAgent("founder")
	founder.HireEcho("echo")
	out := alice.Output("echo")
	agent := "echo"
	founder.CreateSchedule(api.ScheduleCreate{Agent: &agent, Name: "morning", Calendar: "*-*-* 06:00:00", Message: "Plan the day"})

	d.FireSchedules(6*time.Hour + time.Minute)

	out.WaitText("echo: timer morning")
}

func TestEventWake_TheRunnerGetsTheEventATriggerMatched(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	founder := d.AsAgent("founder")
	founder.HireEcho("echo")
	out := alice.Output("echo")
	var action api.TriggerAction
	if err := action.FromWakeAction(api.WakeAction{Kind: api.WakeActionKindWake, Agent: "echo", Message: "{{type}} happened"}); err != nil {
		t.Fatal(err)
	}
	types := []string{"agentsone.reports.published"}
	founder.CreateTrigger(api.TriggerCreate{
		Name: "reports", Filter: api.EventFilter{Types: &types}, Action: action,
		WakeRate: api.WakeRate{Max: 5, PerSeconds: 60},
	})

	alice.PublishEvent(api.EventPublish{Type: "agentsone.reports.published", Data: map[string]any{"title": "Q3"}})

	out.WaitText("echo: event agentsone.reports.published")
}
