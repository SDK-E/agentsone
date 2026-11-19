// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	budget "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/admin"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/keyfile"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

type memKeys struct {
	mu   sync.Mutex
	keys map[routing.ProviderID]string
	fail error
}

func (m *memKeys) Get(_ context.Context, p routing.ProviderID) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return "", m.fail
	}
	k, ok := m.keys[p]
	if !ok {
		return "", gatewayapp.ErrKeyNotFound
	}
	return k, nil
}

func (m *memKeys) Set(_ context.Context, p routing.ProviderID, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.ContainsAny(k, " \n") || k == "" {
		return keyfile.ErrBadKey
	}
	m.keys[p] = k
	return nil
}

func (m *memKeys) Delete(_ context.Context, p routing.ProviderID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.keys[p]; !ok {
		return gatewayapp.ErrKeyNotFound
	}
	delete(m.keys, p)
	return nil
}

type memProviders struct {
	mu   sync.Mutex
	list []gatewayconfig.Provider
	fail error
}

func (m *memProviders) List() ([]gatewayconfig.Provider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]gatewayconfig.Provider(nil), m.list...), m.fail
}

func (m *memProviders) SetEnabled(id string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.list {
		if m.list[i].ID == id {
			m.list[i].Enabled = on
			return nil
		}
	}
	return fmt.Errorf("%w: %q", gatewayconfig.ErrUnknownProvider, id)
}

type memBudgetStore struct {
	set budgetapp.Settings
	sp  budgetapp.Spend
}

func (m *memBudgetStore) LoadSettings(context.Context) (budgetapp.Settings, error) { return m.set, nil }
func (m *memBudgetStore) SaveSettings(_ context.Context, s budgetapp.Settings) error {
	m.set = s
	return nil
}
func (m *memBudgetStore) LoadSpend(context.Context) (budgetapp.Spend, error) { return m.sp, nil }
func (m *memBudgetStore) SaveSpend(_ context.Context, s budgetapp.Spend) error {
	m.sp = s
	return nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC) }

type fixture struct {
	client    *adminapi.Client
	keys      *memKeys
	providers *memProviders
	budget    *budgetapp.Service
	changed   *int
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	keys := &memKeys{keys: map[routing.ProviderID]string{"openai": "sk-openai-1"}}
	providers := &memProviders{list: []gatewayconfig.Provider{
		{ID: "openai", KeyRequired: true, Enabled: true, Models: []gatewayconfig.Model{{ID: "gpt-5-mini", Tier: "normal"}}},
		{ID: "custom", KeyRequired: false},
	}}
	svc, err := budgetapp.Open(t.Context(), &memBudgetStore{set: budgetapp.Settings{Total: 10 * budget.MicrosPerUSD}}, fixedClock{})
	if err != nil {
		t.Fatalf("Open budget: %v", err)
	}
	changed := 0
	mux := http.NewServeMux()
	admin.Register(mux, admin.Options{Keys: keys, Providers: providers, Budget: svc, Changed: func() { changed++ }})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fixture{adminapi.NewHTTPClient(srv.URL, srv.Client()), keys, providers, svc, &changed}
}

func TestProviders_ListsProvidersWithKeyStateButNeverTheKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	got, err := f.client.Providers(t.Context())
	if err != nil {
		t.Fatalf("Providers() error = %v", err)
	}

	want := []adminapi.Provider{
		{ID: "openai", Enabled: true, KeyRequired: true, Key: adminapi.KeyState{Set: true, Fingerprint: adminapi.Fingerprint("sk-openai-1")},
			Models: []adminapi.Model{{ID: "gpt-5-mini", Tier: "normal"}}},
		{ID: "custom", Models: []adminapi.Model{}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Providers() (-want +got):\n%s", diff)
	}
	if strings.Contains(fmt.Sprint(got), "sk-openai-1") {
		t.Error("the key itself was returned")
	}
}

func TestProviders_AddAndRemoveSwitchTheProviderAndApplyAtOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	p, err := f.client.AddProvider(t.Context(), "custom")
	if err != nil || !p.Enabled {
		t.Fatalf("AddProvider() = %+v, %v", p, err)
	}
	if err := f.client.RemoveProvider(t.Context(), "openai"); err != nil {
		t.Fatalf("RemoveProvider() error = %v", err)
	}

	list, _ := f.providers.List()
	if !list[1].Enabled || list[0].Enabled {
		t.Errorf("providers = %+v", list)
	}
	if *f.changed != 2 {
		t.Errorf("routing told %d times, want 2", *f.changed)
	}
	if _, err := f.keys.Get(t.Context(), "openai"); err != nil {
		t.Error("removing a provider dropped its key")
	}
}

func TestProviders_UnknownProviderIsNotFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for name, call := range map[string]func() error{
		"add":       func() error { _, err := f.client.AddProvider(t.Context(), "nope"); return err },
		"remove":    func() error { return f.client.RemoveProvider(t.Context(), "nope") },
		"set key":   func() error { _, err := f.client.SetKey(t.Context(), "nope", "sk-x"); return err },
		"clear key": func() error { return f.client.ClearKey(t.Context(), "nope") },
	} {
		if err := call(); !adminapi.IsProblem(err, adminapi.SlugNotFound) {
			t.Errorf("%s: error = %v, want not-found", name, err)
		}
	}
	if *f.changed != 0 {
		t.Error("a refused change reached routing")
	}
}

