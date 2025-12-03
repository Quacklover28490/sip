package sip

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/webtransport-go"
)

// Message types for WebSocket/WebTransport communication.
const (
	MsgInput   = '0' // Terminal input (client -> server)
	MsgOutput  = '1' // Terminal output (server -> client)
	MsgResize  = '2' // Resize terminal
	MsgPing    = '3' // Ping
	MsgPong    = '4' // Pong
	MsgTitle   = '5' // Set window title
	MsgOptions = '6' // Configuration options
	MsgClose   = '7' // Session closed (server -> client)
)

const (
	readBufSize  = 16 * 1024
	writeBufSize = 16*1024 + 5
)

var (
	readBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, readBufSize)
			return &b
		},
	}
	writeBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, writeBufSize)
			return &b
		},
	}
	smallBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, 256)
			return &b
		},
	}
)

// ResizeMessage is sent when the terminal should be resized.
type ResizeMessage struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// OptionsMessage is sent to configure the terminal.
type OptionsMessage struct {
	ReadOnly bool `json:"readOnly"`
}

// internalSession is the interface that both webSession and cmdSession implement
// for use by the HTTP handlers.
type internalSession interface {
	OutputReader() io.Reader
	InputWriter() io.Writer
	Resize(cols, rows int)
	Done() <-chan struct{}
}

// sessionInfo holds common session metadata for logging.
type sessionInfo struct {
	id   string
	cols int
	rows int
}

func (s *httpServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.checkConnectionLimit() {
		http.Error(w, "Maximum connections reached", http.StatusServiceUnavailable)
		return
	}
	defer s.releaseConnection()

	logger.Info("WebSocket connection attempt",
		"remote", r.RemoteAddr,
		"user_agent", r.UserAgent(),
	)

	opts := &websocket.AcceptOptions{
		OriginPatterns: s.config.AllowOrigins,
	}
	if len(s.config.AllowOrigins) == 0 {
		opts.OriginPatterns = []string{"*"}
	}

	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		logger.Error("WebSocket accept failed", "err", err, "remote", r.RemoteAddr)
		return
	}
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	cols, rows := 80, 24
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	_, data, err := conn.Read(readCtx)
	readCancel()

	if err == nil && len(data) > 0 && data[0] == MsgResize {
		var resize ResizeMessage
		if err := json.Unmarshal(data[1:], &resize); err == nil {
			cols = resize.Cols
			rows = resize.Rows
			logger.Debug("got initial size from browser", "cols", cols, "rows", rows)
		}
	}

	startTime := time.Now()

	// Determine session type based on mode (command or Bubble Tea)
	var session internalSession
	var info sessionInfo
	var closeFunc func()

	if s.cmdHandler != nil {
		// Command mode: spawn external command
		cmdSess, err := s.createCmdSession(ctx, cols, rows)
		if err != nil {
			logger.Error("command session creation failed", "err", err, "remote", r.RemoteAddr)
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		session = cmdSess
		info = sessionInfo{id: cmdSess.id, cols: cmdSess.cols, rows: cmdSess.rows}
		closeFunc = func() { s.closeCmdSession(cmdSess) }
	} else {
		// Bubble Tea mode: run in-process
		webSess, err := s.createSession(ctx, s.handler, cols, rows)
		if err != nil {
			logger.Error("session creation failed", "err", err, "remote", r.RemoteAddr)
			_ = conn.Close(websocket.StatusInternalError, err.Error())
			return
		}
		session = webSess
		info = sessionInfo{id: webSess.id, cols: webSess.cols, rows: webSess.rows}
		closeFunc = func() { s.closeSession(webSess) }
	}

	defer func() {
		closeFunc()
		logger.Info("WebSocket session ended",
			"session", info.id,
			"remote", r.RemoteAddr,
			"duration", time.Since(startTime).Round(time.Second),
		)
	}()

	logger.Info("WebSocket session started",
		"session", info.id,
		"remote", r.RemoteAddr,
		"cols", info.cols,
		"rows", info.rows,
	)

	optionsData, _ := json.Marshal(OptionsMessage{ReadOnly: s.config.ReadOnly})
	_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{MsgOptions}, optionsData...))

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer cancel()
		s.streamOutputToWebSocket(ctx, conn, session, info)
	}()

	go func() {
		defer wg.Done()
		defer cancel()
		s.handleWebSocketInput(ctx, conn, session, info)
	}()

	wg.Wait()
}

