package orca

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
)

// ConnectState is the lifecycle of one authorization attempt.
type ConnectState string

// ConnectState values. Busy is true only for Pending and AwaitingCode.
const (
	StateIdle         ConnectState = "idle"
	StatePending      ConnectState = "pending"
	StateAwaitingCode ConnectState = "awaiting_code"
	StateExchanging   ConnectState = "exchanging"
	StateSucceeded    ConnectState = "succeeded"
	StateDenied       ConnectState = "denied"
	StateFailed       ConnectState = "failed"
	StateCanceled     ConnectState = "canceled"
	StateTimedOut     ConnectState = "timed_out"
)

// Busy reports whether the state holds the login lock.
func (s ConnectState) Busy() bool {
	return s == StatePending || s == StateAwaitingCode
}

// DefaultConnectTimeout bounds how long a user has to approve in the browser.
// The authorization code itself expires after 10 minutes.
const DefaultConnectTimeout = 10 * time.Minute

// ConnectStatus is the UI-safe snapshot of an attempt: no verifier, no code,
// no key.
type ConnectStatus struct {
	State ConnectState
	// AuthorizeURL is the consent URL the user must open. It carries the
	// challenge and state, never the verifier.
	AuthorizeURL string
	// Hint is a short, actionable instruction for the user.
	Hint string
	// Error is a credential-free explanation when the attempt failed.
	Error string
	// Attempt is the generation of this attempt. A response carrying an older
	// attempt must be ignored by the UI.
	Attempt int
	// Scope is the granted scope after a successful exchange.
	Scope string
}

// ConnectOptions configures one authorization attempt.
type ConnectOptions struct {
	// CallbackURL is CallbackOOB for a terminal, or a loopback redirect for
	// the local config server. Defaults to CallbackOOB.
	CallbackURL string
	// AppName is shown on the consent screen.
	AppName string
	// Scope defaults to ScopeAPI.
	Scope string
	// LoginHint pre-fills the consent screen's email field.
	LoginHint string
	// Timeout bounds the attempt. Defaults to DefaultConnectTimeout.
	Timeout time.Duration
	// OpenBrowser is called with the consent URL. Nil means print-only: a
	// headless or SSH session shows the URL and waits for a pasted code.
	OpenBrowser func(url string)
	// AwaitCode receives the code the user pasted, or an error from the
	// frontend when the user cancels. Nil is valid for the OOB flow when the
	// caller instead calls SubmitCode.
	AwaitCode func(ctx context.Context) (string, error)
}

// Connect drives OAuth 2.0 + PKCE and persists the resulting durable key.
//
// It owns one attempt at a time. Every terminal path — success, denial,
// exchange error, timeout, explicit cancel, or a frontend that goes away —
// releases the login lock, so a second attempt can always start.
type Connect struct {
	origins Origins
	store   *Store
	client  *http.Client

	mu       sync.Mutex
	attempt  int
	state    ConnectState
	hint     string
	errText  string
	scope    string
	authURL  string
	pkce     *PKCE
	cancelFn context.CancelFunc
	// terminal records that this attempt already reached a terminal state, so
	// a late response cannot transition it twice.
	terminal bool
}

// NewConnect builds the connect flow. A nil http client uses the default.
func NewConnect(origins Origins, store *Store, client *http.Client) *Connect {
	if client == nil {
		client = http.DefaultClient
	}
	return &Connect{origins: origins, store: store, client: client, state: StateIdle}
}

// Status returns the current snapshot.
func (c *Connect) Status() ConnectStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ConnectStatus{
		State:        c.state,
		AuthorizeURL: c.authURL,
		Hint:         c.hint,
		Error:        c.errText,
		Attempt:      c.attempt,
		Scope:        c.scope,
	}
}

// Busy reports whether an attempt holds the login lock.
func (c *Connect) Busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Busy()
}

// Begin mints fresh PKCE material and moves the flow into Pending. It returns
// the consent URL for the caller to open or print. Starting an attempt while
// one is in flight cancels the older one first: the older attempt's generation
// is invalidated, so its late response cannot overwrite the new attempt.
func (c *Connect) Begin(opts ConnectOptions) (ConnectStatus, error) {
	c.Cancel()

	pkce, err := NewPKCE()
	if err != nil {
		return ConnectStatus{}, err
	}
	callback := opts.CallbackURL
	if callback == "" {
		callback = CallbackOOB
	}
	authURL, err := c.origins.AuthorizeURL(pkce, AuthorizeOptions{
		CallbackURL: callback,
		AppName:     opts.AppName,
		Scope:       opts.Scope,
		LoginHint:   opts.LoginHint,
	})
	if err != nil {
		return ConnectStatus{}, err
	}

	c.mu.Lock()
	c.attempt++
	c.state = StatePending
	c.pkce = &pkce
	c.authURL = authURL
	c.scope = ""
	c.errText = ""
	c.terminal = false
	c.hint = "Open the authorization URL in your browser, then paste the code back here."
	if callback != CallbackOOB {
		c.hint = "Open the authorization URL in your browser to finish connecting."
	}
	status := ConnectStatus{
		State:        c.state,
		AuthorizeURL: authURL,
		Hint:         c.hint,
		Attempt:      c.attempt,
	}
	c.mu.Unlock()

	if opts.OpenBrowser != nil {
		opts.OpenBrowser(authURL)
	}
	return status, nil
}

