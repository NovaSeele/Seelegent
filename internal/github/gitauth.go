package github

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// GitEnv is the environment that lets one git command reach target with the stored token (token tier, old bug
// D18): an Authorization header for the client's host only, given through GIT_CONFIG_COUNT/KEY/VALUE so the token
// is never in a command line or in a git config file, and only for the command that gets this environment. An empty
// first value drops any extra header the user's config sets for the same address. It returns nil when the tier is
// not token, there is no token, or target is not an http(s) URL of the client's host with the host's scheme (https,
// or http for a loopback test server): ssh remotes and every other host keep the user's own git credentials.
func (c *Client) GitEnv(target string) []string {
	if c.tier != api.TierToken || c.token == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || !strings.EqualFold(u.Scheme, c.host.Scheme) || !strings.EqualFold(u.Host, c.host.Name) {
		return nil
	}
	key := "http." + c.host.Scheme + "://" + strings.ToLower(c.host.Name) + "/.extraheader"
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + c.token))
	return []string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=" + key, "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=" + key, "GIT_CONFIG_VALUE_1=Authorization: Basic " + cred,
	}
}
