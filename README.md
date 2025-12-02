# Sip - Serve Bubble Tea Apps Through the Browser

> Drinking tea through the browser

**Status:** Planning/Design Phase

Sip will be a Go library that allows you to serve any [Bubble Tea](https://github.com/charmbracelet/bubbletea) application through a web browser with full terminal emulation, mouse support, and hardware-accelerated rendering.

## Vision

Extract the web terminal functionality from [TUIOS](https://github.com/Gaurav-Gosain/tuios) into a reusable library that any Bubble Tea developer can use to make their TUI applications accessible through a web browser.

## Planned Features

- **WebGL Rendering** - GPU-accelerated terminal rendering via xterm.js for smooth 60fps
- **Dual Protocol Support** - WebTransport (HTTP/3 over QUIC) with automatic WebSocket fallback  
- **Embedded Assets** - All static files (HTML, CSS, JS, fonts) bundled in the binary via go:embed
- **Bundled Nerd Fonts** - JetBrains Mono Nerd Font included, no client-side installation needed
- **Session Management** - Handle multiple concurrent users with isolated sessions
- **Mouse Support** - Full mouse interaction with cell-based event deduplication
- **Auto-Reconnect** - Automatic reconnection with exponential backoff
- **Pure Go** - No CGO dependencies, statically compiled binaries
- **Zero Configuration** - Works out of the box with sensible defaults

## Planned API

```go
package main

import (
    "context"
    tea "github.com/charmbracelet/bubbletea/v2"
    "github.com/Gaurav-Gosain/sip"
)

func main() {
    config := sip.DefaultConfig()
    config.Port = "8080"
    
    server := sip.NewServer(config)
    server.Serve(context.Background(), func() tea.Model {
        return myBubbleTeaModel{}
    })
}
```

## Current Status

Sip is currently in the planning phase. The web terminal functionality exists in [tuios-web](https://github.com/Gaurav-Gosain/tuios) and will be extracted into this library once the API is stabilized.

## Roadmap

1. **Phase 1: Extraction** (Current)
   - Extract web functionality from TUIOS into tuios-web binary
   - Identify reusable components
   - Design public API

2. **Phase 2: Library Development**
   - Create standalone sip repository  
   - Implement core server functionality
   - Add comprehensive tests
   - Write documentation and examples

3. **Phase 3: Integration**
   - Update tuios-web to use sip library
   - Create example apps
   - Publish to GitHub
   - Announce to Charm community

4. **Phase 4: Community**
   - Gather feedback from Bubble Tea developers
   - Add requested features
   - Create more examples
   - Build ecosystem integrations

## Use Cases

### Remote Access
Make any TUI application accessible through a web browser

### Live Demos
Create interactive documentation for your TUI apps with read-only mode

### Development Tools
Transform CLI dev tools into web-accessible dashboards

## Related Projects

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - The TUI framework Sip is built for
- [TUIOS](https://github.com/Gaurav-Gosain/tuios) - Terminal window manager where Sip originated
- [xterm.js](https://xtermjs.org/) - Terminal emulator used in the browser
- [ttyd](https://github.com/tsl0922/ttyd) - Share terminal over the web (C implementation)

## Contributing

This project is in early planning stages. Ideas and discussions are welcome! Please open an issue to share your thoughts on the API design or features you'd like to see.

## License

MIT License - see [LICENSE](LICENSE) for details

## Acknowledgments

Sip is being developed as part of the TUIOS project and builds on the excellent work of the Charm team and the xterm.js community.
