package demo_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	api "example.com/oapi-observability/client"
)

type observation struct {
	operation string
	status    int
	err       error
}

type recordingObserver struct {
	mu        sync.Mutex
	started   int
	completed []observation
}

type observationKey struct{}

// The observer uses only standard Go types and lives outside the generated package.
var _ api.Observer = (*recordingObserver)(nil)

func (o *recordingObserver) Start(ctx context.Context, operation string) (context.Context, func(int, error)) {
	o.mu.Lock()
	o.started++
	o.mu.Unlock()
	return context.WithValue(ctx, observationKey{}, operation), func(status int, err error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.completed = append(o.completed, observation{operation, status, err})
	}
}

func (o *recordingObserver) snapshot() (int, []observation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.started, append([]observation(nil), o.completed...)
}

// Accept a client exposing only the typed response interface.
type withResponsesOnly struct {
	api.ClientWithResponsesInterface
}

func TestObservedClientExposesOnlyWithResponses(t *testing.T) {
	if _, ok := any((*api.ObservedClient)(nil)).(api.ClientInterface); ok {
		t.Fatal("decorator exposes unobserved raw client methods")
	}
}

func TestCallOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		body          string
		editorFailure bool
		brokenURL     bool
		wantError     bool
		wantStatus    int
	}{
		{name: "ok", status: 200, body: `{"id":"42"}`, wantStatus: 200},
		{name: "not_found", status: 404, wantStatus: 404},
		{name: "unavailable", status: 503, wantStatus: 503},
		{name: "invalid_json", status: 200, body: `{`, wantError: true},
		{name: "editor_failure", editorFailure: true, wantError: true},
		{name: "construction_failure", brokenURL: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			baseURL := server.URL
			if tc.brokenURL {
				baseURL = "://broken"
			}
			editorError := errors.New("editor failed")
			raw, err := api.NewClientWithResponses(baseURL)
			if err != nil {
				t.Fatal(err)
			}
			observer := &recordingObserver{}
			client := api.NewObservedClient(withResponsesOnly{raw}, observer)
			response, err := client.GetUserWithResponse(context.Background(), "42", nil,
				func(ctx context.Context, req *http.Request) error {
					if ctx.Value(observationKey{}) != "GetUser" || req.Context().Value(observationKey{}) != "GetUser" {
						t.Error("observer context did not reach the request editor")
					}
					if tc.editorFailure {
						return editorError
					}
					return nil
				},
			)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error = %v", err, tc.wantError)
			}
			if tc.editorFailure && !errors.Is(err, editorError) {
				t.Fatalf("editor error was not preserved: %v", err)
			}
			if !tc.wantError {
				if response == nil || response.StatusCode() != tc.status || string(response.Body) != tc.body {
					t.Fatalf("unexpected response: %+v", response)
				}
				if tc.status == 200 && (response.JSON200 == nil || response.JSON200.Id != "42") {
					t.Fatal("decoded response was not preserved")
				}
			}
			started, completed := observer.snapshot()
			if started != 1 || len(completed) != 1 {
				t.Fatalf("started = %d, completed = %d; want one observation", started, len(completed))
			}
			got := completed[0]
			if got.operation != "GetUser" || got.status != tc.wantStatus || got.err != err {
				t.Fatalf("unexpected observation: %+v; returned error = %v", got, err)
			}
		})
	}
}

func TestBodyVariants(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*api.ObservedClient) (*api.CreateUserResponse, error)
	}{
		{"json", func(c *api.ObservedClient) (*api.CreateUserResponse, error) {
			return c.CreateUserWithResponse(context.Background(), api.CreateUserJSONRequestBody{Id: "1"})
		}},
		{"generic", func(c *api.ObservedClient) (*api.CreateUserResponse, error) {
			return c.CreateUserWithBodyWithResponse(context.Background(), "application/json", strings.NewReader(`{"id":"1"}`))
		}},
		{"text", func(c *api.ObservedClient) (*api.CreateUserResponse, error) {
			return c.CreateUserWithTextBodyWithResponse(context.Background(), "1")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/users" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"id":"1"}`)
			}))
			defer server.Close()
			raw, err := api.NewClientWithResponses(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			observer := &recordingObserver{}
			response, err := tc.invoke(api.NewObservedClient(withResponsesOnly{raw}, observer))
			if err != nil {
				t.Fatal(err)
			}
			if response.JSON201 == nil || response.JSON201.Id != "1" {
				t.Fatal("decoded response was not preserved")
			}
			started, completed := observer.snapshot()
			if started != 1 || len(completed) != 1 || completed[0] != (observation{"CreateUser", 201, nil}) {
				t.Fatalf("unexpected observations: %d, %+v", started, completed)
			}
		})
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type gatedBody struct {
	started chan struct{}
	release chan struct{}
	read    bool
}

func (b *gatedBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	close(b.started)
	<-b.release
	return copy(p, `{"id":"42"}`), io.EOF
}

func (*gatedBody) Close() error { return nil }

func TestObservationCoversBodyRead(t *testing.T) {
	observer := &recordingObserver{}
	body := &gatedBody{started: make(chan struct{}), release: make(chan struct{})}
	raw, err := api.NewClientWithResponses("https://example.test", api.WithHTTPClient(doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := api.NewObservedClient(withResponsesOnly{raw}, observer).GetUserWithResponse(context.Background(), "42", nil)
		done <- err
	}()
	<-body.started
	if started, completed := observer.snapshot(); started != 1 || len(completed) != 0 {
		t.Error("observation completed before the response body was read")
	}
	close(body.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, completed := observer.snapshot(); len(completed) != 1 || completed[0].status != 200 {
		t.Fatalf("unexpected completion: %+v", completed)
	}
}
