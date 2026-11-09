package backend

import (
	"regexp"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/event"
)

// rule maps error text to a class. A backend's rules are tried in order and the first match wins.
type rule struct {
	class event.Class
	re    *regexp.Regexp
}

// tailLines is how many of the last stderr lines classification looks at when the CLI gave no error text. The old
// version searched 4000 characters, which caught the worker's own test output (bug B8).
const tailLines = 5

// classifyText returns the class of the first rule that matches text.
func classifyText(rules []rule, text string) (event.Class, bool) {
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	for _, r := range rules {
		if r.re.MatchString(text) {
			return r.class, true
		}
	}
	return "", false
}

// onlyTransient reports whether text matches a transient rule and no rule of any other class: every rule is tried,
// not only up to the first match, so an error that also reads as quota, sign-in or credits never counts as transient.
func onlyTransient(rules []rule, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	found := false
	for _, r := range rules {
		if r.re.MatchString(text) {
			if r.class != event.ClassTransient {
				return false
			}
			found = true
		}
	}
	return found
}

// lastLines returns the last n non-empty lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			out = append(out, l)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return strings.Join(out, "\n")
}

// Shared pattern pieces. HTTP status codes only count next to a word that makes them a status ("status 429",
// "HTTP 503", "429 Too Many Requests"), never bare: a bare number matches line numbers, ports and test output.
const (
	reStatus5xx = `\b(status|code|http|error)[ :=]*5(00|02|03|04)\b|\b5(00|02|03|04) (internal server error|bad gateway|service unavailable|gateway timeout)\b`
	// reNetwork: a connection that failed or dropped, as Node (claude), Rust (codex), Go (agy) and Windows word it
	// ("API Error: Connection error.", "fetch failed", "getaddrinfo ENOTFOUND", "stream disconnected before
	// completion: error sending request for url", "dns error: failed to lookup address information", "wsarecv").
	// Never a bare "timed out" or "terminated": tests and tools print those.
	reNetwork = `ECONNRESET|ETIMEDOUT|ECONNREFUSED|socket hang up|DEADLINE_EXCEEDED|unexpected EOF|` +
		`stream (error|closed|reset)|connection (reset|closed|refused)|request failed|` +
		`connection error|request timed out|operation timed out|connection timed out|fetch failed|` +
		`\bENOTFOUND\b|\bEAI_AGAIN\b|\bENETUNREACH\b|\bEHOSTUNREACH\b|\bECONNABORTED\b|\bEPIPE\b|\bUND_ERR_\w+|` +
		`other side closed|stream disconnected|error sending request|dns error|failed to lookup address|` +
		`tcp connect error|connection closed before message completed|\bwsarecv\b|\bwsasend\b|no such host|` +
		`network is unreachable`
	re401 = `\b401\b.{0,40}(unauthori[sz]ed|auth)|(unauthori[sz]ed|auth\w*).{0,40}\b401\b`
	re403 = `\b403\b.{0,20}forbidden|forbidden.{0,20}\b403\b`
	re429 = `\b(status|code|http|error)[ :=]*429\b|\b429 too many requests\b|too many requests`
)

// newRule compiles a case-insensitive rule.
func newRule(class event.Class, pattern string) rule {
	return rule{class: class, re: regexp.MustCompile("(?i)" + pattern)}
}

// agyRules: quota, then auth, then transient (old agy.js L279-289, tightened for B8: no bare "sign in", "429",
// "quota" or "limit reached").
func agyRules() []rule {
	return []rule{
		newRule(event.ClassQuotaExhausted, `RESOURCE_EXHAUSTED|quota (exceeded|exhausted|reached)|exceeded (your |the )?(current )?quota|out of quota|rate[ -]?limit(ed| exceeded| reached)|`+re429+`|credits balance is too low|out of credits|usage limit reached`),
		newRule(event.ClassAuthRequired, `not logged in|not signed in|UNAUTHENTICATED|authentication (expired|failed|required)|log back in|(please|must|need to) (sign|log) ?in|sign in again|verify your account|invalid_grant|`+re401),
		newRule(event.ClassTransient, reNetwork+`|\bINTERNAL\b|\bUNAVAILABLE\b|`+reStatus5xx),
	}
}

// reClaudeLimit is Claude Code's wording when a sign-in account reaches one of its limits: "You've hit your 5-hour
// limit · resets 3:10am (Asia/Ho_Chi_Minh)", "Weekly limit reached", "Claude AI usage limit reached|1762459200".
// Each form names the limit and says hit, reached or resets, so "time limit" or "rate limiter" in other text does not
// match.
const reClaudeLimit = `\bhit your (?:(?:5-hour|weekly|session|opus|sonnet|usage) )?limit\b|` +
	`\b(?:5-hour|weekly|opus|sonnet) limit reached\b|\blimit [·\-–] resets\b|\bclaude ai usage limit reached\b`

// reCredits is a key's credit balance or billing running out ("Credit balance is too low", OpenAI's
// insufficient_quota "... check your plan and billing details", "402 Payment Required"): credits, not a quota
// limit, since no time ends it. agy keeps its "out of credits" as quota: a Google plan's credits refill. Providers
// behind an API key word it their own way: OpenRouter "API Error: 402 {... Insufficient credits ...}", DeepSeek
// "API Error: 402 {... Insufficient Balance ...}".
const reCredits = `credits? balance|out of credits|insufficient_quota|insufficient (credits|balance|funds)|billing|` +
	`\b402\b.{0,30}payment|\b(status|code|http|error)[ :=]*402\b`

// claudeRules: auth, then credits, then transient (overloaded counts as transient), then quota (old claude.js
// L416-433). Credits come before transient: a 402 body may also mention a server error.
func claudeRules() []rule {
	return []rule{
		newRule(event.ClassAuthRequired, `invalid\s*(x-)?api[- ]?key|not logged in|please run /login|run /login|authentication_error|oauth token (has )?expired|verify your account|log back in|permission_error|`+re401+`|`+re403),
		newRule(event.ClassCredits, reCredits),
		newRule(event.ClassTransient, reNetwork+`|overloaded_error|\boverloaded\b|\b529\b|api_error|internal server error|`+reStatus5xx),
		newRule(event.ClassQuotaExhausted, reClaudeLimit+`|rate_limit_error|rate[ -]?limit(ed| exceeded| reached)|usage limit|exceeded your current quota|`+re429),
	}
}

// codexRules: credits, then quota, then auth, then transient (old codex.js L643-657, without the bare
// "unauthorized" and "forbidden" of B8).
func codexRules() []rule {
	return []rule{
		newRule(event.ClassCredits, reCredits),
		newRule(event.ClassQuotaExhausted, `RESOURCE_EXHAUSTED|usage[ _]limit|rate[ -]?limit(ed| exceeded| reached)|`+re429+`|capacity exceeded|exceeded your current quota`),
		newRule(event.ClassAuthRequired, `invalid\s*(x-)?(api\s*key|token)|not logged in|please log ?in|codex login|authentication (failed|required|error)|token (has )?expired|refresh token|verify your account|`+re401+`|`+re403),
		newRule(event.ClassTransient, reNetwork+`|\bINTERNAL\b|\bUNAVAILABLE\b|`+reStatus5xx),
	}
}
