package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/LimeOnTop/voting-service/internal/httpjson"
	"github.com/LimeOnTop/voting-service/internal/middleware"
)

func mustPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			t.Fatalf("parse %q: %v", cidr, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

// capture runs the middleware chain and returns whatever the handler observed.
func capture(handler http.Handler, r *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, r)
	return recorder
}

func TestRealIP(t *testing.T) {
	trusted := mustPrefixes(t, "10.0.0.0/8", "192.168.0.0/16")

	tests := map[string]struct {
		remoteAddr string
		forwarded  []string
		trust      []netip.Prefix
		want       string
	}{
		"no trusted proxies ignores the header": {
			remoteAddr: "203.0.113.9:5000",
			forwarded:  []string{"1.2.3.4"},
			trust:      nil,
			want:       "203.0.113.9",
		},
		"untrusted peer cannot spoof the header": {
			remoteAddr: "203.0.113.9:5000",
			forwarded:  []string{"1.2.3.4"},
			trust:      trusted,
			want:       "203.0.113.9",
		},
		"trusted peer yields the forwarded client": {
			remoteAddr: "10.0.0.5:5000",
			forwarded:  []string{"198.51.100.23"},
			trust:      trusted,
			want:       "198.51.100.23",
		},
		"rightmost untrusted hop wins": {
			// A client-injected value sits on the left; the real address is
			// the last untrusted hop appended by our own proxies.
			remoteAddr: "10.0.0.5:5000",
			forwarded:  []string{"1.2.3.4, 198.51.100.23, 10.0.0.9"},
			trust:      trusted,
			want:       "198.51.100.23",
		},
		"multiple header instances are joined": {
			remoteAddr: "10.0.0.5:5000",
			forwarded:  []string{"1.2.3.4", "198.51.100.23, 192.168.1.1"},
			trust:      trusted,
			want:       "198.51.100.23",
		},
		"malformed hop stops the walk": {
			remoteAddr: "10.0.0.5:5000",
			forwarded:  []string{"198.51.100.23, garbage"},
			trust:      trusted,
			want:       "10.0.0.5",
		},
		"all hops trusted falls back to the peer": {
			remoteAddr: "10.0.0.5:5000",
			forwarded:  []string{"10.0.0.7, 192.168.1.1"},
			trust:      trusted,
			want:       "10.0.0.5",
		},
		"ipv6 peer is normalised": {
			remoteAddr: "[2001:db8::1]:5000",
			trust:      trusted,
			want:       "2001:db8::1",
		},
		"unparseable peer is reported as unknown": {
			remoteAddr: "not-an-address",
			trust:      trusted,
			want:       "unknown",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var observed string
			handler := middleware.RealIP(test.trust)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				observed = httpjson.ClientIP(r.Context())
			}))

			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remoteAddr
			for _, value := range test.forwarded {
				request.Header.Add("X-Forwarded-For", value)
			}
			capture(handler, request)

			if observed != test.want {
				t.Fatalf("expected client ip %q, got %q", test.want, observed)
			}
		})
	}
}

func TestCorrelationIssuesAndEchoesRequestIDs(t *testing.T) {
	var observed string
	handler := middleware.Correlation(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = httpjson.RequestID(r.Context())
	}))

	t.Run("generates an id when absent", func(t *testing.T) {
		response := capture(handler, httptest.NewRequest(http.MethodGet, "/", nil))
		if observed == "" {
			t.Fatal("expected a generated request id")
		}
		if response.Header().Get("X-Request-Id") != observed {
			t.Fatal("the response must echo the request id")
		}
	})

	t.Run("echoes a safe caller id", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("X-Request-Id", "trace-abc.123")
		capture(handler, request)
		if observed != "trace-abc.123" {
			t.Fatalf("expected the caller id to be reused, got %q", observed)
		}
	})

	t.Run("rejects an unsafe caller id", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("X-Request-Id", "bad\r\nSet-Cookie: x=1")
		capture(handler, request)
		if observed == "bad\r\nSet-Cookie: x=1" {
			t.Fatal("a header-injecting request id must not be reflected")
		}
	})
}

func TestVoterCookie(t *testing.T) {
	cfg := middleware.VoterCookieConfig{
		Name:     "voter_id",
		TTL:      24 * time.Hour,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}

	var observed string
	handler := middleware.VoterCookie(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed = httpjson.VoterToken(r.Context())
	}))

	t.Run("issues a token when absent", func(t *testing.T) {
		response := capture(handler, httptest.NewRequest(http.MethodGet, "/", nil))
		if observed == "" {
			t.Fatal("expected a token to be issued")
		}

		cookies := response.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected exactly one cookie, got %d", len(cookies))
		}
		cookie := cookies[0]
		if cookie.Value != observed {
			t.Fatal("the issued cookie must carry the token used for deduplication")
		}
		if !cookie.HttpOnly || !cookie.Secure {
			t.Fatal("the voter cookie must be HttpOnly and Secure")
		}
	})

	t.Run("reuses a valid token", func(t *testing.T) {
		first := capture(handler, httptest.NewRequest(http.MethodGet, "/", nil))
		issued := first.Result().Cookies()[0]

		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.AddCookie(issued)
		response := capture(handler, request)

		if observed != issued.Value {
			t.Fatalf("expected the existing token to be reused, got %q", observed)
		}
		if len(response.Result().Cookies()) != 0 {
			t.Fatal("a request that already carries a valid token must not be re-issued one")
		}
	})

	t.Run("replaces a malformed token", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.AddCookie(&http.Cookie{Name: cfg.Name, Value: "this-is-not-a-valid-token-at-all"})
		capture(handler, request)

		if observed == "this-is-not-a-valid-token-at-all" {
			t.Fatal("a token that this service did not issue must be replaced")
		}
		if observed == "" {
			t.Fatal("expected a fresh token to be issued")
		}
	})
}
