package orca

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// VerifierBytes is the entropy of a PKCE verifier: 32 random bytes become a
// 43-character base64url string, inside RFC 7636's 43–128 range.
const VerifierBytes = 32

// StateBytes is the entropy of the CSRF state value.
const StateBytes = 16

// b64url encodes without padding, which is what RFC 7636 and the OrcaRouter
// consent endpoint expect. Standard library only — no new dependency.
func b64url(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

// PKCE holds one authorization attempt's secret material. The verifier never
// leaves the process until the exchange: it is not in the authorize URL, not
// in a log, and not in an error.
type PKCE struct {
	verifier  string
	challenge string
	state     string
}

// NewPKCE mints a fresh verifier, its S256 challenge, and a state value from
// the cryptographic RNG. Every attempt gets new material — nothing here is
// derived from a timestamp, a user name, or a previous attempt.
func NewPKCE() (PKCE, error) {
	verifier, err := randomString(VerifierBytes)
	if err != nil {
		return PKCE{}, fmt.Errorf("generate code verifier: %w", err)
	}
	state, err := randomString(StateBytes)
	if err != nil {
		return PKCE{}, fmt.Errorf("generate state: %w", err)
	}
	return PKCE{verifier: verifier, challenge: ChallengeFor(verifier), state: state}, nil
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return b64url(buf), nil
}

// ChallengeFor returns base64url(sha256(verifier)) with no padding.
func ChallengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64url(sum[:])
}

// Verifier returns the secret. Callers pass it to Exchange and must not
// persist or print it.
func (p PKCE) Verifier() string { return p.verifier }

// Challenge returns the S256 challenge sent on the authorize URL.
func (p PKCE) Challenge() string { return p.challenge }

// State returns the CSRF token echoed back by the consent screen.
func (p PKCE) State() string { return p.state }

// MatchesState compares the state the callback carried against the one this
// attempt sent, in constant time. A mismatch means the code on this callback
// does not belong to this process's authorization request.
func (p PKCE) MatchesState(got string) bool {
	return subtle.ConstantTimeCompare([]byte(p.state), []byte(got)) == 1
}

// CallbackOOB is the literal three-letter callback_url that selects the
// out-of-band flow: the consent screen displays the code for a human to
// transcribe instead of redirecting it somewhere.
const CallbackOOB = "oob"

// AuthorizeOptions are the parameters of one authorization request.
type AuthorizeOptions struct {
	// CallbackURL is a loopback redirect ("http://127.0.0.1:PORT/cb") or
	// CallbackOOB. Required.
	CallbackURL string
	// AppName is the label shown on the consent screen.
	AppName string
	// Scope is "api" (default) or "connector".
	Scope string
	// LoginHint pre-fills the email field.
	LoginHint string
}

// AuthorizeURL builds the consent URL for this attempt. S256 is always sent:
// even in a redirect flow the user may choose "Show me a code" on the consent
// screen, which puts the code in human hands.
func (o Origins) AuthorizeURL(pkce PKCE, opts AuthorizeOptions) (string, error) {
	if opts.CallbackURL == "" {
		return "", errors.New("callback_url is required")
	}
	if opts.CallbackURL != CallbackOOB {
		if _, err := NormalizeCallbackURL(opts.CallbackURL); err != nil {
			return "", err
		}
	}
	scope := opts.Scope
	if scope == "" {
		scope = "api"
	}
	params := url.Values{
		"callback_url":          {opts.CallbackURL},
		"code_challenge":        {pkce.Challenge()},
		"code_challenge_method": {"S256"},
		"state":                 {pkce.State()},
		"scope":                 {scope},
	}
	if opts.AppName != "" {
		params.Set("app_name", opts.AppName)
	}
	if opts.LoginHint != "" {
		params.Set("login_hint", opts.LoginHint)
	}
	return o.authorizeURL(params), nil
}

// NormalizeCallbackURL applies OrcaRouter's callback rules before the user is
// shown anything: https on any host and port, http only on loopback, and no
// userinfo or fragment.
func NormalizeCallbackURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("callback URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("callback URL %q is not a URL: %w", raw, err)
	}
	if u.User != nil {
		return "", fmt.Errorf("callback URL %q must not carry userinfo", raw)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("callback URL %q must not carry a fragment", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", fmt.Errorf("callback URL %q must use https unless it is loopback", raw)
		}
	default:
		return "", fmt.Errorf("callback URL %q must use http or https", raw)
	}
	return u.String(), nil
}
