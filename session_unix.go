//go:build !windows
// +build !windows

package sip

import (
	"fmt"
	"io"
	"os"

	xpty "github.com/charmbracelet/x/xpty"
)

// platformPty holds platform-specific PTY resources.
type platformPty struct {
	pty       xpty.Pty
	ptyMaster *os.File
	ptySlave  *os.File
}

// newPlatformPty creates a new PTY for Unix systems.
func newPlatformPty(cols, rows int) (*platformPty, error) {
	ptyInstance, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("failed to open PTY: %w", err)
	}

	unixPty, ok := ptyInstance.(*xpty.UnixPty)
	if !ok {
		_ = ptyInstance.Close()
		return nil, fmt.Errorf("expected UnixPty")
	}

	return &platformPty{
		pty:       ptyInstance,
		ptyMaster: unixPty.Master(),
		ptySlave:  unixPty.Slave(),
	}, nil
}

// Close closes all PTY resources.
func (p *platformPty) Close() error {
	if p.pty != nil {
		return p.pty.Close()
	}
	return nil
}

// Resize resizes the PTY.
func (p *platformPty) Resize(cols, rows int) error {
	if p.pty != nil {
		return p.pty.Resize(cols, rows)
	}
	return nil
}

// OutputReader returns an io.Reader for reading terminal output.
// On Unix, this reads from the PTY master.
func (p *platformPty) OutputReader() io.Reader {
	return p.ptyMaster
}

// InputWriter returns an io.Writer for writing terminal input.
// On Unix, this writes to the PTY master.
func (p *platformPty) InputWriter() io.Writer {
	return p.ptyMaster
}

// SlaveFile returns the PTY slave file for Bubble Tea.
// On Unix, this is the actual slave file.
func (p *platformPty) SlaveFile() *os.File {
	return p.ptySlave
}

// SlaveFd returns the file descriptor of the slave PTY.
func (p *platformPty) SlaveFd() uintptr {
	if p.ptySlave != nil {
		return p.ptySlave.Fd()
	}
	return 0
}

// SlaveReader returns the reader for Bubble Tea input.
// On Unix, this is the PTY slave file.
func (p *platformPty) SlaveReader() io.Reader {
	return p.ptySlave
}

// SlaveWriter returns the writer for Bubble Tea output.
// On Unix, this is the PTY slave file.
func (p *platformPty) SlaveWriter() io.Writer {
	return p.ptySlave
}
