package security

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

const testBearerKeyID = "test-key"

// testIssuer is a minimal OIDC provider serving discovery and JWKS documents, and signing tokens locally.
type testIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &testIssuer{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                issuer.server.URL,
			"authorization_endpoint":                issuer.server.URL + "/auth",
			"token_endpoint":                        issuer.server.URL + "/token",
			"jwks_uri":                              issuer.server.URL + "/certs",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/certs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"kid": testBearerKeyID,
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})
	issuer.server = httptest.NewServer(mux)
	t.Cleanup(issuer.server.Close)
	return issuer
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": testBearerKeyID})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// validClaims returns the claims of a Keycloak-style client-credentials access token.
func (i *testIssuer) validClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":       i.server.URL,
		"sub":       "service-account-uuid",
		"aud":       []string{"gatus", "account"},
		"exp":       now.Add(5 * time.Minute).Unix(),
		"iat":       now.Unix(),
		"typ":       "Bearer",
		"azp":       "quin-gatus",
		"client_id": "quin-gatus",
		"realm_access": map[string]any{
			"roles": []string{"offline_access"},
		},
		"resource_access": map[string]any{
			"gatus": map[string]any{"roles": []string{"maintenance-writer"}},
		},
	}
}

func newBearerTestApp(t *testing.T, issuer *testIssuer, bearer *OIDCBearerConfig) (*Config, *fiber.App) {
	t.Helper()
	c := &Config{OIDC: &OIDCConfig{
		IssuerURL:    issuer.server.URL,
		RedirectURL:  "http://localhost:80/authorization-code/callback",
		ClientID:     "gatus",
		ClientSecret: "secret",
		Scopes:       []string{"openid"},
		Bearer:       bearer,
	}}
	if !c.ValidateAndSetDefaults() {
		t.Fatal("expected config to be valid")
	}
	app := fiber.New()
	if err := c.RegisterHandlers(app); err != nil {
		t.Fatal("expected no error, got", err)
	}
	protected := app.Group("/")
	if err := c.ApplySecurityMiddleware(protected); err != nil {
		t.Fatal("expected no error, got", err)
	}
	protected.Get("/test", func(ctx *fiber.Ctx) error {
		return ctx.SendStatus(200)
	})
	app.Get("/authenticated", func(ctx *fiber.Ctx) error {
		if c.IsAuthenticated(ctx) {
			return ctx.SendStatus(200)
		}
		return ctx.SendStatus(401)
	})
	return c, app
}

func doTestRequest(t *testing.T, app *fiber.App, path string, mutate func(*http.Request)) int {
	t.Helper()
	request := httptest.NewRequest("GET", path, http.NoBody)
	if mutate != nil {
		mutate(request)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatal("expected no error, got", err)
	}
	return response.StatusCode
}

func withBearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func TestOIDCBearerConfig_isValid(t *testing.T) {
	scenarios := []struct {
		name   string
		config *OIDCBearerConfig
		valid  bool
	}{
		{name: "nil", config: nil, valid: true},
		{name: "disabled-empty", config: &OIDCBearerConfig{}, valid: true},
		{name: "enabled-no-audience", config: &OIDCBearerConfig{Enabled: true, AllowedClients: []string{"quin-gatus"}}, valid: false},
		{name: "enabled-no-allow-rule", config: &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}}, valid: false},
		{name: "enabled-allowed-clients", config: &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, AllowedClients: []string{"quin-gatus"}}, valid: true},
		{name: "enabled-allowed-subjects", config: &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, AllowedSubjects: []string{"abc"}}, valid: true},
		{name: "enabled-required-role", config: &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, RequiredRole: "maintenance-writer"}, valid: true},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			if scenario.config.isValid() != scenario.valid {
				t.Errorf("expected valid=%v", scenario.valid)
			}
			oidcConfig := &OIDCConfig{
				IssuerURL:    "https://sso.example.com",
				RedirectURL:  "http://localhost:80/authorization-code/callback",
				ClientID:     "gatus",
				ClientSecret: "secret",
				Scopes:       []string{"openid"},
				Bearer:       scenario.config,
			}
			if oidcConfig.ValidateAndSetDefaults() != scenario.valid {
				t.Errorf("expected OIDCConfig valid=%v", scenario.valid)
			}
		})
	}
}

func TestConfig_ApplySecurityMiddleware_Bearer(t *testing.T) {
	issuer := newTestIssuer(t)
	_, app := newBearerTestApp(t, issuer, &OIDCBearerConfig{
		Enabled:        true,
		Audiences:      []string{"gatus"},
		AllowedClients: []string{"quin-gatus"},
	})
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	scenarios := []struct {
		name     string
		mutate   func(claims map[string]any)
		key      *rsa.PrivateKey
		expected int
	}{
		{name: "valid", expected: 200},
		{name: "valid-client-id-claim-only", mutate: func(c map[string]any) { delete(c, "azp") }, expected: 200},
		{name: "expired", mutate: func(c map[string]any) {
			c["exp"] = time.Now().Add(-time.Minute).Unix()
			c["iat"] = time.Now().Add(-10 * time.Minute).Unix()
		}, expected: 401},
		{name: "wrong-issuer", mutate: func(c map[string]any) { c["iss"] = "https://evil.example.com" }, expected: 401},
		{name: "wrong-audience", mutate: func(c map[string]any) { c["aud"] = "account" }, expected: 401},
		{name: "bad-signature", key: otherKey, expected: 401},
		{name: "client-not-allowed", mutate: func(c map[string]any) { c["azp"] = "other"; c["client_id"] = "other" }, expected: 401},
		{name: "id-token", mutate: func(c map[string]any) { c["typ"] = "ID" }, expected: 401},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			claims := issuer.validClaims()
			if scenario.mutate != nil {
				scenario.mutate(claims)
			}
			key := issuer.key
			if scenario.key != nil {
				key = scenario.key
			}
			token := signTestToken(t, key, claims)
			if code := doTestRequest(t, app, "/test", withBearer(token)); code != scenario.expected {
				t.Errorf("expected %d on protected route, got %d", scenario.expected, code)
			}
			if code := doTestRequest(t, app, "/authenticated", withBearer(token)); code != scenario.expected {
				t.Errorf("expected IsAuthenticated to return %d, got %d", scenario.expected, code)
			}
		})
	}
	t.Run("malformed-token", func(t *testing.T) {
		if code := doTestRequest(t, app, "/test", withBearer("not-a-jwt")); code != 401 {
			t.Errorf("expected 401, got %d", code)
		}
	})
	t.Run("no-credentials", func(t *testing.T) {
		if code := doTestRequest(t, app, "/test", nil); code != 401 {
			t.Errorf("expected 401, got %d", code)
		}
	})
}

