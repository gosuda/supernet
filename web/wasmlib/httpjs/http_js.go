package httpjs

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"strings"
	"syscall/js"

	"pkg.gfire.dev/supernet/web/wasmlib/streamjs"
)

var (
	// ErrRequestFailed is returned when the HTTP fetch operation fails due to network or other issues
	ErrRequestFailed = errors.New("request failed")
	// ErrAborted is returned when the HTTP request is aborted before completion
	ErrAborted = errors.New("request aborted")
)

var (
	// _fetch is a cached reference to the JavaScript fetch function for HTTP requests
	_fetch = js.Global().Get("fetch")
	// _Headers is a cached reference to the JavaScript Headers constructor for managing HTTP headers
	_Headers = js.Global().Get("Headers")
	// _Response is a cached reference to the JavaScript Response constructor for creating response objects
	_Response = js.Global().Get("Response")
	// _ArrayBuffer is a cached reference to the JavaScript ArrayBuffer constructor for binary data handling
	_ArrayBuffer = js.Global().Get("ArrayBuffer")
	// _Uint8Array is a cached reference to the JavaScript Uint8Array constructor for typed array operations
	_Uint8Array = js.Global().Get("Uint8Array")
	// _Promise is a cached reference to the JavaScript Promise constructor for async operations
	_Promise = js.Global().Get("Promise")
	// _Object is a cached reference to the JavaScript Object constructor for creating plain objects
	_Object = js.Global().Get("Object")
	// _Array is a cached reference to the JavaScript Array constructor for array operations
	_Array = js.Global().Get("Array")
	// _Error is a cached reference to the JavaScript Error constructor for creating error objects
	_Error = js.Global().Get("Error")
)

// Request represents an HTTP request that will be executed via the JavaScript fetch API.
// Supports custom headers and binary request bodies. Use SetHeader and SetBody to configure.
type Request struct {
	Method  string            // HTTP method (GET, POST, PUT, DELETE, etc.)
	URL     string            // Target URL for the request
	Headers map[string]string // Custom HTTP headers to include in the request
	Body    []byte            // Request body as binary data (optional)
}

// Response represents an HTTP response received from the fetch API.
// The body is provided as a JavaScript ReadableStream for efficient streaming of large responses.
type Response struct {
	StatusCode int                        // HTTP status code (200, 404, 500, etc.)
	Headers    map[string]string          // Response headers as key-value pairs
	Body       *streamjs.ReadableStream   // Streaming response body wrapped as a ReadableStream

	jsResponse js.Value       // The underlying JavaScript Response object
	bodyReader io.ReadCloser  // The underlying reader for bulk reading via ReadAll
}

// NewRequest creates a new HTTP request with the specified method and URL.
// The request is initialized with empty headers and body; use SetHeader and SetBody to configure.
func NewRequest(method, url string) *Request {
	return &Request{
		Method:  method,
		URL:     url,
		Headers: make(map[string]string),
	}
}

// SetHeader sets or overwrites an HTTP request header with the given key and value.
// Header names are case-sensitive and should follow HTTP header conventions.
func (r *Request) SetHeader(key, value string) {
	r.Headers[key] = value
}

// SetBody sets the request body from a byte slice.
// The body will be transmitted as binary data (ArrayBuffer) to the server.
// For requests without a body (GET, DELETE), this can be left unset.
func (r *Request) SetBody(body []byte) {
	r.Body = body
}

