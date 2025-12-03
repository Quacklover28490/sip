# AGENTS.md - Sip

> Sip is a Go library for serving Bubble Tea TUI applications through web browsers with full terminal emulation.

## Quick Reference

```bash
# Build library and CLI
go build ./...

# Run example (Bubble Tea mode)
go run ./examples/simple

# Run CLI (command mode)
go run ./cmd/sip -- htop
go run ./cmd/sip -p 8080 -- claude -c
go run ./cmd/sip --host 0.0.0.0 -- bash

# Then open http://localhost:7681 in browser
```

## CLI Usage

Built with Cobra + Fang (same pattern as tuios). Supports shell completion generation.

```bash
sip [flags] -- command [args...]

Flags:
  -H, --host string   Host to bind to (default "localhost")
  -p, --port string   Port to listen on (default "7681")
  -d, --dir string    Working directory for the command
      --debug         Enable debug logging
  -v, --version       Print version and exit
  -h, --help          Show help

Commands:
  completion          Generate shell completion scripts
  help                Help about any command
```

Examples:
- `sip -- htop` - Run htop in browser
- `sip -p 8080 -- claude -c` - Run Claude CLI on port 8080
- `sip --host 0.0.0.0 -- bash` - Expose bash on all interfaces
- `sip completion bash > ~/.bash_completion.d/sip` - Install bash completion

## Project Structure

```
sip/
├── sip.go              # Main API: Session interface, Handler types, Config, MakeOptions
├── server.go           # HTTP server, static file serving, WebTransport setup
├── session.go          # webSession implementation, session lifecycle
├── session_unix.go     # Unix PTY setup (uses xpty.UnixPty with master/slave)
├── session_windows.go  # Windows pipe-based I/O (ConPty not suitable for in-process)
├── cmd_session.go      # cmdSession for CLI mode (spawning external commands)
├── cmd_unix.go         # Unix command PTY spawning
├── cmd_windows.go      # Windows command PTY spawning
├── handlers.go         # WebSocket/WebTransport handlers, message processing
├── cert.go             # Self-signed certificate generation for WebTransport
├── cmd/
│   └── sip/            # CLI binary
│       └── main.go
├── static/             # Embedded web assets (go:embed)
│   ├── index.html
│   ├── terminal.js     # Browser-side terminal client (xterm.js integration)
│   ├── terminal.css
│   └── fonts/          # JetBrains Mono Nerd Font
└── examples/
    └── simple/         # Basic counter example (Bubble Tea mode)
```

## Architecture

### Two Operating Modes

1. **Library mode** (Bubble Tea): Run in-process Bubble Tea programs
   - Use `server.Serve()` or `server.ServeWithProgram()`
   - Handler creates tea.Model for each connection
   - PTY provides terminal semantics for Bubble Tea

2. **CLI mode** (Commands): Spawn external commands in PTY
   - Use `sip -- command [args...]` CLI
   - Calls `server.ServeCommand()`
   - xpty spawns command attached to PTY
   - Pure I/O forwarding between PTY and browser

### Communication Flow
1. Browser connects via WebSocket (HTTP/1.1) or WebTransport (HTTP/3 over QUIC)
2. Server creates a PTY (pseudo-terminal) for each session
3. Bubble Tea program runs attached to the PTY
4. Terminal I/O is bridged between PTY and browser via xterm.js

### Dual Protocol Support
- **WebTransport** (preferred): HTTP/3 over QUIC, lower latency, uses port+1 (default 7682)
- **WebSocket** (fallback): Works everywhere, default port 7681
- Browser automatically tries WebTransport first, falls back to WebSocket

### Message Protocol
Binary messages with type byte prefix (defined in `handlers.go`):
```go
MsgInput   = '0'  // Terminal input (client -> server)
MsgOutput  = '1'  // Terminal output (server -> client)
MsgResize  = '2'  // Resize terminal
MsgPing    = '3'  // Ping
MsgPong    = '4'  // Pong
MsgTitle   = '5'  // Set window title
MsgOptions = '6'  // Configuration options
MsgClose   = '7'  // Session closed
```

## Key Patterns

