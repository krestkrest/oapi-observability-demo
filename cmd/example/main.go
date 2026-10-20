package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	api "example.com/oapi-observability/client"
)

// logObserver can be replaced with metrics, tracing, or application-specific work.
type logObserver struct {
	logger *log.Logger
}

func (o logObserver) Start(ctx context.Context, operation string) (context.Context, func(int, error)) {
	started := time.Now()
	return ctx, func(status int, err error) {
		o.logger.Printf("operation=%s status=%d error=%v duration=%s", operation, status, err, time.Since(started))
	}
}

func run() error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/users/42" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"42"}`)
	}))
	defer server.Close()

	raw, err := api.NewClientWithResponses(server.URL,
		api.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
	)
	if err != nil {
		return err
	}
	observer := logObserver{logger: log.New(os.Stdout, "", 0)}
	client := api.NewObservedClient(raw, observer)

	for _, id := range []string{"42", "missing"} {
		response, err := client.GetUserWithResponse(context.Background(), id, nil)
		if err != nil {
			return err
		}
		if response.JSON200 != nil {
			fmt.Printf("user=%s\n", response.JSON200.Id)
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