// Do executes the HTTP request asynchronously and returns a Response.
// Blocks until the response is received or an error occurs.
// The response body is provided as a ReadableStream for memory-efficient handling of large responses.
func (r *Request) Do() (*Response, error) {
	// Create fetch options object to pass to the JavaScript fetch API
	opts := _Object.New()
	opts.Set("method", r.Method)

	// Configure request headers if any were specified
	if len(r.Headers) > 0 {
		jsHeaders := _Headers.New()
		for key, value := range r.Headers {
			jsHeaders.Call("append", key, value)
		}
		opts.Set("headers", jsHeaders)
	}

	// Convert request body to JavaScript ArrayBuffer if present
	if len(r.Body) > 0 {
		buffer := _ArrayBuffer.New(len(r.Body))
		array := _Uint8Array.New(buffer)
		js.CopyBytesToJS(array, r.Body)
		opts.Set("body", buffer)
	}

	// Create channels to synchronously wait for the asynchronous fetch result
	resultCh := make(chan *Response, 1)
	errCh := make(chan error, 1)

	// Invoke the JavaScript fetch API with configured options
	fetchPromise := _fetch.Invoke(r.URL, opts)

	// Define promise handlers for success and failure cases
	var thenFunc, catchFunc js.Func

	thenFunc = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		defer thenFunc.Release()

		jsResp := args[0]

		// Parse the JavaScript Response object into a Go Response struct
		resp := &Response{
			StatusCode: jsResp.Get("status").Int(),
			Headers:    make(map[string]string),
			jsResponse: jsResp,
		}

		// Extract all response headers from the JavaScript Headers object
		jsHeaders := jsResp.Get("headers")
		entriesIter := jsHeaders.Call("entries")

		for {
			next := entriesIter.Call("next")
			if next.Get("done").Bool() {
				break
			}
			entry := next.Get("value")
			key := entry.Index(0).String()
			value := entry.Index(1).String()
			resp.Headers[key] = value
		}

		// Wrap the JavaScript ReadableStream body for Go consumption
		jsBody := jsResp.Get("body")
		if !jsBody.IsNull() && !jsBody.IsUndefined() {
			// Create a Go reader adapter that wraps the JavaScript ReadableStream
			reader := &jsStreamReader{
				jsReader: jsBody.Call("getReader"),
			}
			resp.bodyReader = reader
			resp.Body = streamjs.NewReadableStream(reader)
		}

		resultCh <- resp
		return nil
	})

	catchFunc = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		defer catchFunc.Release()

		// Extract error message from the JavaScript error if available
		if len(args) > 0 {
			errMsg := args[0].Get("message").String()
			errCh <- errors.New(errMsg)
		} else {
			errCh <- ErrRequestFailed
		}
		return nil
	})

	// Attach promise handlers to the fetch promise
	fetchPromise.Call("then", thenFunc).Call("catch", catchFunc)

	// Block until response is received or error occurs
	select {
	case resp := <-resultCh:
		return resp, nil
	case err := <-errCh:
		return nil, err
	}
}

// jsStreamReader implements io.ReadCloser by reading from a JavaScript ReadableStream.
// It adapts JavaScript's push-based stream model to Go's pull-based io.Reader model.
type jsStreamReader struct {
	// jsReader holds the JavaScript ReadableStreamDefaultReader object obtained from getReader()
	jsReader js.Value
	// closed tracks whether the reader has been closed to prevent further reads
	closed bool
}

