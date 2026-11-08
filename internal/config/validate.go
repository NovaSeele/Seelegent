package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// ErrInvalid is matched (errors.Is) by every error about settings values that are out of range or inconsistent.
// errors.As with *ValidationError gives the list of fields.
var ErrInvalid = errors.New("invalid settings")

// ErrBadPatch is returned by MergePatch for a patch that is not a JSON object. It is about the request, not the
// config file: ErrParse ("config file cannot be parsed") named the wrong culprit.
var ErrBadPatch = errors.New("the settings patch must be a JSON object")

// Port range accepted for the daemon.
const (
	minPort = 1
	maxPort = 65535
)

// FieldError describes one invalid setting.
type FieldError struct {
	// Field is the JSON path of the setting, for example "backends.codex.sandbox".
	Field string
	// Message says what is wrong, in words a user can act on.
	Message string
}

// Error implements error.
func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// ValidationError lists every invalid setting found in one check. It matches ErrInvalid.
type ValidationError struct {
	// Fields holds one entry per problem, in the order of the settings.
	Fields []FieldError
}

// Error implements error.
func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Error()
	}
	return ErrInvalid.Error() + ": " + strings.Join(parts, "; ")
}

// Is makes errors.Is(err, ErrInvalid) true for a *ValidationError.
func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }

// validator collects field errors.
type validator struct{ errs []FieldError }

