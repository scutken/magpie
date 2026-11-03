package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// askingRelay is a relay that answers one model only with one key, as a
// site whose groups serve different models does: sk-b serves m-b, and
// every other key turns m-b away. It keeps what was asked, as key:model.
func askingRelay(t *testing.T, good, model string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	asked := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		asked = append(asked, key+":"+in.Model)
		mu.Unlock()
		if key != good || in.Model != model {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":{"message":"key %s has no model %s"}}`, key, in.Model)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

// A key's own models (#474): the test of a model is asked with the key the
// gateway would send it to, not the provider's first key, which another
// group's site doesn't serve it on.
func TestModelTestUsesTheKeyServingIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	srv, asked := askingRelay(t, "sk-b", "m-b")
	if err := Save(Provider{ID: "relay", Name: "Relay", Chat: srv.URL + "/v1",
		Key: "sk-a", Keys: []KeyAccount{{Name: "b", Key: "sk-b"}}, Models: []string{"m-a", "m-b"}}); err != nil {
		t.Fatal(err)
	}
	if err := SetAccountModels("relay", keyID("sk-a"), []string{"m-a"}); err != nil {
		t.Fatal(err)
	}
	if err := SetAccountModels("relay", keyID("sk-b"), []string{"m-b"}); err != nil {
		t.Fatal(err)
	}
	p, err := Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	rs := p.TestModels(context.Background(), []string{"m-b"})
	if len(rs) != 1 || !rs[0].OK {
		t.Fatalf("m-b's test: %+v (asked %v)", rs, asked())
	}
	for _, a := range asked() {
		if strings.HasPrefix(a, "sk-a:") {
			t.Errorf("asked the key the user kept off m-b: %v", asked())
		}
	}
}

// An account's own models (#474): a model every account on is barred from
// is not asked at all, rather than asked of the first account and failed
// for a reason the user already said.
func TestModelTestBarredModelIsNotAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	srv, asked := askingRelay(t, "sk-a", "m-a")
	p := Provider{ID: "pool", Name: "Pool", Chat: srv.URL + "/v1",
		Account: &Account{Agent: "pool", User: "first"},
		Models:  []string{"m-a", "m-b"},
		AccountModels: map[string][]string{
			"first": {"m-a"},
		}}
	rs := p.TestModels(context.Background(), []string{"m-b"})
	if len(rs) != 1 || rs[0].OK || !strings.Contains(rs[0].Error, "no account") {
		t.Fatalf("m-b's test: %+v", rs)
	}
	if got := asked(); len(got) != 0 {
		t.Errorf("asked anyway: %v", got)
	}
}
