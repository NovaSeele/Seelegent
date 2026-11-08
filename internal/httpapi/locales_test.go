package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every middleware message exists in each language of the dashboard, and X-Seelegent-Lang reaches each of them
// (M18 review: the table had en and vi only).
func TestMessagesInEveryLocale(t *testing.T) {
	for id, tr := range messages {
		for _, l := range locales {
			if tr[l] == "" {
				t.Errorf("message %s has no %s text", id, l)
			}
		}
		if len(tr) != len(locales) {
			t.Errorf("message %s has %d languages, want %d", id, len(tr), len(locales))
		}
	}
	for header, want := range map[string]string{
		"": "en", "en-US": "en", "es-ES,es;q=0.9": "es", "ja": "ja", "ko-KR": "ko", "ru": "ru", "vi-VN": "vi",
		"zh-CN": "zh-CN", "zh-Hans": "zh-CN", "zh": "zh-CN", "fr-FR, ja;q=0.5": "ja", "de": "en",
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.Header.Set("X-Seelegent-Lang", header)
		if got := language(r); got != want {
			t.Errorf("X-Seelegent-Lang %q -> %s, want %s", header, got, want)
		}
	}
	// The browser's own language never picks one: English unless the dashboard says otherwise.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("Accept-Language", "vi-VN,vi;q=0.9")
	if got := language(r); got != "en" {
		t.Errorf("Accept-Language vi-VN alone -> %s, want en", got)
	}
}
