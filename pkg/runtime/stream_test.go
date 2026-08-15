package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInvokeOperation_CollectsAndProjectsSSE(t *testing.T) {
	firstSent := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: chunk\rdata: {\"kind\":\"ignored\",\"text\":\"hel\",\"mode\":\"chat\"}\r\r")
		w.(http.Flusher).Flush()
		close(firstSent)
		select {
		case <-release:
			_, _ = io.WriteString(w, "data: {\"kind\":\"chunk\",\"text\":\"lo\"}\r\rdata: {\"kind\":\"done\",\"id\":\"msg-1\"}\r\r")
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	hint := &StreamingHint{Strategy: "sse", Policy: &StreamPolicy{
		DataFormat: "json", EventNamePath: "kind",
		Collect: &StreamCollectHint{
			RequireStop: true,
			StopEvents:  []string{"done"},
			Fields: []StreamFieldRule{
				{Events: []string{"chunk"}, From: "text", To: "answer", Reduce: "concat"},
				{Events: []string{"chunk"}, From: "mode", To: "mode", Reduce: "first"},
				{Events: []string{"done"}, From: "id", To: "message_id", Reduce: "last"},
			},
		},
		Live: &StreamLiveHint{Events: []string{"chunk"}, From: "text"},
	}}
	out := &observedWriter{wrote: make(chan struct{})}
	resultCh := make(chan OperationResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := invokeOperation(context.Background(), CommandSpec{
			Method: "GET", PathTpl: "/events", Output: OutputHints{Streaming: hint},
		}, OperationInput{}, OperationOptions{Hostname: srv.URL, Client: ClientOptions{MaxRetries: -1}}, operationOutput{live: out})
		resultCh <- result
		errCh <- err
	}()
	select {
	case <-firstSent:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("server did not send the first event")
	}
	select {
	case <-out.wrote:
	case <-time.After(200 * time.Millisecond):
		close(release)
		<-errCh
		t.Fatal("live projection did not write before the response closed")
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("invokeOperation: %v", err)
	}
	result := <-resultCh
	if result.Outcome != OperationOutcomeCompleted || out.String() != "hello" {
		t.Fatalf("outcome = %q, live = %q", result.Outcome, out.String())
	}
	var got map[string]any
	if err := json.Unmarshal(result.Data, &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	want := map[string]any{"answer": "hello", "message_id": "msg-1", "mode": "chat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result = %#v, want %#v", got, want)
	}
}

func TestReadSSE_AcceptsStandardLineEndings(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  string
	}{
		{name: "crlf", end: "\r\n"},
		{name: "cr", end: "\r"},
		{name: "lf", end: "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event, data string
			input := "event: chunk" + tc.end + "data: hello" + tc.end + tc.end
			err := readSSE(strings.NewReader(input), func(gotEvent string, gotData []byte) error {
				event, data = gotEvent, string(gotData)
				return nil
			})
			if err != nil {
				t.Fatalf("readSSE: %v", err)
			}
			if event != "chunk" || data != "hello" {
				t.Fatalf("event = %q, data = %q", event, data)
			}
		})
	}
}

func TestCollectStream_RejectsMissingRequiredStop(t *testing.T) {
	hint := &StreamingHint{Strategy: "ndjson", Policy: &StreamPolicy{
		DataFormat: "json", EventNamePath: "kind",
		Collect: &StreamCollectHint{RequireStop: true, StopEvents: []string{"done"}},
	}}
	outcome := OperationOutcomeCompleted
	if _, err := collectStream(strings.NewReader("{\"kind\":\"chunk\"}\n"), hint, nil, &outcome); err == nil {
		t.Fatal("expected missing terminal event error")
	}

	hint.Policy.Collect.RequireStop = false
	hint.Policy.Collect.ErrorEvents = []string{"failed"}
	_, err := collectStream(strings.NewReader("{\"kind\":\"failed\",\"message\":\"run failed; token=stream-secret; Bearer stream-bearer; Basic c3RyZWFtOmJhc2lj\"}\n"), hint, nil, &outcome)
	if classified := ClassifyError(err); classified == nil || classified.ExitCode != ExitAPIError {
		t.Fatalf("error = %v, classified = %#v", err, classified)
	}
	if strings.Contains(err.Error(), "stream-secret") || strings.Contains(err.Error(), "stream-bearer") || strings.Contains(err.Error(), "c3RyZWFtOmJhc2lj") {
		t.Fatalf("stream error leaked server secret: %v", err)
	}
}
