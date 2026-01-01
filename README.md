# inject

The `inject` package provides a powerful and intuitive dependency injection framework for Go applications. It features automatic dependency resolution, lifecycle management, and thread-safe operations, making it ideal for building scalable applications with clean architecture.

## Features

- `Provide`: Used to provide dependencies through a function.
- `Invoke`: Used to resolve dependencies and invoke a function with them.
- `Resolve`: Used to resolve a dependency by its type.
- `Apply`: Used to apply dependencies to a struct.
- `Build`: Used to eagerly build all provided dependencies.
- `SetParent`: Used to set the parent injector.
- `Element[T]`: Used to provide multiple values of the same type, resolved as `Slice[T]`.
- Thread safety ensured
- Supports injection through the `inject` tag of struct fields

## Usage

For complete usage examples of the basic inject functionality, see [example_test.go](./example_test.go).

## Lifecycle Management

The `lifecycle` subpackage provides a complete lifecycle management system that combines dependency injection with coordinated startup, monitoring, and shutdown of services and actors.

### Key Concepts

- **Actor**: Simple components with start/stop operations (databases, configurations, migrations)
- **Service**: Long-running components that can signal completion (HTTP servers, background workers)
- **Lifecycle**: Orchestrates multiple actors and services with dependency injection

### Core Features

- **Dependency Injection**: Built-in injector for managing dependencies
- **Coordinated Startup**: Actors start in registration order
- **Graceful Shutdown**: Actors stop in reverse order with timeout control
- **Service Monitoring**: Automatic monitoring of long-running services
- **Signal Handling**: Built-in OS signal handling for graceful shutdown
- **Readiness Probes**: Optional blocking until services are ready to serve traffic
- **Error Handling**: Comprehensive error handling and logging

### Basic Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"

    "github.com/pkg/errors"
    "github.com/theplant/inject/lifecycle"
)

func main() {
    if err := lifecycle.Serve(context.Background(),
        lifecycle.SetupSignal,

        func() *Config {
            return &Config{
                HTTPServerPort: 8080,
                DatabaseURL:    "postgres://localhost:5432/myapp",
                RPCServerURL:   "127.0.0.1:1088",
            }
        },

        CreateRPCClient,

        func(lc *lifecycle.Lifecycle, conf *Config) *Database {
            db := &Database{}
            lc.Add(lifecycle.NewFuncActor(
                func(_ context.Context) error {
                    log.Printf("Connecting to database: %s", conf.DatabaseURL)
                    return db.Connect()
                },
                func(_ context.Context) error {
                    return db.Close()
                },
            ).WithName("database"))
            return db
        },

        func(lc *lifecycle.Lifecycle, conf *Config, db *Database, rpcClient *RPCClient) *http.Server {
            mux := http.NewServeMux()
            mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
                fmt.Fprintf(w, "OK - DB Connected: %t, RPCClient Connected: %t", db.connected, rpcClient.connected)
            })

            addr := fmt.Sprintf(":%d", conf.HTTPServerPort)
            server := &http.Server{
                Addr:    addr,
                Handler: mux,
            }

            lc.Add(lifecycle.NewFuncService(func(ctx context.Context) error {
                log.Printf("Starting HTTP server on %s", addr)
                if err := server.ListenAndServe(); err != http.ErrServerClosed {
                    return err
                }
                return nil
            }).WithStop(server.Shutdown).WithName("http-server"))

            return server
        },
    ); err != nil {
        log.Fatal(err)
    }
}

type Config struct {
    HTTPServerPort int
    DatabaseURL    string
    RPCServerURL   string
}

type Database struct {
    connected bool
}

func (db *Database) Connect() error {
    db.connected = true
    log.Println("Database connected")
    return nil
}

func (db *Database) Close() error {
    db.connected = false
    log.Println("Database closed")
    return nil
}

type RPCClient struct {
    connected bool
}

