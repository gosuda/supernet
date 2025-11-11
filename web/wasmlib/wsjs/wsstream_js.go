package wsjs

import (
	"sync"
)

// WsStream provides synchronized io.Reader and io.Writer interface implementations for WebSocket connections.
// It handles thread-safe reading and writing with proper buffering for messages that don't fit in a single read.
type WsStream struct {
	conn          *Conn
	currentBuffer []byte // Remaining bytes from the last message read that didn't fit in the buffer
	readMu        sync.Mutex  // Protects concurrent Read operations
	writeMu       sync.Mutex  // Protects concurrent Write operations
}

// NewWsStream creates a new WsStream adapter from an existing WebSocket connection.
// The returned WsStream implements io.ReadWriteCloser interface for convenient use with standard Go I/O libraries.
func NewWsStream(conn *Conn) *WsStream {
	return &WsStream{
		conn: conn,
	}
}

// Read implements the io.Reader interface by reading data from the WebSocket connection.
// If there is buffered data from a previous message, it returns that first before requesting new data.
// Returns the number of bytes read and any error encountered.
func (ws *WsStream) Read(p []byte) (n int, err error) {
	ws.readMu.Lock()
	defer ws.readMu.Unlock()

	// If we have remaining buffered data from a previous message, use it first to avoid data loss
	if len(ws.currentBuffer) > 0 {
		n = copy(p, ws.currentBuffer)
		ws.currentBuffer = ws.currentBuffer[n:]
		return n, nil
	}

	// Request the next message from the WebSocket connection
	msg, err := ws.conn.NextMessage()
	if err != nil {
		return 0, err
	}

	// Copy as much of the message as fits into the provided buffer
	n = copy(p, msg)

	// Store any remaining message data that didn't fit in the buffer for the next Read call
	if n < len(msg) {
		ws.currentBuffer = msg[n:]
	}

	return n, nil
}

// Write implements the io.Writer interface by sending data to the WebSocket connection.
// All bytes in the slice are sent together as a single message. Thread-safe for concurrent writes.
// Returns the number of bytes written (which is always len(p) on success) and any error encountered.
func (ws *WsStream) Write(p []byte) (n int, err error) {
	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()

	err = ws.conn.Send(p)
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

// Close closes the underlying WebSocket connection and releases associated resources.
// Subsequent Read and Write operations may fail after Close is called.
func (ws *WsStream) Close() error {
	return ws.conn.Close()
}
