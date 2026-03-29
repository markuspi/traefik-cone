package traefik_cone_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/markuspi/traefik-cone"
	"github.com/traefik/genconf/dynamic"
)

func TestProviderGeneratesConfigurationAndUpdatesAllowlist(t *testing.T) {
	config := traefik_cone.CreateConfig()
	// keep expiration short to avoid long-lived state during tests
	config.Expiration = "1s"

	provider, err := traefik_cone.New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := provider.Stop(); err != nil {
			t.Fatal(err)
		}
	})

	if err := provider.Init(); err != nil {
		t.Fatal(err)
	}

	cfgChan := make(chan json.Marshaler)
	if err := provider.Provide(cfgChan); err != nil {
		t.Fatal(err)
	}

	// Receive the initial configuration
	var payload1 *dynamic.JSONPayload
	select {
	case m := <-cfgChan:
		var ok bool
		payload1, ok = m.(*dynamic.JSONPayload)
		if !ok {
			t.Fatalf("unexpected payload type: %T", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial configuration")
	}

	cfg1 := payload1.Configuration
	if cfg1 == nil || cfg1.HTTP == nil {
		t.Fatalf("missing HTTP configuration: %#v", cfg1)
	}

	svc, ok := cfg1.HTTP.Services["service"]
	if !ok || svc == nil || svc.LoadBalancer == nil || len(svc.LoadBalancer.Servers) == 0 {
		t.Fatalf("service 'service' not found or malformed: %#v", cfg1.HTTP.Services)
	}

	// Ensure the service points to a localhost URL (server address assigned at runtime)
	srvURL := svc.LoadBalancer.Servers[0].URL
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("invalid service URL: %v", err)
	}
	if u.Scheme != "http" {
		t.Fatalf("unexpected scheme for service URL: %s", u.Scheme)
	}

	// Initial allowlist should contain 127.0.0.1
	mw, ok := cfg1.HTTP.Middlewares["middleware"]
	if !ok || mw == nil || mw.IPWhiteList == nil {
		t.Fatalf("middleware 'middleware' missing or malformed: %#v", cfg1.HTTP.Middlewares)
	}
	if !contains(mw.IPWhiteList.SourceRange, "127.0.0.1") {
		t.Fatalf("expected initial allowlist to contain 127.0.0.1, got %v", mw.IPWhiteList.SourceRange)
	}

	// Make an HTTP request to the provider's server to add a new IP via X-Real-IP
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", srvURL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	newIP := "1.2.3.4"
	req.Header.Set("X-Real-IP", newIP)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request to provider server failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status from provider server: %d", resp.StatusCode)
	}

	// Expect a new configuration to be emitted containing the new IP
	var payload2 *dynamic.JSONPayload
	select {
	case m := <-cfgChan:
		var ok bool
		payload2, ok = m.(*dynamic.JSONPayload)
		if !ok {
			t.Fatalf("unexpected payload type: %T", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for updated configuration")
	}

	cfg2 := payload2.Configuration
	if cfg2 == nil || cfg2.HTTP == nil {
		t.Fatalf("missing HTTP configuration in updated payload: %#v", cfg2)
	}

	mw2, ok := cfg2.HTTP.Middlewares["middleware"]
	if !ok || mw2 == nil || mw2.IPWhiteList == nil {
		t.Fatalf("middleware 'middleware' missing or malformed in updated config: %#v", cfg2.HTTP.Middlewares)
	}

	if !contains(mw2.IPWhiteList.SourceRange, newIP) {
		t.Fatalf("expected updated allowlist to contain %s, got %v", newIP, mw2.IPWhiteList.SourceRange)
	}
}

func TestProviderPersistsAllowlistToFile(t *testing.T) {
	config := traefik_cone.CreateConfig()
	config.Expiration = "1s"
	f, err := os.CreateTemp("", "traefik-cone-*.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer os.Remove(f.Name())
	config.PersistenceFilepath = f.Name()

	provider, err := traefik_cone.New(context.Background(), config, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Stop()

	if err := provider.Init(); err != nil {
		t.Fatal(err)
	}

	cfgChan := make(chan json.Marshaler)
	if err := provider.Provide(cfgChan); err != nil {
		t.Fatal(err)
	}

	// wait initial config and then add IP
	var payload *dynamic.JSONPayload
	select {
	case m := <-cfgChan:
		payload = m.(*dynamic.JSONPayload)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial configuration")
	}

	newIP := "4.5.6.7"
	req, err := http.NewRequest("GET", payload.Configuration.HTTP.Services["service"].LoadBalancer.Servers[0].URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Real-IP", newIP)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// wait for update
	select {
	case <-cfgChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for updated configuration")
	}

	// verify file includes new IP
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), newIP) {
		t.Fatalf("expected persisted file to contain %s, got %s", newIP, string(data))
	}
}

func contains(slice []string, v string) bool {
	for _, s := range slice {
		if s == v {
			return true
		}
	}
	return false
}
