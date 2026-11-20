// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"net/http"
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// Integration tests: first setup on a real daemon, with its real engine
// fetcher. The Claude engine is off by default (SDK-2059), so a fresh
// install is done once the first human exists: no licence step, no download
// step, and nothing downloaded, also after a restart.

func TestFirstSetup_IsDoneOnceTheFirstHumanExistsAndTheEngineStaysOff(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)

	var before api.SetupStatus
	d.Anonymous().Do(http.MethodGet, "/setup", nil, &before)
	if before.Phase != api.SetupPhaseNeedsHuman || before.FirstHumanCreated {
		t.Fatalf("fresh install: phase %q, first human %v; want needs-human and no human", before.Phase, before.FirstHumanCreated)
	}

	alice := d.SignInHuman("alice")
	var after api.SetupStatus
	alice.Do(http.MethodGet, "/setup", nil, &after)
	assertEngineOff(t, "after the first human", after)

	// A restart starts the fetcher again: still no engine step and nothing
	// downloaded.
	d = d.Restart()
	var restarted api.SetupStatus
	d.LogIn("alice").Do(http.MethodGet, "/setup", nil, &restarted)
	assertEngineOff(t, "after a restart", restarted)
}

// assertEngineOff fails unless setup is done with the engine untouched: the
// licence not accepted, the engine waiting for it, and no byte downloaded.
func assertEngineOff(t *testing.T, when string, st api.SetupStatus) {
	t.Helper()
	if st.Phase != api.SetupPhaseReady {
		t.Fatalf("%s: phase %q, want ready with no engine step", when, st.Phase)
	}
	if st.LicenceAccepted {
		t.Fatalf("%s: the engine licence counts as accepted, but nobody turned the engine on", when)
	}
	if st.Engine.State != api.EngineFetchStateWaitingLicence || st.Engine.BytesDone != 0 {
		t.Fatalf("%s: engine %s with %d bytes, want waiting-licence with nothing downloaded", when, st.Engine.State, st.Engine.BytesDone)
	}
}
