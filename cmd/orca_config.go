package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/pulseaiclub/phi/internal/orca"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/util"
)

// orcaModelItem is one entry of the OrcaRouter catalog as the page sees it.
// It carries only metadata the browser may hold — never a key.
type orcaModelItem struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ContextWindow   int      `json:"contextWindow,omitempty"`
	InputModalities []string `json:"inputModalities,omitempty"`
	EndpointTypes   []string `json:"endpointTypes,omitempty"`
	Source          string   `json:"source"`
}

type orcaModelsResponse struct {
	Models []orcaModelItem `json:"models"`
	// Source is "live" or "seed"; Degraded is true when live discovery failed
	// and the verified fallback list is in use.
	Source   string `json:"source"`
	Degraded bool   `json:"degraded"`
	Warning  string `json:"warning,omitempty"`
	// CatalogURL is the authoritative source the list came from.
	CatalogURL string `json:"catalogUrl"`
}

// orcaState holds the in-flight authorization attempt for this config server.
// One page drives one attempt at a time; the connect object enforces that and
// the generation guard keeps a late response out of a newer login.
type orcaState struct {
	mu       sync.Mutex
	connect  *orca.Connect
	listener *orca.CallbackListener
}

// connectFor returns the connect flow for this handler, creating it on first
// use.
func (h *configHandler) connectFor(origins orca.Origins, store *orca.Store) *orca.Connect {
	h.orca.mu.Lock()
	defer h.orca.mu.Unlock()
	if h.orca.connect == nil {
		h.orca.connect = orca.NewConnect(origins, store, util.DefaultHTTPClient())
	}
	return h.orca.connect
}

// handleOrcaModels serves the capability-filtered OrcaRouter catalog. The key
// stays on this side of the loopback boundary: the browser receives model
// metadata only.
func (h *configHandler) handleOrcaModels(w http.ResponseWriter, r *http.Request, input modelListRequest) {
	origins, store, err := h.orcaContext()
	if err != nil {
		writeConfigErr(w, http.StatusServiceUnavailable, err)
		return
	}

	capability := orca.Capability(strings.TrimSpace(input.Capability))
	if capability == "" {
		capability = orca.CapabilityChat
	}
	if input.ImageInput {
		capability = orca.CapabilityImageInput
	}
	switch capability {
	case orca.CapabilityChat, orca.CapabilityImageInput, orca.CapabilityEmbedding,
		orca.CapabilityImageGen, orca.CapabilityVideo, orca.CapabilityRerank:
	default:
		writeConfigErr(w, http.StatusBadRequest,
			fmt.Errorf("unknown capability %q", input.Capability))
		return
	}

	key := strings.TrimSpace(input.APIKey)
	if key == "" {
		if cred, credErr := store.Credential(r.Context()); credErr == nil {
			key = cred.APIKey
		}
	}

	catalog := origins.LiveCatalogWithFallback(r.Context(), modelListHTTPClient(), key, capability)
	items := make([]orcaModelItem, 0, len(catalog.Models))
	for _, m := range catalog.Models {
		items = append(items, orcaModelItem{
			ID:              m.ID,
			Name:            m.Name,
			ContextWindow:   m.ContextWindow,
			InputModalities: m.InputModalities,
			EndpointTypes:   m.EndpointTypes,
			Source:          m.Source,
		})
	}
	writeConfigJSON(w, orcaModelsResponse{
		Models:     items,
		Source:     catalog.Source,
		Degraded:   catalog.Degraded,
		Warning:    catalog.Warning,
		CatalogURL: origins.CatalogURLFor(capability),
	})
}

// orcaContext resolves the origins and credential store for this workspace.
func (h *configHandler) orcaContext() (orca.Origins, *orca.Store, error) {
	origins, err := orca.ResolveOrigins(nil)
	if err != nil {
		return orca.Origins{}, nil, err
	}
	proj := h.proj
	if proj == nil {
		proj = project.GetDefaultProject()
	}
	return origins, proj.OrcaStore(), nil
}

// orcaCredentialRequest is the body of a credential write.
type orcaCredentialRequest struct {
	// APIKey is a key the user pasted into the page. It is written straight to
	// the credential store and never echoed back.
	APIKey string `json:"apiKey"`
	// Clear removes the stored credential.
	Clear bool `json:"clear"`
}

