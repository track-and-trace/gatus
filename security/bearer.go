package security

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/TwiN/logr"
	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	errBearerNotConfigured   = errors.New("bearer token authentication is not configured")
	errBearerTokenType       = errors.New("token is not an access token")
	errBearerAudience        = errors.New("token audience is not accepted")
	errBearerIdentity        = errors.New("token subject/client is not allowed")
	errBearerRoleMissing     = errors.New("token does not carry the required role")
	errBearerClaimsMalformed = errors.New("token claims could not be parsed")
)

// OIDCBearerConfig configures acceptance of OAuth2 access tokens passed as "Authorization: Bearer <token>".
//
// Tokens must be issued by the same provider as security.oidc.issuer-url. Their signature, issuer and expiry
// are verified on every request; no session is created.
//
// A token is accepted only if:
//   - its audience contains at least one of Audiences, and
//   - when AllowedSubjects or AllowedClients is set, its `sub` is in AllowedSubjects or its `azp`/`client_id`
//     is in AllowedClients, and
//   - when RequiredRole is set, the role is present in `realm_access.roles` or in
//     `resource_access.<security.oidc.client-id>.roles`.
//
// At least one of AllowedSubjects, AllowedClients or RequiredRole must be set when Enabled is true.
type OIDCBearerConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Audiences       []string `yaml:"audiences"`        // Accepted `aud` values (any match)
	AllowedSubjects []string `yaml:"allowed-subjects"` // Accepted `sub` values
	AllowedClients  []string `yaml:"allowed-clients"`  // Accepted `azp` / `client_id` values
	RequiredRole    string   `yaml:"required-role"`    // Realm role, or client role of security.oidc.client-id

	verifier *oidc.IDTokenVerifier
}

func (b *OIDCBearerConfig) isEnabled() bool {
	return b != nil && b.Enabled
}

// isValid returns whether the bearer configuration is valid. A disabled configuration is always valid.
func (b *OIDCBearerConfig) isValid() bool {
	if !b.isEnabled() {
		return true
	}
	if len(b.Audiences) == 0 {
		return false
	}
	return len(b.AllowedSubjects) > 0 || len(b.AllowedClients) > 0 || len(b.RequiredRole) > 0
}

func (b *OIDCBearerConfig) initialize(provider *oidc.Provider) {
	// Access tokens do not carry the Gatus client id as audience, so the audience is checked separately.
	b.verifier = provider.Verifier(&oidc.Config{SkipClientIDCheck: true})
}

type accessTokenClaims struct {
	Type        string `json:"typ"`
	AZP         string `json:"azp"`
	ClientID    string `json:"client_id"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

// verify validates a raw access token and checks it against the allow rules.
// It returns the identity (client id, or subject when no client id is present) on success.
func (b *OIDCBearerConfig) verify(ctx context.Context, rawToken, gatusClientID string) (string, error) {
	if !b.isEnabled() || b.verifier == nil {
		return "", errBearerNotConfigured
	}
	token, err := b.verifier.Verify(ctx, rawToken)
	if err != nil {
		return "", err
	}
	var claims accessTokenClaims
	if err := token.Claims(&claims); err != nil {
		return "", errBearerClaimsMalformed
	}
	// Keycloak marks access tokens with typ=Bearer; refuse ID and refresh tokens.
	if len(claims.Type) > 0 && !strings.EqualFold(claims.Type, "Bearer") {
		return "", errBearerTokenType
	}
	if !slices.ContainsFunc(token.Audience, func(aud string) bool { return slices.Contains(b.Audiences, aud) }) {
		return "", errBearerAudience
	}
	clientID := claims.AZP
	if len(clientID) == 0 {
		clientID = claims.ClientID
	}
	if len(b.AllowedSubjects) > 0 || len(b.AllowedClients) > 0 {
		subjectAllowed := len(token.Subject) > 0 && slices.Contains(b.AllowedSubjects, token.Subject)
		clientAllowed := len(clientID) > 0 && slices.Contains(b.AllowedClients, clientID)
		if !subjectAllowed && !clientAllowed {
			return "", errBearerIdentity
		}
	}
	if len(b.RequiredRole) > 0 {
		hasRole := slices.Contains(claims.RealmAccess.Roles, b.RequiredRole)
		if !hasRole && len(gatusClientID) > 0 {
			hasRole = slices.Contains(claims.ResourceAccess[gatusClientID].Roles, b.RequiredRole)
		}
		if !hasRole {
			return "", errBearerRoleMissing
		}
	}
	if len(clientID) > 0 {
		return clientID, nil
	}
	return token.Subject, nil
}

// extractBearerToken returns the token from an "Authorization: Bearer <token>" header, or an empty string.
func extractBearerToken(request *http.Request) string {
	header := request.Header.Get("Authorization")
	if len(header) < 7 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

// authenticateBearer reports whether the request carries a bearer token (handled) and, if so, whether it is
// valid (ok). It always returns handled=false when bearer authentication is disabled.
func (c *OIDCConfig) authenticateBearer(request *http.Request) (handled, ok bool) {
	if !c.Bearer.isEnabled() {
		return false, false
	}
	rawToken := extractBearerToken(request)
	if len(rawToken) == 0 {
		return false, false
	}
	identity, err := c.Bearer.verify(request.Context(), rawToken, c.ClientID)
	if err != nil {
		logr.Warnf("[security.OIDCConfig.authenticateBearer] Rejected bearer token: %v", err)
		return true, false
	}
	logr.Debugf("[security.OIDCConfig.authenticateBearer] Accepted bearer token for '%s'", identity)
	return true, true
}

// bearerMiddleware authenticates requests carrying a bearer token and hands all other requests to next.
// A request with an invalid bearer token is rejected outright and never falls back to the session cookie.
func (c *OIDCConfig) bearerMiddleware(next func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(handler http.Handler) http.Handler {
		fallback := next(handler)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handled, ok := c.authenticateBearer(r)
			if !handled {
				fallback.ServeHTTP(w, r)
				return
			}
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			handler.ServeHTTP(w, r)
		})
	}
}