func (c *RPCClient) Close() error {
    c.connected = false
    log.Println("RPC client disconnected")
    return nil
}

func DialContext(ctx context.Context, serverURL string) (*RPCClient, error) {
    if serverURL == "" {
        return nil, errors.New("server URL cannot be empty")
    }

    client := &RPCClient{
        connected: true,
    }

    log.Printf("RPC client connected to %s", serverURL)
    return client, nil
}

func CreateRPCClient(ctx context.Context, lc *lifecycle.Lifecycle, conf *Config) (*RPCClient, error) {
    client, err := DialContext(ctx, conf.RPCServerURL)
    if err != nil {
        return nil, err
    }

    lc.Add(lifecycle.NewFuncActor(
        nil,
        func(_ context.Context) error {
            return client.Close()
        },
    ).WithName("rpc-client"))

    return client, nil
}
```

### Advanced Usage

#### Manual Lifecycle Management

```go
type Config struct {
    Port        int
    DatabaseURL string
}

lc := lifecycle.New()

// Provide dependencies
err := lc.Provide(
    func() *Config {
        return &Config{
            Port:        8080,
            DatabaseURL: "postgres://localhost:5432/myapp",
        }
    },
    func(lc *lifecycle.Lifecycle, conf *Config) *Database {
        db := &Database{}
        lc.Add(lifecycle.NewFuncActor(
            func(_ context.Context) error {
                log.Printf("Connecting to database: %s", conf.DatabaseURL)
                return db.Connect()
            },
            func(_ context.Context) error {
                return db.Close()
            },
        ).WithName("database"))
        return db
    },
    setupHTTPServer,
    lifecycle.SetupSignal,
)

// Start all services
if err := lc.Serve(context.Background()); err != nil {
    log.Fatal(err)
}
```

#### Service Collections

You can group related setup functions:

```go
var setupHTTPServer = []any{
    func() *HTTPConfig { return &HTTPConfig{Port: 8080} },
    func(lc *lifecycle.Lifecycle, conf *HTTPConfig) *http.Server {
        server := &http.Server{Addr: fmt.Sprintf(":%d", conf.Port)}
        lc.Add(lifecycle.NewFuncService(func(ctx context.Context) error {
            return server.ListenAndServe()
        }).WithStop(server.Shutdown).WithName("http"))
        return server
    },
}

// Use in lifecycle
lc.Provide(setupHTTPServer)
```

#### Signal Handling

```go
import "syscall"

// Custom signals
lc.Provide(lifecycle.SetupSignalWith(syscall.SIGUSR1, syscall.SIGUSR2))

// Default signals (SIGINT, SIGTERM)
lc.Provide(lifecycle.SetupSignal)
```

#### Readiness Probe

The lifecycle supports optional readiness probes to block startup until services are ready. Use `FuncActor.WithReadiness()` to enable automatic readiness signaling - the probe is signaled when `Start()` completes:

```go
func SetupHTTPReadinessProbe(lc *lifecycle.Lifecycle, listener net.Listener) {
    addr := fmt.Sprintf("http://%s/health", listener.Addr().String())

    lc.Add(lifecycle.NewFuncActor(func(ctx context.Context) error {
        return WaitForReady(ctx, addr)
    }, nil).WithName("http-readiness").WithReadiness())
}
```

When using `lifecycle.Start()`, the lifecycle will block until all probes signal ready:

```go
lc, err := lifecycle.Start(context.Background(),
    SetupHTTPListener,
    SetupHTTPServer,
    SetupHTTPReadinessProbe,
)
```

If the `Actor.Start()` function returns an error, the probe signals failure and `lifecycle.Start()` returns that error.

#### Nested Lifecycle

Since `*lifecycle.Lifecycle` itself implements the `Service` interface, you can nest lifecycles to create modular subsystems:

```go
// Create a subsystem lifecycle for database-related services
func SetupDatabaseSubsystem(parent *lifecycle.Lifecycle, conf *DatabaseConfig) (*DatabaseSubsystem, error) {
    // Create a nested lifecycle
    sub, err := lifecycle.Provide(
        func() *DatabaseConfig { return conf },
        func(lc *lifecycle.Lifecycle, conf *DatabaseConfig) *Database {
            db := &Database{}
            lc.Add(lifecycle.NewFuncActor(
                func(_ context.Context) error { return db.Connect(conf.DatabaseURL) },
                func(_ context.Context) error { return db.Close() },
            ).WithName("database"))
            return db
        },
        func(lc *lifecycle.Lifecycle, db *Database) *MigrationRunner {
            runner := &MigrationRunner{db: db}
            lc.Add(lifecycle.NewFuncActor(
                func(_ context.Context) error { return runner.Run() },
                nil,
            ).WithName("migrations"))
            return runner
        },
    )
    if err != nil {
        return nil, err
    }

    // Add the nested lifecycle as a service to the parent
    parent.Add(sub.WithName("database-subsystem"))

    return &DatabaseSubsystem{lifecycle: sub}, nil
}

