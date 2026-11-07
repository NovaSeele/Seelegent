package httpapi

import "testing"

// TestDevOrigins covers Options.DevOrigins: only loopback http origins with a port are accepted, and an accepted
// one passes both the Host check and the same-origin check, while everything else still fails them.
func TestDevOrigins(t *testing.T) {
	for _, bad := range []string{"https://localhost:5173", "http://localhost", "http://example.com:5173",
		"http://10.0.0.1:5173", "http://localhost:5173/app", "http://u@localhost:5173", "http://localhost:0",
		"http://localhost:99999", "localhost:5173", ""} {
		if _, _, err := parseDevOrigins([]string{bad}); err == nil {
			t.Errorf("dev origin %q accepted", bad)
		}
	}
	hosts, origins, err := parseDevOrigins([]string{"http://LocalHost:5173/", "http://[::1]:5174"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{o: Options{Port: 47831}, devHosts: hosts, devOrigins: origins}
	for host, want := range map[string]bool{"localhost:5173": true, "[::1]:5174": true, "127.0.0.1:47831": true,
		"127.0.0.1:5173": false, "localhost:5174": false, "evil.test:5173": false} {
		if got := s.hostAllowed(host); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
	for origin, want := range map[string]bool{"http://localhost:5173": true, "http://[::1]:5174": true,
		"http://localhost:47831": true, "http://127.0.0.1:5173": false, "https://localhost:5173": false} {
		if got := s.ownOrigin(origin); got != want {
			t.Errorf("ownOrigin(%q) = %v, want %v", origin, got, want)
		}
	}

	// Without dev origins nothing extra passes.
	none, noneO, err := parseDevOrigins(nil)
	if err != nil {
		t.Fatal(err)
	}
	plain := &Server{o: Options{Port: 47831}, devHosts: none, devOrigins: noneO}
	if plain.hostAllowed("localhost:5173") || plain.ownOrigin("http://localhost:5173") {
		t.Fatal("the dev server passes without --dev-origin")
	}
}