// Read reads data from the JavaScript ReadableStream into the provided buffer.
// Blocks until data is available or the stream ends. Returns io.EOF when the stream is fully consumed.
func (r *jsStreamReader) Read(p []byte) (n int, err error) {
	if r.closed {
		return 0, io.EOF
	}

	// Create channel to receive the async read result from the promise handler
	resultCh := make(chan readResult, 1)

	// Invoke read() on the JavaScript ReadableStreamDefaultReader
	readPromise := r.jsReader.Call("read")

	var thenFunc js.Func
	thenFunc = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		defer thenFunc.Release()

		result := args[0]
		done := result.Get("done").Bool()

		// Stream is exhausted when done flag is true
		if done {
			resultCh <- readResult{n: 0, err: io.EOF}
			return nil
		}

		// Extract the chunk (Uint8Array) from the read result
		chunk := result.Get("value")
		if chunk.IsNull() || chunk.IsUndefined() {
			resultCh <- readResult{n: 0, err: nil}
			return nil
		}

		// Get the number of bytes available in the chunk
		length := chunk.Get("byteLength").Int()
		if length == 0 {
			resultCh <- readResult{n: 0, err: nil}
			return nil
		}

		// Determine how many bytes we can actually copy (min of chunk size and buffer size)
		copyLen := length
		if copyLen > len(p) {
			copyLen = len(p)
		}

		// Create a temporary Uint8Array view if we need to copy only partial data
		// This avoids copying more data than requested
		if copyLen < length {
			chunk = _Uint8Array.New(chunk.Get("buffer"), chunk.Get("byteOffset"), copyLen)
		}

		// Copy bytes from JavaScript Uint8Array to Go buffer
		js.CopyBytesToGo(p[:copyLen], chunk)
		resultCh <- readResult{n: copyLen, err: nil}
		return nil
	})

	// Attach the then handler to the promise returned by read()
	readPromise.Call("then", thenFunc)

	// Wait for the promise to resolve and return the result
	res := <-resultCh
	return res.n, res.err
}

// Close closes the JavaScript reader and cancels further reads from the stream.
// Safe to call multiple times. Subsequent Read calls will return io.EOF.
func (r *jsStreamReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true

	// Call cancel() on the JavaScript ReadableStreamDefaultReader to release the lock
	if !r.jsReader.IsNull() && !r.jsReader.IsUndefined() {
		r.jsReader.Call("cancel")
	}
	return nil
}

// readResult is a helper struct to pass both the read count and error through a channel
type readResult struct {
	n   int   // Number of bytes successfully read
	err error // Error that occurred, or nil on success
}

