package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddleware_ValidBearerToken(t *testing.T) {
	jm := newTestJWTManager(t)
	pair, _ := jm.IssueTokenPair("user1")

	handler := Middleware(jm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			t.Fatal("claims should be in context")
		}
		if claims.Subject != "user1" {
			t.Errorf("subject = %q, want %q", claims.Subject, "user1")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestMiddleware_ValidCookie(t *testing.T) {
	jm := newTestJWTManager(t)
	pair, _ := jm.IssueTokenPair("user1")

	handler := Middleware(jm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: pair.AccessToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestMiddleware_NoToken(t *testing.T) {
	jm := newTestJWTManager(t)

	handler := Middleware(jm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	jm := newTestJWTManager(t)

	handler := Middleware(jm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_RefreshTokenRejected(t *testing.T) {
	jm := newTestJWTManager(t)
	pair, _ := jm.IssueTokenPair("user1")

	handler := Middleware(jm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called with refresh token")
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+pair.RefreshToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestClaimsFromContext_NoAuth(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	claims := ClaimsFromContext(req.Context())
	if claims != nil {
		t.Error("claims should be nil without auth")
	}
}

func TestMiddleware_BearerPriorityOverCookie(t *testing.T) {
	jm := newTestJWTManager(t)
	pair, _ := jm.IssueTokenPair("user1")

	handler := Middleware(jm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims.Subject != "user1" {
			t.Errorf("subject = %q, want %q", claims.Subject, "user1")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: "invalid-cookie"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Bearer should take priority, status = %d", rr.Code)
	}
}