func TestKeys_SetAnswersTheFingerprintAndClearIsWriteOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	ks, err := f.client.SetKey(t.Context(), "custom", "sk-custom-2")
	if err != nil || !ks.Set || ks.Fingerprint != adminapi.Fingerprint("sk-custom-2") {
		t.Fatalf("SetKey() = %+v, %v", ks, err)
	}
	if k, _ := f.keys.Get(t.Context(), "custom"); k != "sk-custom-2" {
		t.Fatalf("stored key = %q", k)
	}
	if err := f.client.ClearKey(t.Context(), "custom"); err != nil {
		t.Fatalf("ClearKey() error = %v", err)
	}
	if err := f.client.ClearKey(t.Context(), "custom"); err != nil {
		t.Fatalf("ClearKey() twice error = %v, want it harmless", err)
	}
	list, _ := f.client.Providers(t.Context())
	if list[1].Key.Set || list[1].Key.Fingerprint != "" {
		t.Errorf("custom key after clear = %+v", list[1].Key)
	}
}

func TestKeys_RefusesABadKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := f.client.SetKey(t.Context(), "openai", "two words")

	if !adminapi.IsProblem(err, adminapi.SlugValidation) {
		t.Fatalf("error = %v, want validation", err)
	}
	if k, _ := f.keys.Get(t.Context(), "openai"); k != "sk-openai-1" {
		t.Error("a refused key replaced the stored one")
	}
}

func TestBudget_ShowsTheStateInThePeriod(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	got, err := f.client.Budget(t.Context())
	if err != nil {
		t.Fatalf("Budget() error = %v", err)
	}

	if got.Currency != "USD" || got.TotalMicros != 10_000_000 || got.RemainingMicros != 10_000_000 || got.Period != "month" ||
		!got.PeriodStart.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !got.PeriodEnd.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Budget() = %+v", got)
	}
}

func TestBudget_SetsTotalPeriodAndDefence(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	total, period, on, share := int64(25_000_000), "week", true, 20

	if _, err := f.client.SetBudget(t.Context(), adminapi.SetBudgetRequest{TotalMicros: &total, Period: &period}); err != nil {
		t.Fatalf("SetBudget() error = %v", err)
	}
	got, err := f.client.SetDefence(t.Context(), adminapi.SetDefenceRequest{Enabled: &on, SharePct: &share})
	if err != nil {
		t.Fatalf("SetDefence() error = %v", err)
	}

	if got.TotalMicros != total || got.Period != "week" || !got.Defence.Enabled || got.Defence.SharePct != 20 ||
		got.Defence.ReservedMicros != 5_000_000 || got.RemainingMicros != 20_000_000 {
		t.Errorf("budget = %+v", got)
	}
}

func TestBudget_RefusesBadValuesAndChangesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	neg, ok, badPeriod, empty, badShare := int64(-1), int64(99_000_000), "year", "", 101

	for name, call := range map[string]func() error{
		"negative total": func() error {
			_, err := f.client.SetBudget(t.Context(), adminapi.SetBudgetRequest{TotalMicros: &neg})
			return err
		},
		"bad period with good total": func() error {
			_, err := f.client.SetBudget(t.Context(), adminapi.SetBudgetRequest{TotalMicros: &ok, Period: &badPeriod})
			return err
		},
		"empty period": func() error {
			_, err := f.client.SetBudget(t.Context(), adminapi.SetBudgetRequest{Period: &empty})
			return err
		},
		"share over 100": func() error {
			_, err := f.client.SetDefence(t.Context(), adminapi.SetDefenceRequest{SharePct: &badShare})
			return err
		},
	} {
		if err := call(); !adminapi.IsProblem(err, adminapi.SlugValidation) {
			t.Errorf("%s: error = %v, want validation", name, err)
		}
	}
	if st := f.budget.State(); st.Total != 10*budget.MicrosPerUSD || st.Period != budget.PeriodMonth || st.DefenceSharePct != 0 {
		t.Errorf("a refused change was applied: %+v", st)
	}
}

func TestErrors_AnInternalFailureDoesNotLeakItsText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.keys.fail = errors.New("open /var/lib/agentsone-edge/keys/openai: permission denied")

	_, err := f.client.Providers(t.Context())

	if !adminapi.IsProblem(err, adminapi.SlugInternal) || strings.Contains(err.Error(), "/var/lib") {
		t.Fatalf("error = %v, want an internal problem without the path", err)
	}
}

func TestErrors_AProviderListThatCannotBeReadIsInternal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.providers.fail = errors.New("gateway.toml: bad line")

	for name, call := range map[string]func() error{
		"list":    func() error { _, err := f.client.Providers(t.Context()); return err },
		"set key": func() error { _, err := f.client.SetKey(t.Context(), "openai", "sk-x"); return err },
	} {
		if err := call(); !adminapi.IsProblem(err, adminapi.SlugInternal) {
			t.Errorf("%s: error = %v, want internal", name, err)
		}
	}
}
