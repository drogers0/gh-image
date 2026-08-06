package upload

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTempFile creates a file with the given extension and contents.
func writeTempFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	return path
}

// bearerAgainst points a client at a test server.
func bearerAgainst(server *httptest.Server) *BearerClient {
	c := NewBearerClient("gho_testtoken")
	c.baseURL = server.URL
	return c
}

func TestBearerUpload_Success(t *testing.T) {
	var gotQuery, gotAuth, gotAccept, gotContentType, gotExpect, gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		gotExpect = r.Header.Get("Expect")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://github.com/user-attachments/assets/abc"}`))
	}))
	defer server.Close()

	path := writeTempFile(t, "shot.png", "PNGDATA")
	res, err := bearerAgainst(server).Upload(42, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.URL != "https://github.com/user-attachments/assets/abc" {
		t.Errorf("URL = %q", res.URL)
	}
	if res.Name != "shot.png" {
		t.Errorf("Name = %q, want shot.png", res.Name)
	}
	if want := "![shot.png](https://github.com/user-attachments/assets/abc)"; res.Markdown != want {
		t.Errorf("Markdown = %q, want %q", res.Markdown, want)
	}
	if gotPath != "/user-attachments/assets" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{"name=shot.png", "content_type=image%2Fpng", "repository_id=42"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
	if gotAuth != "Bearer gho_testtoken" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	// Without this header the endpoint answers 400 before looking at anything else.
	if gotContentType != "image/png" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotExpect != "100-continue" {
		t.Errorf("Expect = %q", gotExpect)
	}
	if string(gotBody) != "PNGDATA" {
		t.Errorf("body = %q", gotBody)
	}
}

// TestBearerUpload_PlusInContentTypeIsEncoded guards the query encoding: sent
// raw, the "+" in image/svg+xml arrives as a space and the upload is rejected.
func TestBearerUpload_PlusInContentTypeIsEncoded(t *testing.T) {
	var gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.URL.Query().Get("content_type")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://github.com/user-attachments/assets/svg"}`))
	}))
	defer server.Close()

	path := writeTempFile(t, "diagram.svg", "<svg/>")
	if _, err := bearerAgainst(server).Upload(1, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotContentType != "image/svg+xml" {
		t.Errorf("content_type = %q, want image/svg+xml", gotContentType)
	}
}

func TestBearerUpload_VideoRendersBareURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://github.com/user-attachments/assets/vid"}`))
	}))
	defer server.Close()

	path := writeTempFile(t, "clip.mp4", "MP4")
	res, err := bearerAgainst(server).Upload(1, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Markdown != "https://github.com/user-attachments/assets/vid" {
		t.Errorf("Markdown = %q, want the bare URL", res.Markdown)
	}
}

func TestBearerUpload_StatusErrors(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
	}{
		{"content type refused", http.StatusUnprocessableEntity, `{"errors":[{"field":"content_type"}]}`},
		{"no push access", http.StatusNotFound, `{"message":"Not Found"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			path := writeTempFile(t, "doc.pdf", "PDF")
			_, err := bearerAgainst(server).Upload(1, path)
			var status *StatusError
			if !errors.As(err, &status) {
				t.Fatalf("error = %v, want *StatusError", err)
			}
			if status.Code != tc.code {
				t.Errorf("Code = %d, want %d", status.Code, tc.code)
			}
			if status.Body != tc.body {
				t.Errorf("Body = %q, want %q", status.Body, tc.body)
			}
		})
	}
}

// TestBearerUpload_UnusableSuccess covers a 201 we cannot act on. It must not be
// a *StatusError, since Router treats those as durable answers about the
// content type or the run, and this is neither.
func TestBearerUpload_UnusableSuccess(t *testing.T) {
	for _, body := range []string{`{}`, `{"url":""}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			path := writeTempFile(t, "shot.png", "PNG")
			_, err := bearerAgainst(server).Upload(1, path)
			if err == nil {
				t.Fatal("expected an error")
			}
			var status *StatusError
			if errors.As(err, &status) {
				t.Errorf("error = %v, want a plain error not *StatusError", err)
			}
		})
	}
}

// TestBearerUpload_RejectionSkipsBody is the empirical basis for attempting the
// bearer route unconditionally: a rejected upload must not put the file on the
// wire. Without the 100-continue handshake the server would read all 4 MB before
// its 422 reached the client.
func TestBearerUpload_RejectionSkipsBody(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	// A raw listener rather than httptest: net/http's server would have to read
	// the request body to serve the handler, which is the very thing under test.
	bodyBytes := make(chan int64, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			bodyBytes <- -1
			return
		}
		defer func() { _ = conn.Close() }()

		// Consume the request head, then reject without ever offering 100-continue.
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		body := `{"errors":[{"field":"content_type"}]}`
		fmt.Fprintf(conn, "HTTP/1.1 422 Unprocessable Entity\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)

		// Anything arriving now is body the client should not have sent.
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _ := io.Copy(io.Discard, reader)
		bodyBytes <- n
	}()

	client := NewBearerClient("gho_testtoken")
	client.baseURL = "http://" + listener.Addr().String()

	path := writeTempFile(t, "big.pdf", strings.Repeat("x", 4<<20))
	if _, err := client.Upload(1, path); err == nil {
		t.Fatal("expected a rejection")
	}
	if n := <-bodyBytes; n != 0 {
		t.Errorf("server received %d body bytes, want 0 — the 100-continue short-circuit is not working", n)
	}
}

// TestNewBearerClient_Transport guards the construction decision. The
// short-circuit test above runs over HTTP/1.1 and would still pass if the
// transport were cloned from http.DefaultTransport, but that clone silently
// ignores ExpectContinueTimeout over HTTP/2 because the bundled h2 transport
// reads it through a back-pointer to the transport it was bound to.
func TestNewBearerClient_Transport(t *testing.T) {
	transport, ok := NewBearerClient("t").http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", NewBearerClient("t").http.Transport)
	}
	if transport == http.DefaultTransport {
		t.Fatal("transport is http.DefaultTransport itself")
	}
	if len(transport.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto is populated (%d entries), which means HTTP/2 was bound to another transport", len(transport.TLSNextProto))
	}
	if !transport.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 = false")
	}
	if transport.ExpectContinueTimeout == 0 {
		t.Error("ExpectContinueTimeout = 0, so the body would be sent without waiting for a verdict")
	}
}

func TestBearerUpload_MissingFile(t *testing.T) {
	_, err := NewBearerClient("t").Upload(1, filepath.Join(t.TempDir(), "absent.png"))
	if err == nil || !strings.Contains(err.Error(), "file:") {
		t.Fatalf("error = %v, want a file error", err)
	}
}

func TestStatusError_Message(t *testing.T) {
	if got := (&StatusError{Code: 404}).Error(); got != "HTTP 404" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&StatusError{Code: 422, Body: "nope"}).Error(); got != "HTTP 422: nope" {
		t.Errorf("Error() = %q", got)
	}
}