// Main application
func main() {
    if err := lifecycle.Serve(context.Background(),
        lifecycle.SetupSignal,
        func() *Config { return loadConfig() },
        func(conf *Config) *DatabaseConfig { return conf.DatabaseConfig },
        SetupDatabaseSubsystem,  // Nested lifecycle
        SetupHTTPServer,
    ); err != nil {
        log.Fatal(err)
    }
}
```

Benefits of nested lifecycles:

- **Modularity**: Group related services into self-contained subsystems
- **Isolation**: Each subsystem has its own dependency injection scope
- **Coordinated shutdown**: Parent lifecycle manages shutdown of all nested lifecycles

### Configuration

#### Timeouts

```go
import "time"

lc := lifecycle.New().
    WithStopTimeout(60 * time.Second).     // Total shutdown timeout
    WithStopEachTimeout(10 * time.Second)  // Per-actor shutdown timeout
```

#### Custom Logging

```go
import (
    "log/slog"
    "os"
)

logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
lc := lifecycle.New().WithLogger(logger)
```

### Error Handling and Monitoring

The lifecycle system provides comprehensive monitoring:

- **Startup Errors**: Any actor failing to start stops the entire lifecycle
- **Service Monitoring**: Long-running services are monitored for completion or errors
- **Graceful Shutdown**: When any service completes or signal is received, all actors are stopped in reverse order
- **Timeout Handling**: Configurable timeouts for shutdown operations
- **Stop Cause**: Context includes information about why shutdown was initiated

```go
// Access stop cause in actor shutdown
func(ctx context.Context) error {
    cause := lifecycle.GetStopCause(ctx)
    if cause != nil {
        log.Printf("Shutting down due to: %v", cause)
    }
    return cleanup()
}
```

## Element - Multiple Values of Same Type

The `Element[T]` type allows you to provide multiple values of the same type `T`. When resolving `Slice[T]`, all `*Element[T]` values are automatically collected. Use `NewElement()` to create elements conveniently.

```go
inj := inject.New()

err := inj.Provide(
    func() *Config { return &Config{Prefix: "api-"} },
    // Multiple *Element[T] providers with dependencies
    func(cfg *Config) *inject.Element[string] {
        return inject.NewElement(cfg.Prefix + "route1")
    },
    func(cfg *Config) *inject.Element[string] {
        return inject.NewElement(cfg.Prefix + "route2")
    },
)