func (s *httpServer) handleWebTransport(w http.ResponseWriter, r *http.Request) {
	if !s.checkConnectionLimit() {
		http.Error(w, "Maximum connections reached", http.StatusServiceUnavailable)
		return
	}
	defer s.releaseConnection()

	logger.Info("WebTransport connection attempt",
		"remote", r.RemoteAddr,
		"protocol", r.Proto,
	)

	wtSession, err := s.wtServer.Upgrade(w, r)
	if err != nil {
		logger.Error("WebTransport upgrade failed", "err", err, "remote", r.RemoteAddr)
		return
	}
	defer func() { _ = wtSession.CloseWithError(0, "session closed") }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stream, err := wtSession.AcceptStream(ctx)
	if err != nil {
		logger.Error("stream accept failed", "err", err)
		return
	}
	defer func() { _ = stream.Close() }()

	cols, rows := 80, 24
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(stream, lenBuf); err == nil {
		length := binary.BigEndian.Uint32(lenBuf)
		if length < 1024 {
			data := make([]byte, length)
			if _, err := io.ReadFull(stream, data); err == nil && len(data) > 0 && data[0] == MsgResize {
				var resize ResizeMessage
				if err := json.Unmarshal(data[1:], &resize); err == nil {
					cols = resize.Cols
					rows = resize.Rows
					logger.Debug("got initial size from browser (WT)", "cols", cols, "rows", rows)
				}
			}
		}
	}

	startTime := time.Now()

	// Determine session type based on mode (command or Bubble Tea)
	var session internalSession
	var info sessionInfo
	var closeFunc func()

	if s.cmdHandler != nil {
		// Command mode: spawn external command
		cmdSess, err := s.createCmdSession(ctx, cols, rows)
		if err != nil {
			logger.Error("command session creation failed", "err", err, "remote", r.RemoteAddr)
			return
		}
		session = cmdSess
		info = sessionInfo{id: cmdSess.id, cols: cmdSess.cols, rows: cmdSess.rows}
		closeFunc = func() { s.closeCmdSession(cmdSess) }
	} else {
		// Bubble Tea mode: run in-process
		webSess, err := s.createSession(ctx, s.handler, cols, rows)
		if err != nil {
			logger.Error("session creation failed", "err", err, "remote", r.RemoteAddr)
			return
		}
		session = webSess
		info = sessionInfo{id: webSess.id, cols: webSess.cols, rows: webSess.rows}
		closeFunc = func() { s.closeSession(webSess) }
	}

	defer func() {
		closeFunc()
		logger.Info("WebTransport session ended",
			"session", info.id,
			"remote", r.RemoteAddr,
			"duration", time.Since(startTime).Round(time.Second),
		)
	}()

	logger.Info("WebTransport session started",
		"session", info.id,
		"remote", r.RemoteAddr,
		"cols", info.cols,
		"rows", info.rows,
	)

	optionsData, _ := json.Marshal(OptionsMessage{ReadOnly: s.config.ReadOnly})
	_ = writeFramed(stream, append([]byte{MsgOptions}, optionsData...))

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer cancel()
		s.streamOutputToWebTransport(ctx, stream, session, info)
	}()

	go func() {
		defer wg.Done()
		defer cancel()
		s.handleWebTransportInput(ctx, stream, session, info)
	}()

	wg.Wait()
	<-wtSession.Context().Done()
}

func (s *httpServer) streamOutputToWebSocket(ctx context.Context, conn *websocket.Conn, session internalSession, info sessionInfo) {
	bufPtr := readBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer readBufPool.Put(bufPtr)

	msgPtr := writeBufPool.Get().(*[]byte)
	msg := *msgPtr
	msg[0] = MsgOutput
	defer writeBufPool.Put(msgPtr)

	var totalBytes int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebSocket output stopped (context)", "session", info.id, "bytes_sent", totalBytes)
			return
		case <-session.Done():
			logger.Debug("session ended, sending close", "session", info.id)
			_ = conn.Write(ctx, websocket.MessageBinary, []byte{MsgClose})
			return
		default:
		}

		n, err := session.OutputReader().Read(buf)
		if err != nil {
			logger.Debug("output closed", "session", info.id, "bytes_sent", totalBytes, "error", err)
			_ = conn.Write(ctx, websocket.MessageBinary, []byte{MsgClose})
			return
		}
		if n == 0 {
			continue
		}

		if totalBytes == 0 {
			logger.Debug("first output received", "session", info.id, "bytes", n)
		}

		totalBytes += int64(n)
		copy(msg[1:], buf[:n])
		if err := conn.Write(ctx, websocket.MessageBinary, msg[:n+1]); err != nil {
			logger.Debug("WebSocket write error", "session", info.id, "err", err)
			return
		}
	}
}

