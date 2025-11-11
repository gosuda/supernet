package wsjs

import (
	"errors"
	"syscall/js"
)

var (
	// ErrFailedToDial is returned when the WebSocket connection fails to establish
	ErrFailedToDial = errors.New("failed to dial websocket")
	// ErrClosed is returned when attempting to use a closed WebSocket connection
	ErrClosed = errors.New("websocket connection closed")
)

var (
	// _WebSocket is a cached reference to the JavaScript WebSocket constructor for creating connections
	_WebSocket = js.Global().Get("WebSocket")
	// _ArrayBuffer is a cached reference to the JavaScript ArrayBuffer constructor for binary data
	_ArrayBuffer = js.Global().Get("ArrayBuffer")
	// _Uint8Array is a cached reference to the JavaScript Uint8Array constructor for typed array operations
	_Uint8Array = js.Global().Get("Uint8Array")
)

// Conn represents a managed WebSocket connection with proper resource cleanup.
// It handles both text and binary messages, converting them to Go byte slices for consumption.
type Conn struct {
	// ws holds the JavaScript WebSocket object
	ws js.Value

	// messageChan buffers incoming messages from the WebSocket (up to 128 messages)
	messageChan chan []byte
	// closeChan signals when the WebSocket connection has been closed
	closeChan chan struct{}

	// funcsToBeReleased tracks JavaScript function callbacks that must be released to prevent memory leaks
	funcsToBeReleased []js.Func
}

// freeFuncs releases all registered JavaScript function callbacks to allow garbage collection.
// This must be called when the connection is closed to prevent memory leaks.
func (conn *Conn) freeFuncs() {
	for _, f := range conn.funcsToBeReleased {
		f.Release()
	}
}

// Dial establishes a WebSocket connection to the specified URI.
// Returns a Conn ready for use or an error if the connection fails.
// The connection is ready for receiving and sending messages after this call succeeds.
func Dial(uri string) (*Conn, error) {
	errCh := make(chan error, 1)

	ws := _WebSocket.New(uri)
	ws.Set("binaryType", "arraybuffer")

	conn := &Conn{
		ws:          ws,
		messageChan: make(chan []byte, 128),
		closeChan:   make(chan struct{}, 1),
	}

	onOpen := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		errCh <- nil
		return nil
	})

	onError := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		errCh <- ErrFailedToDial
		return nil
	})

	onMessage := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		jsData := args[0].Get("data")
		if jsData.Type() == js.TypeString {
			// Handle text frame: convert JavaScript string to Go byte slice
			data := []byte(jsData.String())
			conn.messageChan <- data
		} else if jsData.InstanceOf(_ArrayBuffer) {
			// Handle binary frame: convert JavaScript ArrayBuffer to Go byte slice
			array := _Uint8Array.New(jsData)
			byteLength := array.Get("byteLength").Int()
			data := make([]byte, byteLength)
			js.CopyBytesToGo(data, array)
			conn.messageChan <- data
		}

		return nil
	})

	onClose := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		close(conn.closeChan)
		return nil
	})

	conn.funcsToBeReleased = append(conn.funcsToBeReleased, onOpen, onError, onMessage, onClose)

	conn.ws.Call("addEventListener", "open", onOpen)
	conn.ws.Call("addEventListener", "error", onError)
	conn.ws.Call("addEventListener", "message", onMessage)
	conn.ws.Call("addEventListener", "close", onClose)

	err := <-errCh
	if err != nil {
		conn.freeFuncs()
		return nil, err
	}

	return conn, nil
}

// Close closes the WebSocket connection and releases all associated resources.
// It waits for the close event to be received before returning.
// Subsequent calls to Close are safe and will not cause errors.
func (conn *Conn) Close() error {
	conn.ws.Call("close")
	<-conn.closeChan
	conn.freeFuncs()
	return nil
}

// NextMessage retrieves the next message from the WebSocket connection.
// It blocks until a message is available or the connection is closed.
// Returns ErrClosed if the connection has been closed before or during the wait.
func (conn *Conn) NextMessage() ([]byte, error) {
	select {
	case msg := <-conn.messageChan:
		return msg, nil
	case <-conn.closeChan:
		return nil, ErrClosed
	}
}

// Send sends a message to the WebSocket connection as binary data.
// The provided byte slice is converted to a JavaScript ArrayBuffer and sent immediately.
// Returns an error only if the underlying connection operation fails.
func (conn *Conn) Send(data []byte) error {
	// Convert Go byte slice to JavaScript ArrayBuffer for transmission
	buffer := _ArrayBuffer.New(len(data))
	array := _Uint8Array.New(buffer)
	js.CopyBytesToJS(array, data)

	conn.ws.Call("send", buffer)
	return nil
}