// ReadAll reads the entire response body into a byte slice.
// This is a convenience method for small responses; for large bodies, prefer streaming with the Body field.
// Returns an empty slice if no body was present in the response.
func (resp *Response) ReadAll() ([]byte, error) {
	if resp.bodyReader == nil {
		return []byte{}, nil
	}

	var buf bytes.Buffer
	buffer := make([]byte, 4096)

	// Read from the underlying reader in 4KB chunks until EOF
	for {
		n, err := resp.bodyReader.Read(buffer)
		if n > 0 {
			buf.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

// Close closes the response body stream and releases associated resources.
// Should be called when finished consuming the response to free up resources.
// Safe to call multiple times.
func (resp *Response) Close() error {
	if resp.Body != nil {
		resp.Body.Close()
	}
	return nil
}

// Get performs a GET request to the specified URL and returns the response.
// This is a convenience function for simple GET requests without custom headers.
func Get(url string) (*Response, error) {
	req := NewRequest("GET", url)
	return req.Do()
}

// Post performs a POST request to the specified URL with the given body.
// The contentType parameter specifies the Content-Type header; if empty, no Content-Type header is sent.
func Post(url string, contentType string, body []byte) (*Response, error) {
	req := NewRequest("POST", url)
	if contentType != "" {
		req.SetHeader("Content-Type", contentType)
	}
	req.SetBody(body)
	return req.Do()
}

// Put performs a PUT request to the specified URL with the given body.
// The contentType parameter specifies the Content-Type header; if empty, no Content-Type header is sent.
func Put(url string, contentType string, body []byte) (*Response, error) {
	req := NewRequest("PUT", url)
	if contentType != "" {
		req.SetHeader("Content-Type", contentType)
	}
	req.SetBody(body)
	return req.Do()
}

// Delete performs a DELETE request to the specified URL.
// This is a convenience function for simple DELETE requests without custom headers or body.
func Delete(url string) (*Response, error) {
	req := NewRequest("DELETE", url)
	return req.Do()
}

// JSRequestToHTTPRequest converts a JavaScript Request object into a Go net/http.Request.
// This is useful when implementing server-side HTTP handlers that accept JavaScript-generated requests.
// Returns an error if the request format is invalid or if body reading fails.
func JSRequestToHTTPRequest(jsReq js.Value) (*http.Request, error) {
	// Extract HTTP method and URL from the JavaScript Request
	method := jsReq.Get("method").String()
	url := jsReq.Get("url").String()

	// Read the request body as binary data (ArrayBuffer in JavaScript)
	var bodyReader io.Reader
	jsBody := jsReq.Get("body")

	if !jsBody.IsNull() && !jsBody.IsUndefined() {
		// Call arrayBuffer() to get the request body as a Promise<ArrayBuffer>
		bodyPromise := jsReq.Call("arrayBuffer")

		bodyChan := make(chan []byte, 1)
		errChan := make(chan error, 1)

		var successFunc, failFunc js.Func
		successFunc = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			defer successFunc.Release()

			// Convert the ArrayBuffer to a Go byte slice
			jsBodyArray := _Uint8Array.New(args[0])
			bodyBuffer := make([]byte, jsBodyArray.Get("byteLength").Int())
			js.CopyBytesToGo(bodyBuffer, jsBodyArray)
			bodyChan <- bodyBuffer
			return nil
		})

		failFunc = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			defer failFunc.Release()

			if len(args) > 0 {
				errChan <- errors.New(args[0].String())
			} else {
				errChan <- errors.New("failed to read request body")
			}
			return nil
		})

		bodyPromise.Call("then", successFunc).Call("catch", failFunc)

		select {
		case body := <-bodyChan:
			bodyReader = bytes.NewReader(body)
		case err := <-errChan:
			return nil, err
		}
	} else {
		// No body present - use empty reader
		bodyReader = bytes.NewReader([]byte{})
	}

	// Create the Go http.Request with the extracted method, URL, and body
	httpReq, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	// Extract and convert all request headers from the JavaScript Request
	jsHeaders := _Array.Call("from", jsReq.Get("headers").Call("entries"))
	headersLen := jsHeaders.Length()

	var headerBuilder strings.Builder
	for i := 0; i < headersLen; i++ {
		entry := jsHeaders.Index(i)
		if entry.Length() < 2 {
			continue
		}

		key := entry.Index(0).String()
		value := entry.Index(1).String()

		// Format header as "Key: Value\r\n" for MIME header parsing
		headerBuilder.WriteString(key)
		headerBuilder.WriteString(": ")
		headerBuilder.WriteString(value)
		headerBuilder.WriteString("\r\n")
	}
	headerBuilder.WriteString("\r\n")

	// Parse the formatted headers using Go's textproto package
	tpr := textproto.NewReader(bufio.NewReader(strings.NewReader(headerBuilder.String())))
	mimeHeader, err := tpr.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	httpReq.Header = http.Header(mimeHeader)

	return httpReq, nil
}

// HTTPResponseToJSResponse converts a Go net/http.Response into a JavaScript Response object.
// The response body is wrapped in a ReadableStream for efficient streaming to JavaScript consumers.
// Returns a JavaScript Response that can be returned from a WebWorker or server handler.
func HTTPResponseToJSResponse(httpResp *http.Response) js.Value {
	// Create a JavaScript headers object from the Go http.Header
	jsHeaders := _Object.New()
	for key, values := range httpResp.Header {
		if len(values) > 0 {
			jsHeaders.Set(key, values[0])
		}
	}

	// Wrap the response body in a ReadableStream for memory-efficient streaming
	var jsBody js.Value
	if httpResp.Body != nil {
		stream := streamjs.NewReadableStream(httpResp.Body)
		jsBody = stream.Value
	} else {
		jsBody = js.Null()
	}

	// Create the response initialization options for the JavaScript Response constructor
	jsOptions := _Object.New()
	jsOptions.Set("status", httpResp.StatusCode)
	jsOptions.Set("statusText", httpResp.Status)
	jsOptions.Set("headers", jsHeaders)

	// Create and return the JavaScript Response object
	jsResp := _Response.New(jsBody, jsOptions)
	return jsResp
}

