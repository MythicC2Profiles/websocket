//go:build websocket

package servers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mythicGRPC "github.com/MythicMeta/MythicContainer/grpc"
	"github.com/MythicMeta/MythicContainer/grpc/services"
	"github.com/MythicMeta/MythicContainer/logging"
	"google.golang.org/grpc"

	"github.com/gorilla/websocket"
	"github.com/kabukky/httpscerts"
)

type WebsocketC2 struct {
	BaseURL     string
	BindAddress string
	SSL         bool
	SocketURI   string
	Defaultpage string
	Logfile     string
	Debug       bool
	Lock        sync.RWMutex
	PushConn    *grpc.ClientConn
	HTTPClient  *http.Client
}

const (
	backendRequestTimeout = 30 * time.Second
	backendResponseLimit  = 16 << 20
	websocketMessageLimit = 16 << 20
	websocketPongWait     = 2 * time.Minute
	websocketPingPeriod   = 45 * time.Second
	websocketWriteWait    = 10 * time.Second
	readHeaderTimeout     = 10 * time.Second
	httpIdleTimeout       = 60 * time.Second
	maxHeaderBytes        = 64 << 10
)

var defaultHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	},
	Timeout: backendRequestTimeout,
}

var upgrader = websocket.Upgrader{
	// Websocket agents are not browser sessions and may not send an Origin
	// header matching the externally routed callback host.
	CheckOrigin: func(*http.Request) bool { return true },
}

type requestMetadata struct {
	UserAgent string
	URL       string
	RemoteIP  string
}

func newServer() Server {
	return &WebsocketC2{}
}

func (s *WebsocketC2) SetBindAddress(addr string) {
	s.BindAddress = addr
}
func (s *WebsocketC2) MythicBaseURL() string {
	return s.BaseURL
}
func (s *WebsocketC2) SetMythicBaseURL(url string) {
	s.BaseURL = url
}

// SetSocketURI - Set socket uri
func (s *WebsocketC2) SetSocketURI(uri string) {
	s.SocketURI = uri
}

func (s *WebsocketC2) PostMessage(msg []byte) []byte {
	return s.postMessage(context.Background(), msg, requestMetadata{})
}

func (s *WebsocketC2) postMessage(ctx context.Context, msg []byte, metadata requestMetadata) []byte {
	url := s.MythicBaseURL()
	if s.Debug {
		log.Printf("Sending POST request to: %s\n", url)
	}
	requestContext, cancel := context.WithTimeout(ctx, backendRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodPost, url, bytes.NewReader(msg))
	if err != nil {
		if s.Debug {
			log.Printf("Error making new http request object: %v\n", err)
		}
		return nil
	}
	if metadata.UserAgent != "" {
		req.Header.Set("X-Forwarded-User-Agent", metadata.UserAgent)
	}
	if metadata.URL != "" {
		req.Header.Set("X-Forwarded-URL", metadata.URL)
	}
	if metadata.RemoteIP != "" {
		req.Header.Set("X-Forwarded-For", metadata.RemoteIP)
	}
	req.Header.Set("Mythic", "websocket")
	req.ContentLength = int64(len(msg))

	httpClient := s.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if s.Debug {
			log.Printf("Error sending POST request: %v\n", err)
		}
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Drain a bounded amount so the transport can reuse ordinary-sized
		// error responses without accepting an unbounded body.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if s.Debug {
			log.Printf("Did not receive 200 response code: %d\n", resp.StatusCode)
		}
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, backendResponseLimit+1))
	if err != nil {
		if s.Debug {
			log.Printf("Error reading response body: %v\n", err)
		}
		return nil
	}
	if len(body) > backendResponseLimit {
		if s.Debug {
			log.Printf("Mythic response exceeded %d bytes\n", backendResponseLimit)
		}
		return nil
	}
	return body
}
func (s *WebsocketC2) SetDebug(debug bool) {
	s.Debug = debug
}

// GetDefaultPage - Get the default html page
func (s *WebsocketC2) GetDefaultPage() string {
	return s.Defaultpage
}

// SetDefaultPage - Set the default html page
func (s *WebsocketC2) SetDefaultPage(newpage string) {
	s.Defaultpage = newpage
}

// SocketHandler - Websockets handler
func (s *WebsocketC2) SocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if s.Debug {
			log.Printf("Websocket upgrade failed: %v\n", err)
		}
		return
	}
	configureWebsocketConnection(conn)
	if s.Debug {
		log.Println("Received new websocket client")
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Accept-Type")), "Push") {
		go s.managePushClient(conn)
	} else {
		go s.managePollClient(conn, metadataFromRequest(r))
	}
}

func configureWebsocketConnection(conn *websocket.Conn) {
	// might need to change this depending on how agents are handling ping/pong keep alives
	conn.SetReadLimit(websocketMessageLimit)
	_ = conn.SetReadDeadline(time.Now().Add(websocketPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(websocketPongWait))
	})
}