func TestConfig_ApplySecurityMiddleware_BearerSubjectsAndRoles(t *testing.T) {
	issuer := newTestIssuer(t)
	scenarios := []struct {
		name     string
		bearer   *OIDCBearerConfig
		mutate   func(claims map[string]any)
		expected int
	}{
		{
			name:     "allowed-subject",
			bearer:   &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, AllowedSubjects: []string{"service-account-uuid"}},
			expected: 200,
		},
		{
			name:     "subject-not-allowed",
			bearer:   &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, AllowedSubjects: []string{"someone-else"}},
			expected: 401,
		},
		{
			name:     "client-role",
			bearer:   &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, RequiredRole: "maintenance-writer"},
			expected: 200,
		},
		{
			name:   "realm-role",
			bearer: &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, RequiredRole: "gatus-api"},
			mutate: func(c map[string]any) {
				c["realm_access"] = map[string]any{"roles": []string{"gatus-api"}}
			},
			expected: 200,
		},
		{
			name:     "role-missing",
			bearer:   &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, RequiredRole: "admin"},
			expected: 401,
		},
		{
			name:   "role-on-other-client-ignored",
			bearer: &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, RequiredRole: "maintenance-writer"},
			mutate: func(c map[string]any) {
				c["resource_access"] = map[string]any{"account": map[string]any{"roles": []string{"maintenance-writer"}}}
			},
			expected: 401,
		},
		{
			name:     "allowed-client-but-role-missing",
			bearer:   &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, AllowedClients: []string{"quin-gatus"}, RequiredRole: "admin"},
			expected: 401,
		},
		{
			name:     "allowed-client-and-role",
			bearer:   &OIDCBearerConfig{Enabled: true, Audiences: []string{"gatus"}, AllowedClients: []string{"quin-gatus"}, RequiredRole: "maintenance-writer"},
			expected: 200,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			_, app := newBearerTestApp(t, issuer, scenario.bearer)
			claims := issuer.validClaims()
			if scenario.mutate != nil {
				scenario.mutate(claims)
			}
			if code := doTestRequest(t, app, "/test", withBearer(signTestToken(t, issuer.key, claims))); code != scenario.expected {
				t.Errorf("expected %d, got %d", scenario.expected, code)
			}
		})
	}
}

func TestConfig_ApplySecurityMiddleware_BearerDisabled(t *testing.T) {
	issuer := newTestIssuer(t)
	for name, bearer := range map[string]*OIDCBearerConfig{
		"nil":      nil,
		"disabled": {Enabled: false, Audiences: []string{"gatus"}, AllowedClients: []string{"quin-gatus"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, app := newBearerTestApp(t, issuer, bearer)
			token := signTestToken(t, issuer.key, issuer.validClaims())
			if code := doTestRequest(t, app, "/test", withBearer(token)); code != 401 {
				t.Errorf("expected bearer header to be ignored (401), got %d", code)
			}
			if code := doTestRequest(t, app, "/authenticated", withBearer(token)); code != 401 {
				t.Errorf("expected IsAuthenticated to ignore bearer header, got %d", code)
			}
		})
	}
}

func TestConfig_ApplySecurityMiddleware_SessionCookieWithBearerEnabled(t *testing.T) {
	issuer := newTestIssuer(t)
	_, app := newBearerTestApp(t, issuer, &OIDCBearerConfig{
		Enabled:        true,
		Audiences:      []string{"gatus"},
		AllowedClients: []string{"quin-gatus"},
	})
	sessionID := "bearer-test-session"
	sessions.SetWithTTL(sessionID, "user@example.com", time.Minute)
	t.Cleanup(func() { sessions.Delete(sessionID) })
	withCookie := func(value string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieNameSession, Value: value}) }
	}
	if code := doTestRequest(t, app, "/test", withCookie(sessionID)); code != 200 {
		t.Errorf("expected valid session cookie to be accepted, got %d", code)
	}
	if code := doTestRequest(t, app, "/authenticated", withCookie(sessionID)); code != 200 {
		t.Errorf("expected IsAuthenticated to accept valid session cookie, got %d", code)
	}
	if code := doTestRequest(t, app, "/test", withCookie("unknown")); code != 401 {
		t.Errorf("expected unknown session cookie to be rejected, got %d", code)
	}
	// A bearer request must not create a session
	before := sessions.Count()
	if code := doTestRequest(t, app, "/test", withBearer(signTestToken(t, issuer.key, issuer.validClaims()))); code != 200 {
		t.Errorf("expected bearer token to be accepted, got %d", code)
	}
	if sessions.Count() != before {
		t.Error("expected no session to be created for bearer requests")
	}
}
