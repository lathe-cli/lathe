package runtime

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lathe-cli/lathe/internal/testutil"
)

type observedWriter struct {
	buf   bytes.Buffer
	once  sync.Once
	wrote chan struct{}
}

func (w *observedWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	w.once.Do(func() { close(w.wrote) })
	return n, err
}

func (w *observedWriter) String() string { return w.buf.String() }

func TestBuild_RawStreamingWritesBeforeResponseCloses(t *testing.T) {
	isolateRuntime(t)

	firstSent := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		close(firstSent)
		select {
		case <-release:
			_, _ = io.WriteString(w, "data: second\n\n")
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	out := &observedWriter{wrote: make(chan struct{})}
	root := newExecutionRoot("raw")
	root.SetOut(out)
	root.SetErr(io.Discard)
	mustBuild(t, root, "demo", []CommandSpec{{
		Group:    "Events",
		Use:      "watch",
		Method:   http.MethodGet,
		PathTpl:  "/events",
		Output:   OutputHints{Streaming: &StreamingHint{Strategy: "sse"}},
		Security: &SecurityHint{Public: true},
	}})
	root.SetArgs([]string{"--hostname", srv.URL, "demo", "events", "watch", "-o", "raw"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()
	select {
	case <-firstSent:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("server did not send first event")
	}
	select {
	case <-out.wrote:
	case <-time.After(200 * time.Millisecond):
		close(release)
		<-errCh
		t.Fatal("command produced no output before the streaming response closed")
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "data: first\n\ndata: second\n\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestBuild_StreamOutputFlagCombinations(t *testing.T) {
	isolateRuntime(t)

	response := "{\"kind\":\"done\"}\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, response)
	}))
	defer srv.Close()

	spec := CommandSpec{
		Group:   "Events",
		Use:     "watch",
		Method:  http.MethodPost,
		PathTpl: "/events",
		Output: OutputHints{Streaming: &StreamingHint{Strategy: "ndjson", Policy: &StreamPolicy{
			DataFormat:    "json",
			EventNamePath: "kind",
			Collect:       &StreamCollectHint{RequireStop: true, StopEvents: []string{"done"}},
			Live:          &StreamLiveHint{Events: []string{"chunk"}, From: "text"},
		}}},
		Security: &SecurityHint{Public: true},
	}
	cases := []struct {
		name       string
		args       []string
		wantErr    string
		wantOutput string
	}{
		{name: "live with json", args: []string{"-o", "json", "demo", "events", "watch", "--stream"}, wantErr: "live stream output does not support -o json"},
		{name: "live with wait", args: []string{"demo", "events", "watch", "--stream", "--wait"}, wantErr: "live stream output does not support wait polling"},
		{name: "raw with wait", args: []string{"-o", "raw", "demo", "events", "watch", "--wait"}, wantOutput: response},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			root := newExecutionRoot("table")
			root.SetOut(&out)
			root.SetErr(io.Discard)
			mustBuild(t, root, "demo", []CommandSpec{spec})
			root.SetArgs(append([]string{"--hostname", srv.URL}, tc.args...))

			err := root.Execute()
			if tc.wantErr != "" {
				testutil.Require(t, err != nil && strings.Contains(err.Error(), tc.wantErr), "expected %q error, got %v", tc.wantErr, err)
				return
			}
			testutil.Require(t, err == nil, "Execute: %v", err)
			if out.String() != tc.wantOutput {
				t.Fatalf("output = %q, want %q", out.String(), tc.wantOutput)
			}
		})
	}
}
