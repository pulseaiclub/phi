package llm

import "context"

// Credential is the single thing every OrcaRouter inference path needs: a
// bearer key, the account it belongs to, and the generation that key was
// issued under. Whether the user pasted it or signed in to obtain it is not
// represented here on purpose — the router, the model catalog, and every
// entry point consume credentials through this one shape.
type Credential struct {
	// APIKey is the OrcaRouter key ("sk-orca-…"). Never log it.
	APIKey string
	// AccountID is the OrcaRouter user id the key belongs to. Empty for a
	// hand-pasted key, which was never bound to an account in this process.
	AccountID string
	// Source records how the key was obtained, for status display only.
	Source CredentialSource
	// Generation increments on every successful acquisition. A late failure
	// from an older generation must never mark a newer key as broken, so the
	// credential that made a rejected request is compared by generation.
	Generation int
	// NeedsReauth is set when the relay rejected this exact generation with
	// 401. A durable key has no refresh grant, so this is terminal until a
	// new login replaces the credential.
	NeedsReauth bool
}

// CredentialSource identifies which adapter produced a credential.
type CredentialSource string

// CredentialSource values. Both adapters end in the same Credential; only
// status text and tests care which one ran.
const (
	CredentialSourceAPIKey CredentialSource = "api_key"
	CredentialSourcePKCE   CredentialSource = "pkce"
)

// CredentialProvider is the seam between "how a key was obtained" and "what
// the agent does with it". The API-key adapter and the OAuth 2.0 + PKCE
// adapter are two implementations; the provider adapter, the model catalog,
// and the GUI read credentials only through this interface.
type CredentialProvider interface {
	// Credential returns the current credential, or an error when the user
	// has not supplied one yet. Implementations must not perform network I/O.
	Credential(ctx context.Context) (Credential, error)
	// Status describes the credential for display without revealing the key.
	Status(ctx context.Context) CredentialStatus
}

// CredentialStatus is the redacted, UI-safe view of a credential.
type CredentialStatus struct {
	// Present reports that a key is stored.
	Present bool
	// Source is the adapter that supplied it.
	Source CredentialSource
	// AccountID is the OrcaRouter user id, when known.
	AccountID string
	// Masked is the key with all but a short prefix and suffix hidden. It is
	// the only form of the secret that may reach a UI, a log, or an error.
	Masked string
	// NeedsReauth reports that the stored credential was rejected and the
	// user must sign in again.
	NeedsReauth bool
}

// MaskSecret renders a secret for display: a short leading and trailing
// fragment joined by an ellipsis, never the whole value. Anything shorter
// than the fragments collapses to a fixed placeholder so a short or corrupt
// key cannot be reconstructed from its own mask.
func MaskSecret(secret string) string {
	const head, tail = 6, 4
	if secret == "" {
		return ""
	}
	if len(secret) <= head+tail+1 {
		return "••••"
	}
	return secret[:head] + "…" + secret[len(secret)-tail:]
}