// Resolve as Slice[T] (which is []T)
var routes inject.Slice[string]
inj.Resolve(&routes) // routes = []string{"api-route1", "api-route2"}
```

**Note**: `*Element[T]` must be a pointer type. `Slice[T]` is a slice type alias (`type Slice[T any] []T`), so you can use it directly as a slice.

### Nil Element Handling

When resolving `Slice[T]`, the following behaviors apply:

- **`nil *Element[T]`**: If a provider returns `nil` (i.e., `return nil` instead of `return NewElement(...)`), the nil element is **skipped** and not included in the resulting slice.
- **`*Element[T]` with nil `Value`**: If a provider returns a valid `*Element[T]` but with a nil `Value` (e.g., `return NewElement[*Service](nil)`), the nil value **is included** in the resulting slice.

```go
// Example: nil *Element[T] is skipped
inj.Provide(
    func() *inject.Element[string] { return inject.NewElement("first") },
    func() *inject.Element[string] { return nil },  // Skipped
    func() *inject.Element[string] { return inject.NewElement("third") },
)
var strs inject.Slice[string]
inj.Resolve(&strs) // strs = []string{"first", "third"}

// Example: *Element[T] with nil Value is included
inj.Provide(
    func() *inject.Element[*Service] { return inject.NewElement(&Service{Name: "valid"}) },
    func() *inject.Element[*Service] { return inject.NewElement[*Service](nil) },  // Included as nil
    func() *inject.Element[*Service] { return inject.NewElement(&Service{Name: "another"}) },
)
var services inject.Slice[*Service]
inj.Resolve(&services) // services = []*Service{&Service{...}, nil, &Service{...}}
```

## Void Constructors

The `Void` type allows you to register constructors that don't return any value (or only return `error`). These are useful for "side-effect only" operations like modifying configuration or performing initialization tasks.

When a constructor has no return type (or only returns `error`), it is automatically treated as returning `*Element[*Void]`.

```go
inj := inject.New()

// Provide a config
err := inj.Provide(func() *Config {
    return &Config{Debug: false}
})

// Void constructor - modifies config as side effect
err = inj.Provide(func(cfg *Config) {
    cfg.Debug = true  // Modify config
})

// Execute all constructors via Build
err = inj.Build()

// Or resolve explicitly to trigger void constructors
var voids inject.Slice[*inject.Void]
inj.Resolve(&voids)
```

**Key behaviors:**

- **Lazy execution**: Void constructors are not executed until `Build()` is called or `Slice[*Void]` is resolved
- **Single execution**: Each void constructor is executed only once (like all other constructors)
- **Dependency injection**: Void constructors can have dependencies injected as parameters
- **Error handling**: Void constructors can return `error` as their only return type

```go
// Void constructor with error handling
inj.Provide(func(cfg *Config) error {
    if cfg.DatabaseURL == "" {
        return errors.New("database URL is required")
    }
    return nil
})
```

## Important Notes

### Special Parameter Types

The inject package handles certain parameter types in special ways:

- **`context.Context`**: Not managed by the injector. Cannot be used as a return type. This is the context passed to dependency resolution methods (like `InvokeContext`, `ResolveContext`) and will be automatically passed to constructor functions that declare it as a parameter.

- **`error`**: Not managed by the injector. Can only be used as a return type, and must be the last return type if present. If a constructor returns an error, the injection will fail and propagate the error.

### Constructor Function Rules

- Constructor functions can have multiple return values, but error must be the last one if present
- All non-error return values will be registered as available dependencies
- Constructor functions are called lazily when their return types are needed
- Use `Build()` or `BuildContext()` to eagerly instantiate all dependencies

### Error Handling

```go
// Constructor with error handling
func NewDatabase(conf *Config) (*Database, error) {
    db, err := sql.Open("postgres", conf.DatabaseURL)
    if err != nil {
        return nil, errors.Wrap(err, "failed to open database")
    }
    return &Database{db: db}, nil
}

// context.Context comes from the calling context (e.g., injector.InvokeContext(ctx, ...))
func NewRPCClient(ctx context.Context, conf *Config) (*RPCClient, error) {
    client, err := rpc.DialContext(ctx, conf.ServerURL)
    if err != nil {
        return nil, errors.Wrap(err, "failed to dial RPC server")
    }
    return client, nil
}
```