func (s *httpServer) streamOutputToWebTransport(ctx context.Context, stream *webtransport.Stream, session internalSession, info sessionInfo) {
	bufPtr := readBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer readBufPool.Put(bufPtr)

	framePtr := writeBufPool.Get().(*[]byte)
	frame := *framePtr
	defer writeBufPool.Put(framePtr)

	var totalBytes int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebTransport output stopped (context)", "session", info.id, "bytes_sent", totalBytes)
			return
		case <-session.Done():
			logger.Debug("session ended, sending close", "session", info.id)
			_ = writeFramed(stream, []byte{MsgClose})
			return
		default:
		}

		n, err := session.OutputReader().Read(buf)
		if err != nil {
			logger.Debug("output closed", "session", info.id, "bytes_sent", totalBytes, "error", err)
			_ = writeFramed(stream, []byte{MsgClose})
			return
		}
		if n == 0 {
			continue
		}

		if totalBytes == 0 {
			debugBytes := n
			if debugBytes > 100 {
				debugBytes = 100
			}
			logger.Debug("first output received (WT)", "session", info.id, "bytes", n, "first_bytes", fmt.Sprintf("%q", string(buf[:debugBytes])))
		}

		totalBytes += int64(n)

		msgLen := n + 1
		binary.BigEndian.PutUint32(frame[0:4], uint32(msgLen))
		frame[4] = MsgOutput
		copy(frame[5:], buf[:n])

		if _, err := stream.Write(frame[:5+n]); err != nil {
			logger.Debug("WebTransport write error", "session", info.id, "err", err)
			return
		}
	}
}

func (s *httpServer) handleWebSocketInput(ctx context.Context, conn *websocket.Conn, session internalSession, info sessionInfo) {
	var totalBytes int64
	var msgCount int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebSocket input stopped", "session", info.id, "messages", msgCount, "bytes", totalBytes)
			return
		case <-session.Done():
			return
		default:
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		totalBytes += int64(len(data))
		msgCount++
		s.processInput(data, session, info)
	}
}

func (s *httpServer) handleWebTransportInput(ctx context.Context, stream *webtransport.Stream, session internalSession, info sessionInfo) {
	lenBuf := make([]byte, 4)
	var totalBytes int64
	var msgCount int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebTransport input stopped", "session", info.id, "messages", msgCount, "bytes", totalBytes)
			return
		case <-session.Done():
			return
		default:
		}

		if _, err := io.ReadFull(stream, lenBuf); err != nil {
			return
		}

		length := binary.BigEndian.Uint32(lenBuf)
		if length > 1024*1024 {
			logger.Warn("message too large", "session", info.id, "size", length)
			return
		}

		var msg []byte
		if length <= 256 {
			bufPtr := smallBufPool.Get().(*[]byte)
			msg = (*bufPtr)[:length]
			defer smallBufPool.Put(bufPtr)
		} else {
			msg = make([]byte, length)
		}

		if _, err := io.ReadFull(stream, msg); err != nil {
			return
		}

		totalBytes += int64(length)
		msgCount++
		s.processInput(msg, session, info)
	}
}

func (s *httpServer) processInput(data []byte, session internalSession, info sessionInfo) {
	if len(data) == 0 {
		return
	}

	msgType := data[0]
	payload := data[1:]

	switch msgType {
	case MsgInput:
		if !s.config.ReadOnly {
			_, _ = session.InputWriter().Write(payload)
		}

	case MsgResize:
		var resize ResizeMessage
		if err := json.Unmarshal(payload, &resize); err != nil {
			logger.Warn("invalid resize message", "session", info.id, "err", err)
			return
		}
		session.Resize(resize.Cols, resize.Rows)
		logger.Debug("terminal resized",
			"session", info.id,
			"to", []int{resize.Cols, resize.Rows},
		)

	case MsgPing:
		// Pong handled at transport layer
	}
}

func writeFramed(w io.Writer, msg []byte) error {
	frame := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(msg)))
	copy(frame[4:], msg)
	_, err := w.Write(frame)
	return err
}

func (s *httpServer) incrementConnCount() int32 {
	return atomic.AddInt32(&s.connCount, 1)
}

func (s *httpServer) decrementConnCount() int32 {
	return atomic.AddInt32(&s.connCount, -1)
}
