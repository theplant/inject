# inject

The `inject` package provides a powerful and intuitive dependency injection framework for Go applications. It features automatic dependency resolution, lifecycle management, and thread-safe operations, making it ideal for building scalable applications with clean architecture.

## Features

- `Provide`: Used to provide dependencies through a function.
- `Invoke`: Used to resolve dependencies and invoke a function with them.
- `Resolve`: Used to resolve a dependency by its type.
- `Apply`: Used to apply dependencies to a struct.
- `Build`: Used to eagerly build all provided dependencies.
- `SetParent`: Used to set the parent injector.
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
- **Error Handling**: Comprehensive error handling and logging

### Basic Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"

    "github.com/theplant/inject/lifecycle"
)

// Example database connection
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

func main() {
    if err := lifecycle.Serve(context.Background(),
        // Signal handling for graceful shutdown
        lifecycle.SetupSignal,

        // Simple actor: database setup
        func(lc *lifecycle.Lifecycle) *Database {
            db := &Database{}
            lc.Add(lifecycle.NewFuncActor(
                func(ctx context.Context) error {
                    return db.Connect()
                },
                func(ctx context.Context) error {
                    return db.Close()
                },
            ).WithName("database"))
            return db
        },

        // Long-running service: HTTP server
        func(lc *lifecycle.Lifecycle, db *Database) *http.Server {
            mux := http.NewServeMux()
            mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
                fmt.Fprintf(w, "OK - DB Connected: %t", db.connected)
            })

            server := &http.Server{
                Addr:    ":8080",
                Handler: mux,
            }

            lc.Add(lifecycle.NewFuncService(func(ctx context.Context) error {
                log.Println("Starting HTTP server on :8080")
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
    func(conf *Config, lc *lifecycle.Lifecycle) *Database {
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

#### Signal Handling

```go
import "syscall"

// Custom signals
lc.Provide(lifecycle.SetupSignalWith(syscall.SIGUSR1, syscall.SIGUSR2))

// Default signals (SIGINT, SIGTERM)
lc.Provide(lifecycle.SetupSignal)
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

## Important Notes

### Special Parameter Types

The inject package handles certain parameter types in special ways:

- **`inject.Context`**: Not managed by the injector. This is a context type that you pass to dependency resolution methods and constructor functions to provide context for the dependency resolution process.

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

// Using inject.Context for dependency resolution context
func NewRPCClient(ctx inject.Context, conf *Config) (*RPCClient, error) {
    // ctx provides context for the dependency resolution process
    client, err := rpc.DialContext(ctx, conf.ServerURL)
    if err != nil {
        return nil, errors.Wrap(err, "failed to dial RPC server")
    }
    return client, nil
}
```