func (v *validator) add(field, format string, args ...any) {
	v.errs = append(v.errs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) intRange(field string, got, lo, hi int) {
	if got < lo || got > hi {
		v.add(field, "must be between %d and %d (got %d)", lo, hi, got)
	}
}

func (v *validator) oneOf(field, got string, allowed ...string) {
	if !slices.Contains(allowed, got) {
		v.add(field, "must be one of %s (got %q)", strings.Join(allowed, ", "), got)
	}
}

func (v *validator) err() error {
	if len(v.errs) == 0 {
		return nil
	}
	return &ValidationError{Fields: v.errs}
}

// Upper bounds that catch typos (minutes typed as seconds, an extra zero) without restricting real use.
const (
	maxMinutes = 7 * 24 * 60
	maxHours   = 365 * 24
	maxDays    = 3650
)

// Validate checks every setting and returns nil or a *ValidationError (matching ErrInvalid) that lists all
// problems at once.
func (s *Settings) Validate() error {
	v := &validator{}
	v.intRange("port", s.Port, minPort, maxPort)
	v.oneOf("defaultTier", s.DefaultTier, Tiers()...)
	validateBackend(v, "backends.agy", s.Backends.Agy)
	validateBackend(v, "backends.codex", s.Backends.Codex.Backend)
	v.oneOf("backends.codex.sandbox", s.Backends.Codex.Sandbox, SandboxReadOnly, SandboxWorkspaceWrite, SandboxFullAccess)
	validateBackend(v, "backends.claude", s.Backends.Claude)

	v.intRange("maxConcurrentPerAccount", s.MaxConcurrentPerAccount, 1, 64)
	v.intRange("quotaRefreshMinutes", s.QuotaRefreshMinutes, 1, maxMinutes)
	if s.MinRemaining < 0 || s.MinRemaining >= 1 {
		v.add("minRemaining", "must be a fraction from 0 up to (not including) 1 (got %g)", s.MinRemaining)
	}
	v.intRange("passiveCooldownMinutes", s.PassiveCooldownMinutes, 1, maxMinutes)
	v.intRange("taskTimeoutMinutes", s.TaskTimeoutMinutes, 1, maxMinutes)
	validateLinks(v, s.LinkIntoProfiles)

	v.intRange("stallMinutes", s.StallMinutes, 1, maxMinutes)
	v.intRange("stallToolMinutes", s.StallToolMinutes, 1, maxMinutes)
	v.intRange("loopRepeat", s.LoopRepeat, LoopRepeatMin, LoopRepeatMax)
	v.intRange("attentionDecayHours", s.AttentionDecayHours, 1, maxHours)

	v.intRange("gateTimeoutSec", s.GateTimeoutSec, 1, 24*60*60)
	v.intRange("gateMaxAutoFixes", s.GateMaxAutoFixes, 0, 10)
	v.intRange("gateSettleMinutes", s.GateSettleMinutes, 0, maxMinutes)
	v.intRange("gateSettlePollMs", s.GateSettlePollMs, 100, 60*60*1000)
	v.intRange("gateOutputTailBytes", s.GateOutputTailBytes, 1, 1<<20)
	v.oneOf("lanes", s.Lanes, LanesOff, LanesWarn, LanesEnforce)
	v.intRange("laneHoldHours", s.LaneHoldHours, 0, maxHours)
	v.oneOf("verifierTier", s.VerifierTier, Tiers()...)

	v.intRange("snapshotRetentionDays", s.SnapshotRetentionDays, 1, maxDays)
	v.intRange("contentionWindowHours", s.ContentionWindowHours, 1, maxHours)
	v.intRange("githubPollMinutes", s.GithubPollMinutes, 0, maxMinutes)
	validateGitHubHosts(v, s.GithubEnterpriseHosts)

	v.oneOf("workerPriority", s.WorkerPriority, PriorityLow, PriorityNormal)
	v.oneOf("gatePriority", s.GatePriority, PriorityLow, PriorityNormal)
	v.intRange("maxConcurrentWorkers", s.MaxConcurrentWorkers, 0, MaxConcurrentWorkersMax)
	v.intRange("gateConcurrency", s.GateConcurrency, 0, GateConcurrencyMax)
	v.intRange("minFreeMemoryMB", s.MinFreeMemoryMB, 0, MemoryMBMax)
	v.intRange("minFreeCommitMB", s.MinFreeCommitMB, 0, MemoryMBMax)
	v.intRange("criticalFreeMemoryMB", s.CriticalFreeMemoryMB, 0, MemoryMBMax)
	if s.CriticalFreeMemoryMB > 0 && s.MinFreeMemoryMB > 0 && s.CriticalFreeMemoryMB >= s.MinFreeMemoryMB {
		v.add("criticalFreeMemoryMB", "must be below minFreeMemoryMB (%d) while both are on (got %d)",
			s.MinFreeMemoryMB, s.CriticalFreeMemoryMB)
	}
	v.intRange("workerMemoryLimitMB", s.WorkerMemoryLimitMB, 0, MemoryMBMax)
	v.intRange("workerCPURatePercent", s.WorkerCPURatePercent, 0, CPURatePercentMax)
	v.intRange("testParallelism", s.TestParallelism, 0, TestParallelismMax)
	v.intRange("daemonMemoryLimitMB", s.DaemonMemoryLimitMB, 0, DaemonMemoryLimitMBMax)
	return v.err()
}

// validateBackend checks the allowed-models pattern and that every tier model is a plain id the pattern allows.
func validateBackend(v *validator, field string, b Backend) {
	var allowed *regexp.Regexp
	if b.AllowedModels != "" {
		re, err := regexp.Compile("(?i)" + b.AllowedModels)
		if err != nil {
			v.add(field+".allowedModels", "is not a valid regular expression: %v", err)
		} else {
			allowed = re
		}
	}
	for _, tier := range Tiers() {
		model, ok := b.Tiers.Model(tier)
		if !ok {
			continue
		}
		f := field + ".tiers." + tier
		switch {
		case strings.IndexFunc(model, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
			v.add(f, "model id must not contain spaces or control characters (got %q)", model)
		case allowed != nil && !allowed.MatchString(model):
			v.add(f, "model %q does not match allowedModels %q", model, b.AllowedModels)
		}
	}
}

// validateLinks checks that every entry names something inside the home directory.
func validateLinks(v *validator, links []string) {
	seen := make(map[string]bool, len(links))
	for i, l := range links {
		f := fmt.Sprintf("linkIntoProfiles[%d]", i)
		clean := filepath.Clean(filepath.FromSlash(l))
		switch {
		case strings.TrimSpace(l) == "":
			v.add(f, "must not be empty")
		case filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || strings.HasPrefix(l, "/") || strings.HasPrefix(l, `\`):
			v.add(f, "must be relative to the home directory (got %q)", l)
		case clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)):
			v.add(f, "must stay inside the home directory (got %q)", l)
		case seen[clean]:
			v.add(f, "is listed twice (%q)", l)
		}
		seen[clean] = true
	}
}

// maxGitHubHosts caps githubEnterpriseHosts.
const maxGitHubHosts = 20

// validateGitHubHosts checks the GitHub Enterprise hosts: a host name with an optional port, or an http(s) URL
// with nothing but a scheme and a host; plain http only for a loopback address.
func validateGitHubHosts(v *validator, hosts []string) {
	if len(hosts) > maxGitHubHosts {
		v.add("githubEnterpriseHosts", "lists more than %d hosts", maxGitHubHosts)
	}
	seen := map[string]bool{}
	for i, h := range hosts {
		f := fmt.Sprintf("githubEnterpriseHosts[%d]", i)
		norm, err := NormalizeGitHubHost(h)
		switch {
		case err != nil:
			v.add(f, "%v (got %q)", err, h)
		case norm.Host == "github.com":
			v.add(f, "github.com is always a GitHub host; list only enterprise hosts")
		case seen[norm.Host]:
			v.add(f, "is listed twice (%q)", h)
		}
		seen[norm.Host] = true
	}
}

// GitHubHost is a normalized entry of githubEnterpriseHosts.
type GitHubHost struct {
	// Host is the lower-case host name with its port, if any ("ghe.example.com", "127.0.0.1:8080").
	Host string
	// Scheme is https, or http for a loopback test server.
	Scheme string
}

// NormalizeGitHubHost reads one entry of githubEnterpriseHosts.
func NormalizeGitHubHost(entry string) (GitHubHost, error) {
	e := strings.TrimSpace(entry)
	if e == "" {
		return GitHubHost{}, errors.New("must not be empty")
	}
	if strings.IndexFunc(e, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return GitHubHost{}, errors.New("must not contain spaces")
	}
	scheme := "https"
	if strings.Contains(e, "://") {
		u, err := url.Parse(e)
		if err != nil {
			return GitHubHost{}, errors.New("is not a valid URL")
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return GitHubHost{}, errors.New("must be an https URL or a host name")
		}
		if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return GitHubHost{}, errors.New("must hold only a scheme and a host")
		}
		scheme, e = u.Scheme, u.Host
	}
	h := strings.ToLower(e)
	name, port := h, ""
	if host, p, err := net.SplitHostPort(h); err == nil {
		name, port = host, p
	}
	if name == "" || strings.ContainsAny(name, "/@?#\\") {
		return GitHubHost{}, errors.New("is not a host name")
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return GitHubHost{}, errors.New("has an invalid port")
		}
	}
	if scheme == "http" && !isLoopback(name) {
		return GitHubHost{}, errors.New("plain http is allowed only for a loopback address")
	}
	return GitHubHost{Host: h, Scheme: scheme}, nil
}

// isLoopback reports whether name is localhost or a loopback IP address.
func isLoopback(name string) bool {
	if name == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(name, "[]"))
	return ip != nil && ip.IsLoopback()
}
