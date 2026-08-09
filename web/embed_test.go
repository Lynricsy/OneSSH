package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTMLRoutesDenyFraming(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/", "/login", "/oauth/authorize"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("状态码 = %d", response.Code)
			}
			if got := response.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
				t.Fatalf("Content-Security-Policy = %q", got)
			}
			if got := response.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Fatalf("X-Frame-Options = %q", got)
			}
		})
	}
}
