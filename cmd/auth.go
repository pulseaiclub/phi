package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/orca"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/util"
)

// orcaAppName labels this client on the OrcaRouter consent screen.
const orcaAppName = "phi"

var (
	authCommand = cli.Command{
		Name: "auth",
		Desc: "manage OrcaRouter credentials (login, status, logout)",
		Long: `Two ways to connect OrcaRouter:

  phi auth login --orcarouter          sign in with OAuth 2.0 + PKCE (browser)
  phi auth login --orcarouter --api-key  paste an existing sk-orca-… key

Both end in the same durable OrcaRouter key, stored once and reused until it
is revoked. Keys: ` + orca.ConsoleKeysURL,
	}

	authLoginCommand = cli.Command{
		Name: "login",
		Desc: "connect OrcaRouter (PKCE sign-in by default, or --api-key)",
		Flags: []cli.Flag{
			cli.Bool("orcarouter", "", "connect OrcaRouter"),
			cli.Bool("api-key", "", "prompt for an existing sk-orca-… key instead of signing in"),
			cli.String("token", "", "provide the key non-interactively from stdin (for scripts)", ""),
			cli.String("login-hint", "", "pre-fill the consent screen's email field", ""),
			cli.String("scope", "", "requested scope: api (default) or connector", ""),
			cli.Bool("no-browser", "", "print the authorization URL instead of opening a browser"),
		},
	}

	authStatusCommand = cli.Command{
		Name: "status",
		Desc: "show the OrcaRouter credential state (never the key)",
	}

	authLogoutCommand = cli.Command{
		Name:    "logout",
		Aliases: []string{"clear"},
		Desc:    "remove the stored OrcaRouter credential",
	}
)

func init() {
	authLoginCommand.Run = func(_ []string, flags cli.Flags) error { return runAuthLogin(flags) }
	authStatusCommand.Run = func(_ []string, _ cli.Flags) error { return runAuthStatus() }
	authLogoutCommand.Run = func(_ []string, _ cli.Flags) error { return runAuthLogout() }

	authCommand.Add(&authLoginCommand, &authStatusCommand, &authLogoutCommand)
}

// runAuthLogin is the single entry point for both authentication choices. The
// two adapters converge on the same store, so nothing downstream can tell
// which one ran.
func runAuthLogin(flags cli.Flags) error {
	if !flags.Bool("orcarouter") {
		return authLoginCommand.Usagef("specify a provider: --orcarouter")
	}
	proj := project.GetDefaultProject()
	store := proj.OrcaStore()

	if token := strings.TrimSpace(flags.String("token")); token != "" {
		return storeAPIKey(store, token)
	}
	if flags.Bool("api-key") {
		key, err := promptSecret("Paste your OrcaRouter API key (sk-orca-…): ")
		if err != nil {
			return err
		}
		return storeAPIKey(store, key)
	}
	return loginWithPKCE(store, flags)
}

// storeAPIKey is the API-key adapter's write path: the pasted key goes through
// the same seam the PKCE flow writes to.
func storeAPIKey(store *orca.Store, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("no API key supplied")
	}
	// A prefix check catches an obvious paste mistake; it is not proof of
	// validity, and no paid request is spent to make a settings screen say so.
	if !strings.HasPrefix(key, "sk-orca-") {
		fmt.Fprintln(os.Stderr, "warning: OrcaRouter keys normally start with \"sk-orca-\"; storing it anyway")
	}
	if _, err := store.SetAPIKey(key, ""); err != nil {
		return err
	}
	fmt.Printf("OrcaRouter API key stored (%s).\n", maskedStatus(store))
	fmt.Printf("Add a model entry with api: %s and name: %s to start using it.\n",
		model.OrcaRouterProvider, model.OrcaAutoModel)
	return nil
}

