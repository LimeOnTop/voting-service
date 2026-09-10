package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LimeOnTop/voting-service/internal/httpjson"
	"github.com/LimeOnTop/voting-service/internal/middleware"
)

const adminToken = "0d1a4c9f7b2e8a5c3d6f0b9e4a7c1d8f"

func adminHandler() http.Handler {
	return middleware.AdminAuth(adminToken)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestAdminAuth(t *testing.T) {
	tests := map[string]struct {
		authorization string
		wantStatus    int
	}{
		"valid token":       {"Bearer " + adminToken, http.StatusNoContent},
		"scheme any case":   {"bearer " + adminToken, http.StatusNoContent},
		"missing header":    {"", http.StatusUnauthorized},
		"wrong token":       {"Bearer wrong-token-value-of-right-length", http.StatusUnauthorized},
		"missing scheme":    {adminToken, http.StatusUnauthorized},
		"wrong scheme":      {"Basic " + adminToken, http.StatusUnauthorized},
		"token prefix only": {"Bearer " + adminToken[:16], http.StatusUnauthorized},
		"token with suffix": {"Bearer " + adminToken + "x", http.StatusUnauthorized},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin", nil)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := capture(adminHandler(), request)

			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d", test.wantStatus, response.Code)
			}
			if test.wantStatus == http.StatusUnauthorized {
				if strings.Contains(response.Body.String(), adminToken) {
					t.Fatal("the error body must never echo credentials")
				}
			}
		})
	}
}

func TestBodyLimitSurfacesAsPayloadTooLarge(t *testing.T) {
	const limit = 64

	handler := middleware.BodyLimit(limit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Value string `json:"value"`
		}
		if err := httpjson.DecodeJSON(r, &payload); err != nil {
			httpjson.WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("small body passes", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"ok"}`))
		if response := capture(handler, request); response.Code != http.StatusNoContent {
			t.Fatalf("expected the request to succeed, got %d", response.Code)
		}
	})

	t.Run("oversized body is rejected", func(t *testing.T) {
		oversized := `{"value":"` + strings.Repeat("x", limit*4) + `"}`
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(oversized))

		response := capture(handler, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", response.Code)
		}
	})
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Value string `json:"value"`
		}
		if err := httpjson.DecodeJSON(r, &payload); err != nil {
			httpjson.WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"ok","typo":1}`))
	if response := capture(handler, request); response.Code != http.StatusBadRequest {
		t.Fatalf("expected an unknown field to be rejected, got %d", response.Code)
	}
}