func startWebsocketHeartbeat(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(websocketPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(websocketWriteWait)); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func metadataFromRequest(r *http.Request) requestMetadata {
	remoteIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		remoteIP = host
	}
	return requestMetadata{
		UserAgent: r.UserAgent(),
		URL:       r.URL.RequestURI(),
		RemoteIP:  remoteIP,
	}
}

func (s *WebsocketC2) managePollClient(c *websocket.Conn, metadata requestMetadata) {
	heartbeatDone := make(chan struct{})
	go startWebsocketHeartbeat(c, heartbeatDone)
	defer func() {
		close(heartbeatDone)
		log.Println("Lost poll client")
		_ = c.Close()
	}()
	log.Println("Got new poll client")
	for {
		// Wait for the client to send the initial checkin message
		m := Message{}
		var resp []byte
		if err := c.ReadJSON(&m); err != nil {
			if s.Debug {
				log.Println(fmt.Sprintf("Read error %s. Exiting session", err.Error()))
			}
			return
		}
		if s.Debug {
			log.Printf("Received agent message %+v\n", m)
		}
		resp = s.postMessage(context.Background(), []byte(m.Data), metadata)

		reply := Message{}
		if len(resp) == 0 {
			reply.Data = ""
		} else {
			reply.Data = string(resp)
		}
		if err := c.WriteJSON(reply); err != nil {
			if s.Debug {
				log.Println(fmt.Sprintf("Error writing json to client %s", err.Error()))
			}
			return
		}
	}
}
func (s *WebsocketC2) managePushClient(websocketClient *websocket.Conn) {
	s.Lock.Lock()
	if s.PushConn == nil {
		s.PushConn = mythicGRPC.GetNewPushC2ClientConnection()
	}
	s.Lock.Unlock()
	grpcClient := services.NewPushC2Client(s.PushConn)
	streamContext, cancel := context.WithCancel(context.Background())
	grpcStream, err := grpcClient.StartPushC2Streaming(streamContext)
	if err != nil {
		cancel()
		log.Printf("Failed to get new client: %v\n", err)
		_ = websocketClient.Close()
		return
	}
	log.Printf("Got new push client")

	heartbeatDone := make(chan struct{})
	go startWebsocketHeartbeat(websocketClient, heartbeatDone)
	closeConnection := make(chan struct{}, 2)
	// read from websocketClient and send to grpcClient
	go func() {
		defer func() {
			log.Printf("finished websocket -> grpc\n")
			closeConnection <- struct{}{}
		}()
		for {
			fromAgent := Message{}
			readErr := websocketClient.ReadJSON(&fromAgent)
			if readErr != nil {
				if s.Debug {
					log.Println(fmt.Sprintf("Read error %s. Exiting session", readErr.Error()))
				}
				return
			}
			if s.Debug {
				log.Printf("Received agent message %+v\n", fromAgent)
			}
			readErr = grpcStream.Send(&services.PushC2MessageFromAgent{
				C2ProfileName: "websocket",
				RemoteIP:      websocketClient.RemoteAddr().String(),
				Message:       nil,
				Base64Message: []byte(fromAgent.Data),
			})
			if readErr != nil {
				log.Printf("failed to send message to grpc stream: %v\n", readErr)
				return
			}
			//log.Printf("sent agent message to Mythic")
		}
	}()
	// read from grpcClient and send to websocketClient
	go func() {
		defer func() {
			log.Printf("finished grpc -> websocket\n")
			closeConnection <- struct{}{}
		}()
		for {
			fromMythic, readErr := grpcStream.Recv()
			if readErr != nil {
				log.Printf("Failed to read from grpc stream, closing connections: %v\n", readErr)
				return
			}
			reply := Message{}
			reply.Data = string(fromMythic.GetMessage())
			if s.Debug {
				log.Printf("sending agent reply %v\n", fromMythic)
			}
			readErr = websocketClient.WriteJSON(reply)
			if readErr != nil {
				if s.Debug {
					log.Printf("Error writing json to client: %v\n", readErr)
				}
				return
			}
		}
	}()
	<-closeConnection
	// Whichever relay exits first tears down both underlying operations. The
	// websocket close releases ReadJSON while context cancellation releases
	// blocked gRPC Send/Recv calls.
	close(heartbeatDone)
	cancel()
	_ = websocketClient.Close()
	<-closeConnection
	_ = grpcStream.CloseSend()
	log.Printf("closing push client connection\n")
}

