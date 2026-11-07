package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAndCompareVersions(t *testing.T) {
	order := []string{"v0.0.1", "v0.1.0-alpha", "v0.1.0-alpha.1", "v0.1.0-alpha.beta", "v0.1.0-beta.2",
		"v0.1.0-beta.11", "v0.1.0-rc.1", "0.1.0", "v0.1.1", "v1.0.0", "v1.10.0"}
	for i := range order {
		for j := range order {
			a, err := ParseVersion(order[i])
			if err != nil {
				t.Fatal(err)
			}
			b, _ := ParseVersion(order[j])
			if got, want := a.Compare(b), cmpInt(i, j); got != want {
				t.Errorf("%s vs %s = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "dev", "v1", "v1.2", "v01.2.3", "1.2.3.4", "v1.2.3-"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) passed", bad)
		}
	}
	if v, _ := ParseVersion("1.2.3-rc.1+build.5"); v.String() != "v1.2.3-rc.1" {
		t.Errorf("String = %s", v)
	}
}

func TestReleaseVersion(t *testing.T) {
	for s, want := range map[string]bool{
		"v1.2.3": true, "v1.2.3-rc.1": true, "1.0.0": true,
		"dev": false, "": false, "v1.2.3-4-gabcdef1": false, "v1.2.3-rc.1-4-gabcdef1": false,
		"v1.2.3-dirty": false, "v1.2.3-4-gabcdef1-dirty": false, "v0.0.2-SNAPSHOT-abc1234": false,
	} {
		if _, got := releaseVersion(s); got != want {
			t.Errorf("releaseVersion(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestMetadataURL(t *testing.T) {
	for _, c := range []struct{ override, tag, want string }{
		{"", "", LatestURL},
		{"", "v1.2.3", "https://api.github.com/repos/" + Repo + "/releases/tags/v1.2.3"},
		{"http://127.0.0.1:9/releases/latest/", "v0.0.1", "http://127.0.0.1:9/releases/tags/v0.0.1"},
		{"http://127.0.0.1:9/my.json", "v0.0.1", "http://127.0.0.1:9/my.json"},
	} {
		if got := MetadataURL(c.override, c.tag); got != c.want {
			t.Errorf("MetadataURL(%q, %q) = %q, want %q", c.override, c.tag, got, c.want)
		}
	}
	if tag, err := NormalizeTag("1.2.3"); err != nil || tag != "v1.2.3" {
		t.Errorf("NormalizeTag = %q, %v", tag, err)
	}
}

func TestCheckURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://github.com/x": true, "http://127.0.0.1:8080/x": true, "http://localhost/x": true,
		"http://[::1]:9/x": true, "http://example.com/x": false, "ftp://127.0.0.1/x": false,
		"file:///etc/passwd": false,
	} {
		if err := checkURL(raw); (err == nil) != ok {
			t.Errorf("checkURL(%q) err = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestRedirectToPlainHTTPRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/evil", http.StatusFound)
	}))
	defer srv.Close()
	u := New(Options{Current: "v1.0.0", MetadataURL: srv.URL + "/releases/latest"})
	_, err := u.Check(context.Background(), "")
	if !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("err = %v, want ErrInsecureURL", err)
	}
	u = New(Options{Current: "v1.0.0", MetadataURL: "http://example.com/releases/latest"})
	if _, err := u.Check(context.Background(), ""); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("plain http err = %v", err)
	}
}

func TestCheckWithVersion(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.2", "v0.0.1", "v0.0.2")
	u := e.updater(t, "v0.0.2")
	res, err := u.Check(context.Background(), "v0.0.1")
	if err != nil || !res.UpdateAvailable || res.LatestVersion != "v0.0.1" {
		t.Fatalf("Check(v0.0.1) = %+v, %v", res, err)
	}
	res, err = u.Check(context.Background(), "")
	if err != nil || res.UpdateAvailable || !strings.Contains(res.Message, "up to date") {
		t.Fatalf("Check = %+v, %v", res, err)
	}
	// An override that is not a /releases/latest address must name the asked version.
	u.o.MetadataURL = e.server.URL + "/releases/tags/v0.0.2"
	if _, err := u.Check(context.Background(), "v0.0.1"); err == nil || !strings.Contains(err.Error(), "not v0.0.1") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsBinaryEntry(t *testing.T) {
	for name, want := range map[string]bool{
		"seelegent": true, "./seelegent": true, "seelegent_1.0.0/seelegent": true, "a/b/seelegent": false,
		"../seelegent": false, "/seelegent": false, "seelegent.exe": false, "x/../seelegent": false,
	} {
		if got := isBinaryEntry(name, "seelegent"); got != want {
			t.Errorf("isBinaryEntry(%q) = %v, want %v", name, got, want)
		}
	}
}