// ServeHTTPAsyncWithStreaming handles an HTTP request asynchronously using the provided handler
// and returns a Promise that resolves to a JavaScript Response with streaming body support.
// This function safely executes the handler in a goroutine and streams the response back to JavaScript
// without blocking the JS thread. Panics in the handler are caught and converted to error responses.
func ServeHTTPAsyncWithStreaming(handler http.Handler, jsReq js.Value) js.Value {
	return _Promise.New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		resolve := args[0]
		reject := args[1]

		go func() {
			// Convert the JavaScript Request to a Go net/http.Request
			httpReq, err := JSRequestToHTTPRequest(jsReq)
			if err != nil {
				reject.Invoke(_Error.New(err.Error()))
				return
			}

			// Create an io.Pipe to stream the response body from the handler to JavaScript
			pr, pw := io.Pipe()

			// Create custom ResponseWriter that captures headers and pipes the body
			respWriter := &streamingResponseWriter{
				pipeWriter:      pw,
				header:          make(http.Header),
				statusCode:      200,
				wroteHeaderChan: make(chan struct{}, 1),
			}

			// Execute the handler in a separate goroutine to avoid blocking
			go func() {
				defer pw.Close()
				defer func() {
					if r := recover(); r != nil {
						// Recover from panic in handler and return an error response
						respWriter.statusCode = http.StatusInternalServerError
						pw.CloseWithError(errors.New("internal server error"))
					}
				}()

				handler.ServeHTTP(respWriter, httpReq)

				// Ensure headers were written (required for valid HTTP response)
				if !respWriter.wroteHeader {
					respWriter.WriteHeader(http.StatusBadGateway)
					http.Error(respWriter, "Bad Gateway\n\nUpstream server error", http.StatusBadGateway)
				}
			}()

			// Wait for the handler to write headers before returning response to JavaScript
			<-respWriter.wroteHeaderChan

			// Construct an http.Response with the handler's status and headers, and streaming body
			httpResp := &http.Response{
				StatusCode: respWriter.statusCode,
				Status:     http.StatusText(respWriter.statusCode),
				Header:     respWriter.header,
				Body:       pr,
			}

			// Convert the Go response to a JavaScript Response object and resolve the promise
			jsResp := HTTPResponseToJSResponse(httpResp)
			resolve.Invoke(jsResp)
		}()

		return nil
	}))
}

// streamingResponseWriter implements http.ResponseWriter interface for streaming HTTP responses.
// It pipes the response body to an io.PipeReader for consumption by ReadableStream,
// while capturing headers and status code to send back to the JavaScript caller.
type streamingResponseWriter struct {
	// pipeWriter is where the handler writes the response body; data flows to JavaScript through the pipe
	pipeWriter *io.PipeWriter
	// header stores the HTTP response headers set by the handler
	header http.Header
	// statusCode stores the HTTP status code (default 200)
	statusCode int
	// wroteHeader tracks whether WriteHeader has been called
	wroteHeader bool
	// wroteHeaderChan signals when headers have been written, allowing the main goroutine to proceed
	wroteHeaderChan chan struct{}
}

// Header returns the response header map that handlers can use to set response headers.
func (w *streamingResponseWriter) Header() http.Header {
	return w.header
}

// Write writes data to the response body. If headers haven't been written yet, it writes them with status 200.
// Returns the number of bytes written and any error encountered.
func (w *streamingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.pipeWriter.Write(b)
}

// WriteHeader sends the HTTP status code and must be called before writing the response body.
// It can only be called once; subsequent calls are ignored.
// Signals completion on wroteHeaderChan to notify the main goroutine.
func (w *streamingResponseWriter) WriteHeader(statusCode int) {
	if !w.wroteHeader {
		w.statusCode = statusCode
		w.wroteHeader = true
		close(w.wroteHeaderChan)
	}
}
