# oapi-codegen Observer example

A copyable Go example that uses custom [oapi-codegen templates](https://github.com/oapi-codegen/oapi-codegen/tree/v2.8.0#custom-code-generation)
to wrap generated `WithResponse` methods with your own `Observer`. Use the hook
for metrics, tracing, logging, or other per-operation work.

## Run

Requires Go 1.25+ and Make. The first run downloads Go dependencies.

```sh
make run   # Generate the client and run the local example
make test  # Regenerate, vet, and run tests with the race detector
```

The example starts a local HTTP server and logs the operation, status, error,
and duration for a successful request and a 404. No external services are needed.
`make generate` runs only code generation, pinned to oapi-codegen v2.8.0.

## Reuse

Copy `templates/`, `upstream/client-with-responses.tmpl`, `upstream/LICENSE`,
`oapi-codegen.yaml`, and `generate.go` into your project. Set the package and
output path in the config, point the generation directive at your OpenAPI spec,
and keep the generator version aligned with the upstream template. Run
`go generate .` and `go mod tidy` to generate the client and resolve its runtime
dependency.

Implement this generated interface in your application:

```go
type Observer interface {
    Start(ctx context.Context, operation string) (
        context.Context,
        func(statusCode int, err error),
    )
}
```

Create the generated client, then wrap it with your observer:

```go
raw, err := api.NewClientWithResponses(baseURL)
if err != nil {
    return err
}
client := api.NewObservedClient(raw, observer)
response, err := client.GetUserWithResponse(ctx, "42", nil)
```

See [cmd/example/main.go](cmd/example/main.go) for a complete implementation.
Copy the code into your application; this repository is a snippet, not a library.

`Start` receives the operation ID and returns the context passed to the client
and a non-nil completion callback. Observers must support concurrent calls.
Each `WithResponse` call is observed once, including request preparation, body
reading, and response decoding. Status is `0` if no parsed response is available,
including decoding failures. HTTP 4xx/5xx responses can have a nil Go error;
the observer decides how to classify them. The wrapper preserves return values.

Generated with AI assistance. [MIT](LICENSE); the copied upstream template
retains its [Apache-2.0 license](upstream/LICENSE).