// AwaitingCode records that the flow is now waiting for a pasted code. It is
// called after the URL has been handed to the user.
func (c *Connect) AwaitingCode(attempt int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if attempt != c.attempt || c.terminal {
		return false
	}
	c.state = StateAwaitingCode
	return true
}

// Run drives one attempt to completion and persists the key on success.
//
// The attempt is bound to the context: canceling it (an explicit Cancel, a
// provider switch, or a pagehide-driven server cancellation) ends the attempt
// without touching the UI, while every terminal transition clears the busy
// state so the lock is released.
func (c *Connect) Run(ctx context.Context, opts ConnectOptions) (llm.Credential, error) {
	status, err := c.Begin(opts)
	if err != nil {
		return llm.Credential{}, err
	}
	attempt := status.Attempt

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c.setCancel(attempt, cancel)

	c.AwaitingCode(attempt)

	var code string
	if opts.AwaitCode == nil {
		// No code source: nothing can complete this attempt.
		return llm.Credential{}, c.failAttempt(attempt, StateFailed,
			"no way to receive the authorization code")
	}
	code, err = opts.AwaitCode(ctx)
	if err != nil {
		state, message := classifyAwaitError(ctx, err)
		return llm.Credential{}, c.failAttempt(attempt, state, message)
	}
	return c.SubmitCode(ctx, attempt, code)
}

// SubmitCode exchanges a code for this attempt and stores the key. The code
// may arrive from a pasted prompt (Flow B) or from the loopback callback
// handler (Flow A); both land here.
func (c *Connect) SubmitCode(ctx context.Context, attempt int, code string) (llm.Credential, error) {
	c.mu.Lock()
	if attempt != c.attempt {
		c.mu.Unlock()
		return llm.Credential{}, errors.New("this authorization attempt was superseded")
	}
	if c.terminal {
		c.mu.Unlock()
		return llm.Credential{}, errors.New("this authorization attempt already finished")
	}
	pkce := c.pkce
	c.state = StateExchanging
	c.mu.Unlock()

	if pkce == nil {
		return llm.Credential{}, c.failAttempt(attempt, StateFailed, "internal error: no PKCE material")
	}

	result, err := c.origins.Exchange(ctx, c.client, code, pkce.Verifier())
	if err != nil {
		// Keep the *ExchangeError in the chain: the caller needs to tell a
		// dead code (terminal) from a rate limit (retryable).
		return llm.Credential{}, c.failAttemptWith(attempt, StateFailed, err.Error(), err)
	}

	// The response's scope is what was granted, not what was asked for.
	if result.Scope != "" && result.Scope != ScopeAPI && result.Scope != ScopeConnector {
		return llm.Credential{}, c.failAttempt(attempt, StateFailed,
			fmt.Sprintf("the granted scope %q is not one this client can use", result.Scope))
	}

	cred, err := c.store.SetPKCEKey(result.APIKey, result.UserID)
	if err != nil {
		return llm.Credential{}, c.failAttempt(attempt, StateFailed,
			"the key could not be stored: "+err.Error())
	}

	c.mu.Lock()
	if attempt != c.attempt {
		// A newer attempt replaced this one while the exchange was in flight.
		// Its credential is the current one; do not overwrite the state.
		c.mu.Unlock()
		return cred, nil
	}
	c.state = StateSucceeded
	c.scope = result.Scope
	c.hint = "Connected."
	c.terminal = true
	c.pkce = nil
	c.cancelFn = nil
	c.mu.Unlock()
	return cred, nil
}

