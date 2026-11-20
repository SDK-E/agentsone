// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// Integration test: a human's request is routed by the real router, whose one quick model call
// goes through the real model gateway to a fake provider that refuses sampling fields the way
// Claude 4.7 and later do. The router sends "temperature": 0; the gateway drops it for the
// model, so the model's rating decides the pick instead of a text match.
func TestRouting_TheRoutersRatingCallGoesThroughTheGatewayToTheQuickModel(t *testing.T) {
	t.Parallel()
	model := daemontest.NewFakeModel(t)
	d := daemontest.Start(t, daemontest.WithModelProvider(model.URL(), model.Key()))
	alice := d.SignInHuman("alice")
	d.AsAgent("founder").HireEcho("planner")
	model.Script(daemontest.ModelReply{Text: `{"ratings":[{"agent":"planner","score":0.9,"reason":"Plans events."}]}`})

	var asg api.Assignment
	alice.Do(http.MethodPost, "/router/route", api.RouteRequest{Text: "Plan the launch party for Friday."}, &asg)

	if asg.Chosen != "planner" || len(asg.Shortlist) == 0 || asg.Shortlist[0].Reason != "Plans events." {
		t.Fatalf("assignment = %+v, want planner picked by the model's rating", asg)
	}
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("the model got %d calls, want the router's one: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.Model != daemontest.ModelQuick || !c.Scripted || c.Refused != "" {
		t.Fatalf("call to %q (scripted %v, refused %q), want the quick model, answered", c.Model, c.Scripted, c.Refused)
	}
	if slices.Contains(c.Fields, "temperature") {
		t.Errorf("the gateway forwarded temperature to a model that refuses it: fields %v", c.Fields)
	}
	if !c.ProviderKey || c.AgentToken {
		t.Errorf("provider key %v, router token seen %v; want the gateway's key and never the token", c.ProviderKey, c.AgentToken)
	}
}
