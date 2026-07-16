//go:build websocket

package servers

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MythicMeta/MythicContainer/grpc/services"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type closeTrackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *closeTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestPostMessageForwardsRequestMetadata(t *testing.T) {
	type capturedRequest struct {
		body      string
		userAgent string
		url       string
		remoteIP  string
		profile   string
	}
	captured := make(chan capturedRequest, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		captured <- capturedRequest{
			body:      string(body),
			userAgent: r.Header.Get("X-Forwarded-User-Agent"),
			url:       r.Header.Get("X-Forwarded-URL"),
			remoteIP:  r.Header.Get("X-Forwarded-For"),
			profile:   r.Header.Get("Mythic"),
		}
		_, _ = w.Write([]byte("mythic-response"))
	}))
	defer backend.Close()

	server := &WebsocketC2{BaseURL: backend.URL, HTTPClient: backend.Client()}
	response := server.postMessage(context.Background(), []byte("agent-message"), requestMetadata{
		UserAgent: "agent-user-agent",
		URL:       "/socket?campaign=test",
		RemoteIP:  "192.0.2.10",
	})
	if string(response) != "mythic-response" {
		t.Fatalf("unexpected response %q", response)
	}

	request := <-captured
	if request.body != "agent-message" {
		t.Errorf("unexpected request body %q", request.body)
	}
	if request.userAgent != "agent-user-agent" {
		t.Errorf("unexpected forwarded user agent %q", request.userAgent)
	}
	if request.url != "/socket?campaign=test" {
		t.Errorf("unexpected forwarded URL %q", request.url)
	}
	if request.remoteIP != "192.0.2.10" {
		t.Errorf("unexpected forwarded IP %q", request.remoteIP)
	}
	if request.profile != "websocket" {
		t.Errorf("unexpected Mythic profile %q", request.profile)
	}
}

func TestPostMessageClosesNonOKResponseBody(t *testing.T) {
	body := &closeTrackingBody{Reader: strings.NewReader("backend failure")}
	var hasDeadline atomic.Bool
	server := &WebsocketC2{
		BaseURL: "http://mythic.invalid/agent_message",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			_, hasDeadlineValue := request.Context().Deadline()
			hasDeadline.Store(hasDeadlineValue)
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       body,
				Request:    request,
			}, nil
		})},
	}

	if response := server.PostMessage([]byte("message")); response != nil {
		t.Fatalf("expected an empty response, got %q", response)
	}
	if !body.closed.Load() {
		t.Fatal("non-OK response body was not closed")
	}
	if !hasDeadline.Load() {
		t.Fatal("backend request did not have a deadline")
	}
}

func TestMetadataFromRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.test/socket?value=1", nil)
	request.Header.Set("User-Agent", "agent-user-agent")
	request.RemoteAddr = "198.51.100.25:54321"

	metadata := metadataFromRequest(request)
	if metadata.UserAgent != "agent-user-agent" {
		t.Errorf("unexpected user agent %q", metadata.UserAgent)
	}
	if metadata.URL != "/socket?value=1" {
		t.Errorf("unexpected URL %q", metadata.URL)
	}
	if metadata.RemoteIP != "198.51.100.25" {
		t.Errorf("unexpected remote IP %q", metadata.RemoteIP)
	}
}

func TestDefaultPageHandlersDoNotAppendNotFound(t *testing.T) {
	page := []byte("<html><body>decoy</body></html>")
	pagePath := filepath.Join(t.TempDir(), "default.html")
	if err := os.WriteFile(pagePath, page, 0600); err != nil {
		t.Fatalf("write default page: %v", err)
	}
	server := &WebsocketC2{Defaultpage: pagePath}

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "ServeDefaultPage", handler: server.ServeDefaultPage},
		{name: "ServeFile", handler: server.ServeFile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
			test.handler(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("unexpected status %d", response.Code)
			}
			if response.Body.String() != string(page) {
				t.Fatalf("unexpected body %q", response.Body.String())
			}
		})
	}
}

func TestDefaultPageReturnsNotFoundForOtherRoutes(t *testing.T) {
	server := &WebsocketC2{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/missing", nil)
	server.ServeDefaultPage(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected status %d", response.Code)
	}
}

func TestHostedFileRejectsRequestBodyMethods(t *testing.T) {
	server := &WebsocketC2{}
	handler := server.ServeFileWrapper("file-id")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://example.test/file", strings.NewReader("body"))
	handler(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected status %d", response.Code)
	}
	if response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("unexpected Allow header %q", response.Header().Get("Allow"))
	}
}

func TestTLSCertificateFilesUsesConfiguredPair(t *testing.T) {
	certFile, keyFile, cleanup, err := tlsCertificateFiles(C2ConfigEntry{
		SSLCert: "/configured/cert.pem",
		SSLKey:  "/configured/key.pem",
	})
	if err != nil {
		t.Fatalf("select configured TLS files: %v", err)
	}
	defer cleanup()
	if certFile != "/configured/cert.pem" || keyFile != "/configured/key.pem" {
		t.Fatalf("configured TLS files were not preserved: cert=%q key=%q", certFile, keyFile)
	}
}