// CallbackQuery handles a loopback redirect: it compares state before doing
// anything else, then returns the code. A denial is reported as denied.
func (c *Connect) CallbackQuery(attempt int, query map[string][]string) (string, error) {
	c.mu.Lock()
	if attempt != c.attempt || c.terminal {
		c.mu.Unlock()
		return "", errors.New("this authorization attempt was superseded")
	}
	pkce := c.pkce
	c.mu.Unlock()
	if pkce == nil {
		return "", errors.New("no authorization attempt is in flight")
	}

	state := firstValue(query, "state")
	if !pkce.MatchesState(state) {
		_ = c.failAttempt(attempt, StateFailed, "state mismatch: the callback did not belong to this attempt")
		return "", errors.New("state mismatch")
	}
	if errCode := firstValue(query, "error"); errCode != "" {
		_ = c.failAttempt(attempt, StateDenied, "authorization was denied")
		return "", fmt.Errorf("authorization was denied: %s", errCode)
	}
	code := firstValue(query, "code")
	if code == "" {
		_ = c.failAttempt(attempt, StateFailed, "the callback carried no authorization code")
		return "", errors.New("the callback carried no authorization code")
	}
	return code, nil
}

func firstValue(query map[string][]string, key string) string {
	values := query[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Cancel ends the in-flight attempt, if any. It is safe to call at any time
// and always releases the lock.
func (c *Connect) Cancel() {
	c.mu.Lock()
	attempt := c.attempt
	cancel := c.cancelFn
	busy := c.state.Busy()
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if busy {
		_ = c.failAttempt(attempt, StateCanceled, "")
	}
}

// CancelAttempt cancels only when attempt is still the current generation, so
// a stale pagehide from an old attempt cannot cancel a newer login.
func (c *Connect) CancelAttempt(attempt int) bool {
	c.mu.Lock()
	current := c.attempt
	busy := c.state.Busy()
	cancel := c.cancelFn
	c.mu.Unlock()
	if attempt != current || !busy {
		return false
	}
	if cancel != nil {
		cancel()
	}
	_ = c.failAttempt(attempt, StateCanceled, "")
	return true
}

// Abandon releases the login lock without recording a user-visible failure.
// The back-forward-cache path uses it: the page may be restored, so it must
// not come back permanently busy, but no error banner is wanted either.
func (c *Connect) Abandon(attempt int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if attempt != c.attempt || c.terminal {
		return false
	}
	if c.cancelFn != nil {
		c.cancelFn()
	}
	c.state = StateCanceled
	c.hint = ""
	c.errText = ""
	c.pkce = nil
	c.cancelFn = nil
	c.terminal = true
	return true
}

func (c *Connect) setCancel(attempt int, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if attempt != c.attempt {
		cancel()
		return
	}
	c.cancelFn = cancel
}

// failAttempt transitions to a terminal state for the given attempt only.
// A stale attempt (already superseded) changes nothing, which is what keeps a
// late failure from an old request out of a newly authenticated session.
func (c *Connect) failAttempt(attempt int, state ConnectState, message string) error {
	return c.failAttemptWith(attempt, state, message, nil)
}

// failAttemptWith is failAttempt plus the underlying cause, so callers can
// still tell a terminal rejection from a retryable rate limit with errors.As.
func (c *Connect) failAttemptWith(attempt int, state ConnectState, message string, cause error) error {
	c.mu.Lock()
	if attempt != c.attempt || c.terminal {
		c.mu.Unlock()
		if message == "" {
			return errors.New("authorization canceled")
		}
		return &attemptError{msg: message, cause: cause}
	}
	c.state = state
	c.errText = message
	c.pkce = nil
	c.cancelFn = nil
	c.terminal = true
	switch state {
	case StateDenied:
		c.hint = "Authorization was denied. Run the login again to retry."
	case StateCanceled:
		c.hint = "Authorization canceled."
	case StateTimedOut:
		c.hint = "Authorization timed out. Run the login again to retry."
	case StateFailed:
		c.hint = "Authorization failed. Run the login again to retry."
	}
	c.mu.Unlock()
	if message == "" {
		return errors.New("authorization canceled")
	}
	return &attemptError{msg: message, cause: cause}
}

// attemptError carries the failure text a user sees plus the exchange error
// that caused it. The cause is what lets a caller ask errors.As for a
// *ExchangeError and decide between "this code is dead" and "try again".
type attemptError struct {
	msg   string
	cause error
}

func (e *attemptError) Error() string { return e.msg }

func (e *attemptError) Unwrap() error { return e.cause }

// classifyAwaitError maps the code-source error onto a state: a cancelled
// context is a timeout, a user cancel stays a cancel, anything else is a
// failure. All three release the lock.
func classifyAwaitError(ctx context.Context, err error) (ConnectState, string) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return StateTimedOut, "authorization timed out; the code expires after 10 minutes"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return StateCanceled, ""
	}
	return StateFailed, sanitizeMessage(err.Error())
}
