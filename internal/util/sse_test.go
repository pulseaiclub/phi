package util

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// collectDataStream drains ParseDataStream, copying each payload because the
// yielded slice aliases the scanner's buffer and is only valid until the next
// iteration.
func collectDataStream(t *testing.T, body io.Reader) ([]string, error) {
	t.Helper()
	var payloads []string
	for data, err := range ParseDataStream(body) {
		if err != nil {
			return payloads, err
		}
		payloads = append(payloads, string(bytes.Clone(data)))
	}
	return payloads, nil
}

func TestParseDataStream(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{name: "empty body", body: "", want: nil},
		{
			name: "single event",
			body: "data: {\"a\":1}\n\n",
			want: []string{`{"a":1}`},
		},
		{
			name: "no space after colon",
			body: "data:{\"a\":1}\n\n",
			want: []string{`{"a":1}`},
		},
		{
			name: "only one leading space is stripped",
			body: "data:  indented\n",
			want: []string{" indented"},
		},
		{
			name: "trailing whitespace is kept",
			body: "data: x \n",
			want: []string{"x "},
		},
		{
			name: "empty payload",
			body: "data:\ndata: \n",
			want: []string{"", ""},
		},
		{
			name: "several events in order",
			body: "data: one\n\ndata: two\n\ndata: [DONE]\n\n",
			want: []string{"one", "two", "[DONE]"},
		},
		{
			name: "non-data fields, comments and blank lines are skipped",
			body: ": keep-alive\n" +
				"event: message_start\n" +
				"id: 7\n" +
				"retry: 1000\n" +
				"\n" +
				"data: payload\n" +
				"event: ping\n" +
				"\n",
			want: []string{"payload"},
		},
		{
			name: "prefix must start the line",
			body: " data: indented\nxdata: nope\nDATA: upper\ndata: yes\n",
			want: []string{"yes"},
		},
		{
			name: "CRLF line endings",
			body: "event: delta\r\ndata: one\r\n\r\ndata: two\r\n\r\n",
			want: []string{"one", "two"},
		},
		{
			name: "final line without newline",
			body: "data: first\ndata: last",
			want: []string{"first", "last"},
		},
		{
			name: "colon inside the payload",
			body: "data: data: nested\n",
			want: []string{"data: nested"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := collectDataStream(t, strings.NewReader(tt.body))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseDataStreamLineLongerThanInitialBuffer(t *testing.T) {
	big := strings.Repeat("x", SSEBufferSize*2)
	got, err := collectDataStream(t, strings.NewReader("data: "+big+"\ndata: after\n"))
	require.NoError(t, err)
	require.Equal(t, []string{big, "after"}, got)
}

func TestParseDataStreamLineTooLong(t *testing.T) {
	body := "data: ok\ndata: " + strings.Repeat("x", MaxSSETokenSize) + "\ndata: unreachable\n"
	got, err := collectDataStream(t, strings.NewReader(body))
	require.ErrorIs(t, err, bufio.ErrTooLong)
	require.ErrorContains(t, err, "SSE stream error")
	require.Equal(t, []string{"ok"}, got)
}

// errAfterReader returns its data, then err instead of io.EOF.
type errAfterReader struct {
	r   io.Reader
	err error
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, e.err
	}
	return n, err
}

func TestParseDataStreamReadError(t *testing.T) {
	readErr := errors.New("connection reset")
	body := &errAfterReader{r: strings.NewReader("data: one\ndata: two\n"), err: readErr}

	var payloads []string
	var errs []error
	for data, err := range ParseDataStream(body) {
		if err != nil {
			errs = append(errs, err)
			require.Nil(t, data)
			continue
		}
		payloads = append(payloads, string(data))
	}

	require.Equal(t, []string{"one", "two"}, payloads)
	require.Len(t, errs, 1, "the read error is yielded exactly once, after the payloads")
	require.ErrorIs(t, errs[0], readErr)
	require.EqualError(t, errs[0], "SSE stream error: connection reset")
}

func TestParseDataStreamStopsWhenConsumerBreaks(t *testing.T) {
	var got []string
	for data, err := range ParseDataStream(strings.NewReader("data: one\ndata: two\ndata: three\n")) {
		require.NoError(t, err)
		got = append(got, string(data))
		break
	}
	require.Equal(t, []string{"one"}, got)
}

func TestParseDataStreamNoErrorAfterBreak(t *testing.T) {
	// Breaking out early must not yield the trailing read error: calling
	// yield again after it returned false would panic in a range-over-func.
	body := &errAfterReader{r: strings.NewReader("data: one\n"), err: errors.New("boom")}
	calls := 0
	for range ParseDataStream(body) {
		calls++
		break
	}
	require.Equal(t, 1, calls)
}

func TestParseDataStreamReusesPooledBuffer(t *testing.T) {
	// Run the parser repeatedly so pooled buffers are reused across streams;
	// bytes from a long payload must not leak into the next, shorter one.
	for i := range 3 {
		got, err := collectDataStream(t, strings.NewReader("data: "+strings.Repeat("y", SSEBufferSize+i)+"\n"))
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Len(t, got[0], SSEBufferSize+i)

		got, err = collectDataStream(t, strings.NewReader("data: short\n"))
		require.NoError(t, err)
		require.Equal(t, []string{"short"}, got)
	}
}