// ServeDefaultPage - HTTP handler
func (s *WebsocketC2) ServeDefaultPage(w http.ResponseWriter, r *http.Request) {
	if (r.URL.Path == "/" || r.URL.Path == "/index.html") && r.Method == "GET" {
		// Serve the default page if we receive a GET request at the base URI
		http.ServeFile(w, r, s.GetDefaultPage())
		return
	}
	http.Error(w, "Not Found", http.StatusNotFound)
}
func (s *WebsocketC2) ServeFileWrapper(fileUUID string, downloadToken string) func(http.ResponseWriter, *http.Request) {
	mythicServerHost := os.Getenv("MYTHIC_SERVER_HOST")
	mythicServerPort := os.Getenv("MYTHIC_SERVER_PORT")
	directorForFiles := func(req *http.Request) {
		req.Header.Add("X-forwarded-user-agent", req.Header.Get("User-Agent"))
		req.Header.Add("x-forwarded-url", req.URL.RequestURI())
		req.URL.Scheme = "http"
		req.URL.Host = fmt.Sprintf("%s:%s", mythicServerHost, mythicServerPort)
		req.Host = fmt.Sprintf("%s:%s", mythicServerHost, mythicServerPort)
		req.URL.Path = "/direct/download/" + fileUUID
		req.Header.Add("mythic", "websocket")
		req.Header.Add("authorization", fmt.Sprintf("Bearer %s", downloadToken))
	}
	proxyForFiles := &httputil.ReverseProxy{Director: directorForFiles,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: backendRequestTimeout,
		}}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		proxyForFiles.ServeHTTP(w, r)
	}
}
func (s *WebsocketC2) ServeFile(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request: ", r.URL)
	log.Println("URI Path ", r.URL.Path)
	if (r.URL.Path == "/" || r.URL.Path == "/index.html") && r.Method == "GET" {
		// Serve the default page if we receive a GET request at the base URI
		http.ServeFile(w, r, s.GetDefaultPage())
		return
	}
	http.Error(w, "Not Found", http.StatusNotFound)
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

func certificateHost(bindAddress string) string {
	if host, _, err := net.SplitHostPort(bindAddress); err == nil {
		if host == "" {
			return "localhost"
		}
		return host
	}
	if host := strings.TrimSpace(strings.Trim(bindAddress, "[]")); host != "" {
		return host
	}
	return "localhost"
}

func tlsCertificateFiles(cf C2ConfigEntry) (certFile string, keyFile string, cleanup func(), err error) {
	hasCert := strings.TrimSpace(cf.SSLCert) != ""
	hasKey := strings.TrimSpace(cf.SSLKey) != ""
	if hasCert != hasKey {
		return "", "", func() {}, fmt.Errorf("both sslcert and sslkey must be configured together")
	}
	if hasCert {
		return cf.SSLCert, cf.SSLKey, func() {}, nil
	}

	tlsDirectory, err := os.MkdirTemp("", "mythic-websocket-tls-")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("create temporary TLS directory: %w", err)
	}
	cleanup = func() {
		if removeErr := os.RemoveAll(tlsDirectory); removeErr != nil {
			log.Printf("Failed to remove temporary TLS directory: %v\n", removeErr)
		}
	}
	certFile = filepath.Join(tlsDirectory, "cert.pem")
	keyFile = filepath.Join(tlsDirectory, "key.pem")
	if err := httpscerts.Generate(certFile, keyFile, certificateHost(cf.BindAddress)); err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("generate self-signed TLS certificate: %w", err)
	}
	return certFile, keyFile, cleanup, nil
}

// Run - main function for the websocket profile
func (s *WebsocketC2) Run(cf C2ConfigEntry) {
	s.SetDebug(cf.Debug)
	s.SetDefaultPage(cf.Defaultpage)
	mythicServerHost := os.Getenv("MYTHIC_SERVER_HOST")
	mythicServerPort := os.Getenv("MYTHIC_SERVER_PORT")

	s.SetMythicBaseURL(fmt.Sprintf("http://%s:%s/agent_message", mythicServerHost, mythicServerPort))
	s.SetBindAddress(cf.BindAddress)
	s.SetSocketURI(cf.SocketURI)
	newHTTPMux := http.NewServeMux()
	// Handle requests to the base uri
	for url, fileData := range cf.Payloads {
		localURL := url
		logging.LogInfo("Hosting file", "path", url, "uuid", fileData.AgentFileID)
		newHTTPMux.HandleFunc(localURL, s.ServeFileWrapper(fileData.AgentFileID, fileData.DownloadToken))
	}
	newHTTPMux.HandleFunc("/", s.ServeDefaultPage)
	// Handle requests to the websockets uri
	logging.LogInfo("Serving websocket", "path", s.SocketURI)
	newHTTPMux.HandleFunc(fmt.Sprintf("/%s", s.SocketURI), s.SocketHandler)

	httpServer := newHTTPServer(cf.BindAddress, newHTTPMux)
	if cf.UseSSL {
		certFile, keyFile, cleanup, err := tlsCertificateFiles(cf)
		if err != nil {
			log.Fatal("Failed to configure TLS: ", err)
		}
		defer cleanup()
		if s.Debug {
			log.Printf("Starting SSL server at https://%s and wss://%s\n", cf.BindAddress, cf.BindAddress)
		}
		err = httpServer.ListenAndServeTLS(certFile, keyFile)
		if err != nil {
			log.Fatal("Failed to start websocket TLS server: ", err)
		}
	} else {
		if s.Debug {
			log.Printf("Starting server at http://%s and ws://%s\n", cf.BindAddress, cf.BindAddress)
		}
		err := httpServer.ListenAndServe()
		if err != nil {
			log.Fatal("Failed to start websocket server: ", err)
		}
	}
}
