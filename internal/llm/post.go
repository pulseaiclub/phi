package llm

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/pulseaiclub/phi/internal/util"
)

// Post issues a retry-wrapped JSON POST and validates the response status.
// Content-Type: application/json is always set; extra carries provider
// headers (auth, streaming Accept). A non-200 reply is drained and converted
// into a provider error, so a nil error guarantees a 200 response whose body
// the caller owns and must close.
func Post(
	ctx context.Context,
	client *http.Client,
	provider string,
	url string,
	body []byte,
	extra http.Header,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, values := range extra {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := util.DoWithRetry(client, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, FormatAPIError(provider, resp.StatusCode, respBody)
	}
	return resp, nil
}

// PostBody is Post plus a full read of the 200 reply's body, for
// non-streaming endpoints.
func PostBody(
	ctx context.Context,
	client *http.Client,
	provider string,
	url string,
	body []byte,
	extra http.Header,
) ([]byte, error) {
	resp, err := Post(ctx, client, provider, url, body, extra)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