// orcaCredentialResponse is the redacted view of the credential.
type orcaCredentialResponse struct {
	Present     bool   `json:"present"`
	Source      string `json:"source,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	Masked      string `json:"masked,omitempty"`
	NeedsReauth bool   `json:"needsReauth"`
	// ConsoleURL is where the user reads or revokes keys.
	ConsoleURL string `json:"consoleUrl"`
}

// handleOrcaCredential reads or writes the OrcaRouter credential. It is the
// API-key adapter's HTTP entry point; the PKCE flow writes through the same
// store, so both choices produce one credential shape.
func (h *configHandler) handleOrcaCredential(w http.ResponseWriter, r *http.Request) {
	_, store, err := h.orcaContext()
	if err != nil {
		writeConfigErr(w, http.StatusServiceUnavailable, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeConfigJSON(w, orcaCredentialView(r.Context(), store))
	case http.MethodPost:
		if status, err := validateLocalJSONRequest(r); err != nil {
			writeConfigErr(w, status, err)
			return
		}
		var input orcaCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeConfigErr(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
			return
		}
		switch {
		case input.Clear:
			if err := store.Clear(); err != nil {
				writeConfigErr(w, http.StatusInternalServerError, err)
				return
			}
		case strings.TrimSpace(input.APIKey) != "":
			if _, err := store.SetAPIKey(input.APIKey, ""); err != nil {
				writeConfigErr(w, http.StatusBadRequest, err)
				return
			}
		default:
			writeConfigErr(w, http.StatusBadRequest, errors.New("supply an apiKey or set clear"))
			return
		}
		writeConfigJSON(w, orcaCredentialView(r.Context(), store))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func orcaCredentialView(ctx context.Context, store *orca.Store) orcaCredentialResponse {
	st := store.Status(ctx)
	return orcaCredentialResponse{
		Present:     st.Present,
		Source:      string(st.Source),
		AccountID:   st.AccountID,
		Masked:      st.Masked,
		NeedsReauth: st.NeedsReauth,
		ConsoleURL:  orca.ConsoleKeysURL,
	}
}

// orcaConnectState is the page-visible snapshot of one authorization attempt.
type orcaConnectState struct {
	State string `json:"state"`
	// AuthorizeURL is the consent URL. It carries the S256 challenge and the
	// state, never the verifier.
	AuthorizeURL string `json:"authorizeUrl,omitempty"`
	Hint         string `json:"hint,omitempty"`
	Error        string `json:"error,omitempty"`
	Attempt      int    `json:"attempt"`
	Scope        string `json:"scope,omitempty"`
	// Busy is true while the login lock is held. The page must clear its own
	// busy flag whenever this turns false, including after a pagehide.
	Busy bool `json:"busy"`
}

// orcaConnectRequest is the body of a connect call.
type orcaConnectRequest struct {
	// Attempt is the generation the page believes is current. A stale value is
	// rejected so an old page cannot cancel a newer login.
	Attempt int `json:"attempt"`
	// Action is one of: begin, cancel, abandon, code.
	Action string `json:"action"`
	// Code is the authorization code, for a pasted out-of-band code.
	Code string `json:"code"`
	// Redirect selects Flow A: the server listens on a loopback port and the
	// consent screen redirects the code back to it.
	Redirect bool `json:"redirect"`
	// LoginHint pre-fills the consent screen's email field.
	LoginHint string `json:"loginHint"`
	// Scope requests api (default) or connector.
	Scope string `json:"scope"`
}

// handleOrcaConnect drives OAuth 2.0 + PKCE from the config page.
func (h *configHandler) handleOrcaConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if status, err := validateLocalJSONRequest(r); err != nil {
		writeConfigErr(w, status, err)
		return
	}
	var input orcaConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeConfigErr(w, http.StatusBadRequest, fmt.Errorf("bad request: %w", err))
		return
	}

	origins, store, err := h.orcaContext()
	if err != nil {
		writeConfigErr(w, http.StatusServiceUnavailable, err)
		return
	}
	conn := h.connectFor(origins, store)

	switch input.Action {
	case "begin":
		h.orcaConnectBegin(w, r, conn, input)
	case "cancel":
		if input.Attempt != 0 {
			conn.CancelAttempt(input.Attempt)
		} else {
			conn.Cancel()
		}
		writeConfigJSON(w, orcaStateOf(conn))
	case "abandon":
		// The page is going away (pagehide / unmount). Release the lock and
		// report no error: the page may be restored from the back-forward
		// cache and must not come back permanently busy.
		if input.Attempt != 0 {
			conn.Abandon(input.Attempt)
		} else {
			conn.Cancel()
		}
		h.closeListener()
		writeConfigJSON(w, orcaStateOf(conn))
	case "code":
		status := conn.Status()
		if input.Attempt != 0 && input.Attempt != status.Attempt {
			writeConfigErr(w, http.StatusConflict,
				errors.New("this authorization attempt was superseded"))
			return
		}
		if _, err := conn.SubmitCode(r.Context(), status.Attempt, input.Code); err != nil {
			writeConfigErr(w, http.StatusBadGateway, err)
			return
		}
		h.closeListener()
		writeConfigJSON(w, orcaStateOf(conn))
	default:
		writeConfigErr(w, http.StatusBadRequest,
			fmt.Errorf("unknown action %q", input.Action))
	}
}

func (h *configHandler) orcaConnectBegin(
	w http.ResponseWriter,
	r *http.Request,
	conn *orca.Connect,
	input orcaConnectRequest,
) {
	opts := orca.ConnectOptions{
		CallbackURL: orca.CallbackOOB,
		AppName:     orcaAppName,
		Scope:       input.Scope,
		LoginHint:   input.LoginHint,
	}
	if input.Redirect {
		// Flow A: the config page runs beside a browser on loopback, so the
		// code can come straight back to this process. The listener is opened
		// before the consent URL is built, so the port is known and nothing
		// races.
		h.closeListener()
		listener, err := orca.ListenForCallback(conn)
		if err != nil {
			writeConfigErr(w, http.StatusBadGateway, err)
			return
		}
		h.orca.mu.Lock()
		h.orca.listener = listener
		h.orca.mu.Unlock()
		opts.CallbackURL = listener.CallbackURL()
	}

	status, err := conn.Begin(opts)
	if err != nil {
		h.closeListener()
		writeConfigErr(w, http.StatusBadRequest, err)
		return
	}
	if status.AuthorizeURL != "" {
		// Best effort: the URL is also returned, so a browser that does not
		// open automatically still lets the user continue.
		openBrowser(r.Context(), status.AuthorizeURL)
	}
	if input.Redirect {
		h.awaitRedirect(conn, status.Attempt)
	}
	writeConfigJSON(w, orcaStateOf(conn))
}

// awaitRedirect completes a Flow A attempt in the background: it waits for the
// redirect, exchanges the code, and closes the listener on every path.
func (h *configHandler) awaitRedirect(conn *orca.Connect, attempt int) {
	h.orca.mu.Lock()
	listener := h.orca.listener
	h.orca.mu.Unlock()
	if listener == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), orcaConnectTimeout)
		defer cancel()
		code, err := listener.Wait(ctx)
		if err != nil {
			// CallbackQuery already recorded the reason when it had one.
			_ = err
			return
		}
		if _, err := conn.SubmitCode(context.Background(), attempt, code); err != nil {
			_ = err
		}
	}()
}

func (h *configHandler) closeListener() {
	h.orca.mu.Lock()
	listener := h.orca.listener
	h.orca.listener = nil
	h.orca.mu.Unlock()
	if listener != nil {
		listener.Close()
	}
}

func orcaStateOf(conn *orca.Connect) orcaConnectState {
	st := conn.Status()
	return orcaConnectState{
		State:        string(st.State),
		AuthorizeURL: st.AuthorizeURL,
		Hint:         st.Hint,
		Error:        st.Error,
		Attempt:      st.Attempt,
		Scope:        st.Scope,
		Busy:         st.State.Busy(),
	}
}

// handleOrcaConnectStatus reports the current attempt so a page can recover
// after a reload without starting a second authorization.
func (h *configHandler) handleOrcaConnectStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	origins, store, err := h.orcaContext()
	if err != nil {
		writeConfigErr(w, http.StatusServiceUnavailable, err)
		return
	}
	writeConfigJSON(w, orcaStateOf(h.connectFor(origins, store)))
}
