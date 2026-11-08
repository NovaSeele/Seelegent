package github

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestGitEnv: the token goes to git only as a header for the client's own host and scheme (D18), never for ssh,
// another host, plain http to a non-loopback host, or a tier without the token.
func TestGitEnv(t *testing.T) {
	const tok = "ghp_GitEnvTestToken0123456789abcdefABCD"
	ghe, ok := NewHosts([]string{"ghe.example.com"}).ByName("ghe.example.com")
	if !ok {
		t.Fatal("ghe.example.com is not a host")
	}
	loop, ok := NewHosts([]string{"http://127.0.0.1:9000"}).ByName("127.0.0.1:9000")
	if !ok {
		t.Fatal("loopback host missing")
	}
	client := func(tier string, h Host) *Client {
		return NewClient(ClientOptions{Access: Access{Tier: tier}, Host: h, Owner: "o", Repo: "r", Token: tok})
	}
	header := "GIT_CONFIG_VALUE_1=Authorization: Basic " +
		base64.StdEncoding.EncodeToString([]byte("x-access-token:"+tok))

	env := client(api.TierToken, DotCom).GitEnv("https://github.com/o/r.git")
	if !slices.Equal(env, []string{"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=http.https://github.com/.extraheader", header}) {
		t.Fatalf("github.com env = %q", env)
	}
	if env := client(api.TierToken, loop).GitEnv("http://127.0.0.1:9000/o/r.git"); len(env) != 5 ||
		!strings.HasSuffix(env[1], "http.http://127.0.0.1:9000/.extraheader") {
		t.Fatalf("loopback env = %q", env)
	}
	none := map[string]struct {
		c      *Client
		target string
	}{
		"gh tier":           {client(api.TierGh, DotCom), "https://github.com/o/r.git"},
		"manual tier":       {client(api.TierManual, DotCom), "https://github.com/o/r.git"},
		"ssh":               {client(api.TierToken, DotCom), "git@github.com:o/r.git"},
		"other host":        {client(api.TierToken, DotCom), "https://gitlab.com/o/r.git"},
		"look-alike host":   {client(api.TierToken, DotCom), "https://github.com.evil.example/o/r.git"},
		"plain http":        {client(api.TierToken, DotCom), "http://github.com/o/r.git"},
		"enterprise http":   {client(api.TierToken, ghe), "http://ghe.example.com/o/r.git"},
		"other port":        {client(api.TierToken, loop), "http://127.0.0.1:9001/o/r.git"},
		"origin name":       {client(api.TierToken, DotCom), "origin"},
		"no token":          {NewClient(ClientOptions{Access: Access{Tier: api.TierToken}, Host: DotCom}), "https://github.com/o/r"},
		"enterprise to com": {client(api.TierToken, ghe), "https://github.com/o/r.git"},
	}
	for name, c := range none {
		if env := c.c.GitEnv(c.target); env != nil {
			t.Errorf("%s: env = %q, want none", name, env)
		}
	}
}