func TestTLSCertificateFilesRequiresCompletePair(t *testing.T) {
	tests := []C2ConfigEntry{
		{SSLCert: "/configured/cert.pem"},
		{SSLKey: "/configured/key.pem"},
	}
	for _, config := range tests {
		_, _, cleanup, err := tlsCertificateFiles(config)
		cleanup()
		if err == nil {
			t.Fatalf("expected incomplete TLS pair to fail: %+v", config)
		}
	}
}

func TestCertificateHost(t *testing.T) {
	tests := map[string]string{
		"0.0.0.0:8081":   "0.0.0.0",
		"localhost:8081": "localhost",
		"[::1]:8081":     "::1",
		":8081":          "localhost",
		"example.test":   "example.test",
		"":               "localhost",
	}
	for bindAddress, expected := range tests {
		if actual := certificateHost(bindAddress); actual != expected {
			t.Errorf("certificateHost(%q) = %q, want %q", bindAddress, actual, expected)
		}
	}
}

func TestTLSCertificateFilesGeneratesIsolatedPrivateKeys(t *testing.T) {
	config := C2ConfigEntry{BindAddress: "127.0.0.1:8081"}
	certOne, keyOne, cleanupOne, err := tlsCertificateFiles(config)
	if err != nil {
		t.Fatalf("generate first TLS pair: %v", err)
	}
	certTwo, keyTwo, cleanupTwo, err := tlsCertificateFiles(config)
	if err != nil {
		cleanupOne()
		t.Fatalf("generate second TLS pair: %v", err)
	}

	if filepath.Dir(certOne) == filepath.Dir(certTwo) {
		t.Fatal("generated TLS pairs share a directory")
	}
	for _, pair := range []struct {
		cert string
		key  string
	}{{cert: certOne, key: keyOne}, {cert: certTwo, key: keyTwo}} {
		if _, err := tls.LoadX509KeyPair(pair.cert, pair.key); err != nil {
			t.Errorf("load generated TLS pair: %v", err)
		}
		info, err := os.Stat(pair.key)
		if err != nil {
			t.Errorf("stat generated private key: %v", err)
			continue
		}
		if permissions := info.Mode().Perm(); permissions&0077 != 0 {
			t.Errorf("private key permissions are too broad: %o", permissions)
		}
	}

	directoryOne := filepath.Dir(certOne)
	directoryTwo := filepath.Dir(certTwo)
	cleanupOne()
	cleanupTwo()
	for _, directory := range []string{directoryOne, directoryTwo} {
		if _, err := os.Stat(directory); !os.IsNotExist(err) {
			t.Errorf("temporary TLS directory was not removed: %s", directory)
		}
	}
}

func TestHTTPServerHasResourceTimeouts(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NewServeMux())
	if server.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout is not configured")
	}
	if server.IdleTimeout <= 0 {
		t.Fatal("IdleTimeout is not configured")
	}
	if server.MaxHeaderBytes <= 0 {
		t.Fatal("MaxHeaderBytes is not configured")
	}
	if defaultHTTPClient.Timeout <= 0 {
		t.Fatal("backend HTTP client timeout is not configured")
	}
}

func TestSocketHandlerSupportsConcurrentUpgrades(t *testing.T) {
	server := &WebsocketC2{}
	httpServer := httptest.NewServer(http.HandlerFunc(server.SocketHandler))
	defer httpServer.Close()
	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	const clients = 20
	errors := make(chan error, clients)
	var waitGroup sync.WaitGroup
	for i := 0; i < clients; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			dialer := *websocket.DefaultDialer
			connection, _, err := dialer.Dial(websocketURL, nil)
			if err != nil {
				errors <- err
				return
			}
			_ = connection.Close()
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("websocket upgrade failed: %v", err)
	}
}

type cancellationPushServer struct {
	services.UnimplementedPushC2Server
	started  chan struct{}
	canceled chan struct{}
}

func (s *cancellationPushServer) StartPushC2Streaming(stream services.PushC2_StartPushC2StreamingServer) error {
	close(s.started)
	<-stream.Context().Done()
	close(s.canceled)
	return nil
}

func TestPushClientDisconnectCancelsGRPCStream(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	pushServer := &cancellationPushServer{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	services.RegisterPushC2Server(grpcServer, pushServer)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	contextWithTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	grpcConnection, err := grpc.DialContext(
		contextWithTimeout,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial buffered gRPC server: %v", err)
	}
	t.Cleanup(func() {
		_ = grpcConnection.Close()
	})

	server := &WebsocketC2{PushConn: grpcConnection}
	httpServer := httptest.NewServer(http.HandlerFunc(server.SocketHandler))
	defer httpServer.Close()
	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	header := make(http.Header)
	header.Set("Accept-Type", "Push")
	connection, _, err := websocket.DefaultDialer.Dial(websocketURL, header)
	if err != nil {
		t.Fatalf("dial websocket server: %v", err)
	}

	if err := connection.WriteJSON(Message{Data: "agent-message"}); err != nil {
		_ = connection.Close()
		t.Fatalf("write websocket message: %v", err)
	}
	select {
	case <-pushServer.started:
	case <-time.After(2 * time.Second):
		_ = connection.Close()
		t.Fatal("gRPC stream did not start")
	}

	if err := connection.Close(); err != nil {
		t.Fatalf("close websocket connection: %v", err)
	}
	select {
	case <-pushServer.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket disconnect did not cancel the gRPC stream")
	}
}