### Handler API (Wish-like)
Two handler patterns modeled after [Wish](https://github.com/charmbracelet/wish):

```go
// Simple handler - returns model and options
type Handler func(sess Session) (tea.Model, []tea.ProgramOption)

// Advanced handler - returns full tea.Program
type ProgramHandler func(sess Session) *tea.Program
```

### Session Interface
```go
type Session interface {
    Pty() Pty                           // Terminal dimensions
    Context() context.Context           // Cancelled on disconnect
    Read(p []byte) (n int, err error)   // Read from terminal
    Write(p []byte) (n int, err error)  // Write to terminal
    Fd() uintptr                        // File descriptor for TTY detection
    PtySlave() *os.File                 // PTY slave for Bubble Tea raw mode
    WindowChanges() <-chan WindowSize   // Resize events
}
```

### MakeOptions Helper
Always use `MakeOptions(sess)` when creating tea.Program to properly configure:
- PTY slave for input/output (required for raw mode)
- TrueColor profile
- Window size
- Environment variables (TERM=xterm-256color, COLORTERM=truecolor)
- Suspend message filter

## Code Conventions

### Go Code
- Standard Go formatting (gofmt)
- Package-level logger with `charmbracelet/log`
- Structured logging with key-value pairs
- Error wrapping with `fmt.Errorf("context: %w", err)`
- Context-based cancellation
- sync.Pool for buffer reuse in hot paths
- sync.Map for concurrent session storage

### Naming
- Exported types: `Session`, `Handler`, `ProgramHandler`, `Config`, `Server`
- Internal types: `httpServer`, `webSession`
- Constants: ALL_CAPS for message types (`MsgInput`, `MsgOutput`)

### Error Handling
- Return errors up to caller, don't log and return
- Use `logger.Error/Warn/Debug` for operational logs
- Graceful degradation (WebTransport -> WebSocket fallback)

### Frontend (terminal.js)
- IIFE pattern to avoid global pollution
- Class-based structure (`TuiosTerminal`)
- Pre-allocated buffers for performance
- requestAnimationFrame for batched terminal writes
- Mouse event deduplication (cell-based)

## Dependencies

### Core
- `bubbletea/v2` - TUI framework (beta version)
- `lipgloss/v2` - Styling (beta version)
- `x/xpty` - Cross-platform PTY
- `colorprofile` - Terminal color detection
- `log` - Structured logging

### Transport
- `coder/websocket` - WebSocket implementation
- `quic-go/quic-go` - QUIC protocol
- `quic-go/webtransport-go` - WebTransport

### Frontend
- xterm.js with addons (fit, webgl, canvas, web-links)
- JetBrains Mono Nerd Font (embedded)

## Important Notes

### PTY Handling
- **Unix**: Uses `xpty.UnixPty` with separate master/slave files
  - Master used by handlers for reading output/writing input
  - Slave passed to Bubble Tea for terminal I/O with raw mode support
- **Windows**: Uses `io.Pipe()` for bidirectional communication
  - ConPty is designed for spawning child processes, not in-process use
  - Pipes provide equivalent I/O without terminal semantics
  - Resize handled via `tea.WindowSizeMsg` (same as Unix)
- Session owns PTY/pipe lifecycle - closing session closes resources

### Certificate Generation
- Self-signed certificates generated on startup for WebTransport
- Valid for 10 days (Chrome requires < 14 days for serverCertificateHashes)
- Certificate hash exposed via `/cert-hash` endpoint for browser verification

### Embedded Assets
- Static files embedded via `//go:embed static/*`
- Served from memory, no filesystem dependencies at runtime
- Fonts cached for 1 year, other assets served fresh

### Concurrency
- Each connection spawns two goroutines (input/output streaming)
- Sessions stored in sync.Map
- Connection limiting via atomic counter

## Gotchas

1. **Bubble Tea v2 beta**: Uses pre-release bubbletea/v2 and lipgloss/v2 - API may change
2. **Port +1 for WebTransport**: If HTTP is on 7681, WebTransport is on 7682
4. **Logger colors disabled**: Logger explicitly disables ANSI to prevent escape sequences leaking to terminal
5. **Suspend filtered**: `tea.SuspendMsg` converted to `tea.ResumeMsg` (no suspend in browser)
6. **colorprofile warning**: go.mod shows it should be direct dependency - run `go mod tidy` if you see warnings