// loginWithPKCE runs Flow B: the consent screen displays a code and the user
// pastes it back. A terminal client has no predictable callback address — it
// may be an SSH session or a container — which is exactly what the
// out-of-band flow exists for.
func loginWithPKCE(store *orca.Store, flags cli.Flags) error {
	origins, err := orca.ResolveOrigins(nil)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// The code is read from stdin while the attempt runs, so the reader is
	// created once and handed to the connect flow.
	reader := bufio.NewReader(os.Stdin)
	codes := make(chan string, 1)
	codeErrs := make(chan error, 1)
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			codeErrs <- err
			return
		}
		codes <- strings.TrimSpace(line)
	}()

	conn := orca.NewConnect(origins, store, util.DefaultHTTPClient())
	var launch func(string)
	if !flags.Bool("no-browser") {
		launch = func(url string) { openBrowser(ctx, url) }
	}

	status, err := conn.Begin(orca.ConnectOptions{
		CallbackURL: orca.CallbackOOB,
		AppName:     orcaAppName,
		Scope:       flags.String("scope"),
		LoginHint:   flags.String("login-hint"),
		OpenBrowser: launch,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Authorize phi in your browser, then paste the code back here.\n")
	if flags.Bool("no-browser") {
		fmt.Printf("\n%s\n\n", status.AuthorizeURL)
	}
	fmt.Print("Code: ")

	cred, err := conn.Run(ctx, orca.ConnectOptions{
		AwaitCode: func(ctx context.Context) (string, error) {
			select {
			case code := <-codes:
				if code == "" {
					return "", errors.New("no code supplied")
				}
				return code, nil
			case err := <-codeErrs:
				return "", err
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if final := conn.Status(); final.Hint != "" {
			fmt.Fprintln(os.Stderr, final.Hint)
		}
		// The diagnostic is already printed; exit non-zero without a duplicate.
		return exitCode(ExitError)
	}

	if final := conn.Status(); final.Scope != "" && final.Scope != orca.ScopeAPI {
		fmt.Fprintf(os.Stderr, "note: the granted scope is %q, not %q\n", final.Scope, orca.ScopeAPI)
	}
	_ = cred
	fmt.Printf("Connected. OrcaRouter key stored (%s).\n", maskedStatus(store))
	fmt.Printf("Add a model entry with api: %s and name: %s to start using it.\n",
		model.OrcaRouterProvider, model.OrcaAutoModel)
	return nil
}

func runAuthStatus() error {
	proj := project.GetDefaultProject()
	store := proj.OrcaStore()
	st := store.Status(context.Background())
	if !st.Present {
		fmt.Println("OrcaRouter: not connected.")
		fmt.Println("  phi auth login --orcarouter          (OAuth 2.0 + PKCE)")
		fmt.Println("  phi auth login --orcarouter --api-key (paste an existing key)")
		return nil
	}
	source := "API key"
	if st.Source == "pkce" {
		source = "OAuth 2.0 + PKCE"
	}
	fmt.Printf("OrcaRouter: connected via %s\n", source)
	fmt.Printf("  key: %s\n", st.Masked)
	if st.AccountID != "" {
		fmt.Printf("  account: %s\n", st.AccountID)
	}
	if st.NeedsReauth {
		fmt.Println("  state: needs reauthentication — run 'phi auth login --orcarouter'")
	}
	fmt.Printf("  revoke: %s\n", orca.ConsoleKeysURL)
	return nil
}

func runAuthLogout() error {
	store := project.GetDefaultProject().OrcaStore()
	if err := store.Clear(); err != nil {
		return err
	}
	if orca.Getenv() != "" {
		fmt.Printf("Stored OrcaRouter credential removed. %s is still set and takes precedence.\n", orca.EnvAPIKey)
		return nil
	}
	fmt.Println("Stored OrcaRouter credential removed.")
	return nil
}

// maskedStatus renders the current credential for a confirmation line without
// ever printing the key.
func maskedStatus(store *orca.Store) string {
	st := store.Status(context.Background())
	if !st.Present {
		return "no key"
	}
	return st.Masked
}

// promptSecret reads a secret from stdin. Echo is not suppressed because phi
// has no terminal raw-mode helper, so the flag documents that it is for
// non-interactive use; the value is never logged or persisted except through
// the credential store.
func promptSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
