package streamjs

import (
	"io"
	"sync"
	"syscall/js"
)

var (
	_ReadableStream = js.Global().Get("ReadableStream")
	_Object         = js.Global().Get("Object")
	_Promise        = js.Global().Get("Promise")
	_Error          = js.Global().Get("Error")
	_Uint8Array     = js.Global().Get("Uint8Array")
)

type ReadableStream struct {
	js.Value
	r         io.ReadCloser
	closeOnce sync.Once

	// buffer is used to temporarily store data read from the underlying Go reader
	buffer []byte

	funcsToBeReleased []js.Func
}

// NewReadableStream wraps a Go io.ReadCloser into a JavaScript ReadableStream object.
// This allows streaming data from Go to JavaScript in an asynchronous, non-blocking manner.
func NewReadableStream(r io.ReadCloser) *ReadableStream {
	// 1. First, create the Go wrapper struct that holds the reader and manages lifecycle.
	rs := &ReadableStream{
		r:      r,
		buffer: make([]byte, 4096), // Initialize with 4KB buffer to minimize allocations
	}

	// 2. Define JS callback functions that will be invoked by the JavaScript ReadableStream.
	// These functions capture the 'rs' pointer in their closure to access the reader.
	var onStart, onPull, onCancel js.Func

	// onStart: Called when the stream is first created (typically left empty as no setup is needed)
	onStart = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		// controller := args[0]
		return nil
	})

	// onPull: Called when JavaScript requests more data from the stream (most critical callback).
	// This is where actual I/O reading happens.
	onPull = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		controller := args[0]

		// 3. Create and return a Promise to handle the asynchronous data reading.
		// We return a Promise to prevent blocking the JS thread during potentially blocking I/O.
		// The actual reading happens in a separate goroutine.
		var promiseFn js.Func
		promiseFn = js.FuncOf(func(this js.Value, pArgs []js.Value) interface{} {
			resolve := pArgs[0]
			reject := pArgs[1]

			// 4. Launch a goroutine to perform the potentially blocking Read operation.
			// This ensures the JS thread is never blocked waiting for I/O.
			go func() {
				defer promiseFn.Release()

				n, err := rs.r.Read(rs.buffer)

				// 5. Handle errors that may occur during reading
				if err != nil {
					if err == io.EOF {
						// 5a. End of file (EOF) reached - close the stream normally
						controller.Call("close")
					} else {
						// 5b. Actual read error occurred - signal error to the stream and reject the promise
						jsErr := _Error.New(err.Error())
						controller.Call("error", jsErr)
						reject.Invoke(jsErr) // Reject the promise with the error
					}
					resolve.Invoke() // Resolve promise to indicate pull operation is complete
					return
				}

				// 6. Successfully read data - process and enqueue it for JavaScript to consume
				if n > 0 {
					// 6a. Create a JavaScript Uint8Array with the exact number of bytes read
					jsChunk := _Uint8Array.New(n)

					// 6b. Copy bytes from Go buffer (rs.buffer[:n]) to JS Uint8Array
					js.CopyBytesToJS(jsChunk, rs.buffer[:n])

					// 6c. Add the chunk to the stream controller's queue for JavaScript to consume
					controller.Call("enqueue", jsChunk)
				}

				// 7. Signal successful completion of the pull operation by resolving the promise
				resolve.Invoke()
			}()

			return nil
		})

		return _Promise.New(promiseFn)
	})

	// onCancel: Called when JavaScript side cancels the stream (e.g., due to consumption stoppage)
	onCancel = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		// Close the Go reader and clean up resources when stream is cancelled
		rs.closeOnce.Do(func() {
			rs.r.Close()
		})
		return nil
	})

	// 8. Create the JavaScript 'underlyingSource' object that implements the ReadableStream protocol
	underlyingSource := _Object.New()
	underlyingSource.Set("start", onStart)
	underlyingSource.Set("pull", onPull)
	underlyingSource.Set("cancel", onCancel)

	// 9. Create the actual JavaScript ReadableStream instance with the underlying source
	stream := _ReadableStream.New(underlyingSource)

	// 10. Complete the Go wrapper struct by assigning the JS stream and tracking functions for cleanup
	rs.Value = stream
	rs.funcsToBeReleased = []js.Func{onStart, onPull, onCancel}

	return rs
}

// Close closes the stream and releases all allocated JavaScript function callbacks.
// It ensures proper cleanup of both Go and JavaScript resources to prevent memory leaks.
func (rs *ReadableStream) Close() {
	// Release all JavaScript function callbacks to allow garbage collection
	for _, f := range rs.funcsToBeReleased {
		f.Release()
	}

	// Also close the underlying Go reader to free associated resources.
	// Using closeOnce.Do ensures the reader is closed exactly once, even if Close is called multiple times.
	rs.closeOnce.Do(func() {
		rs.r.Close()
	})
}
