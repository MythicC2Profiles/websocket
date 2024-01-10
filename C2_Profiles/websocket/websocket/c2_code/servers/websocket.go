//go:build websocket

package servers

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	mythicGRPC "github.com/MythicMeta/MythicContainer/grpc"
	"github.com/MythicMeta/MythicContainer/grpc/services"
	"github.com/MythicMeta/MythicContainer/logging"
	"google.golang.org/grpc"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"sync"
	"time"

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
}

var tr = &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
}
var client = &http.Client{Transport: tr}
var upgrader = websocket.Upgrader{}

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
	url := s.MythicBaseURL()
	//log.Println("Sending POST request to url: ", url)
	if s.Debug {
		log.Println(fmt.Sprintln("Sending POST request to: ", url))
	}
	if req, err := http.NewRequest("POST", url, bytes.NewBuffer(msg)); err != nil {
		if s.Debug {
			log.Println(fmt.Sprintf("Error making new http request object: %s", err.Error()))
		}
		return make([]byte, 0)
	} else {
		req.Header.Add("Mythic", "websocket")
		contentLength := len(msg)
		req.ContentLength = int64(contentLength)

		if resp, err := client.Do(req); err != nil {
			if s.Debug {
				log.Println(fmt.Sprintf("Error sending POST request: %s", err.Error()))
			}
			return make([]byte, 0)
		} else if resp.StatusCode != 200 {
			if s.Debug {
				log.Println(fmt.Sprintf("Did not receive 200 response code: %d", resp.StatusCode))
			}
			return make([]byte, 0)
		} else {
			defer resp.Body.Close()
			if body, err := io.ReadAll(resp.Body); err != nil {
				if s.Debug {
					log.Println(fmt.Sprintf("Error reading response body: %s", err.Error()))
				}
				return make([]byte, 0)
			} else {
				return body
			}
		}
	}
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
	//Upgrade the websocket connection
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if s.Debug {
			log.Println(fmt.Sprintf("Websocket upgrade failed: %s\n", err.Error()))
		}
		http.Error(w, "websocket connection failed", http.StatusBadRequest)
		return
	}
	if s.Debug {
		log.Println(fmt.Sprintf("Received new websocket client"))
	}
	taskingType, ok := r.Header["Accept-Type"]
	if !ok || (len(taskingType) > 0 && taskingType[0] == "Poll") {
		go s.managePollClient(conn)
	} else {
		go s.managePushClient(conn)
	}

}
func (s *WebsocketC2) managePollClient(c *websocket.Conn) {
	defer func() {
		log.Println("Lost poll client")
		c.Close()
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
			log.Println(fmt.Sprintf("Received agent message %+v\n", m))
		}
		resp = s.PostMessage([]byte(m.Data))

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
func (s *WebsocketC2) getGRPConnection() *grpc.ClientConn {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	if s.PushConn == nil {
		s.PushConn = mythicGRPC.GetNewPushC2ClientConnection()
	}
	return s.PushConn
}
func (s *WebsocketC2) getNewPushClient() services.PushC2Client {
	return services.NewPushC2Client(s.getGRPConnection())
}
func (s *WebsocketC2) managePushClient(websocketClient *websocket.Conn) {
	defer websocketClient.Close()
	grpcClient := s.getNewPushClient()
	streamContext, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		err := websocketClient.Close()
		if err != nil {
			log.Printf("Failed to close websocket connection: %v\n", err)
		}
	}()
	grpcStream, err := grpcClient.StartPushC2Streaming(streamContext)
	if err != nil {
		log.Printf("Failed to get new client: %v\n", err)
		return
	} else {
		log.Printf("Got new push client")
	}
	closeConnection := make(chan bool, 2)
	// read from websocketClient and send to grpcClient
	go func() {
		defer func() {
			log.Printf("finished websocket -> grpc\n")
			cancel()
			closeConnection <- true
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
				log.Println(fmt.Sprintf("Received agent message %+v\n", fromAgent))
			}
			readErr = grpcStream.Send(&services.PushC2MessageFromAgent{
				C2ProfileName: "websocket",
				RemoteIP:      websocketClient.RemoteAddr().String(),
				TaskingSize:   0,
				Message:       nil,
				Base64Message: []byte(fromAgent.Data),
			})
			if readErr != nil {
				log.Printf("failed to send message to grpc stream: %v\n", readErr)
				grpcStream.CloseSend()
				return
			}
			//log.Printf("sent agent message to Mythic")
		}
	}()
	// read from grpcClient and send to websocketClient
	go func() {
		defer func() {
			log.Printf("finished grpc -> websocket\n")
			closeConnection <- true
		}()
		for {
			fromMythic, readErr := grpcStream.Recv()
			if readErr != nil {
				log.Printf("Failed to read from grpc stream, closing connections: %v\n", readErr)
				grpcStream.CloseSend()
				return
			}
			reply := Message{}
			reply.Data = string(fromMythic.GetMessage())
			if s.Debug {
				log.Println(fmt.Sprintf("sending agent reply %v\n", fromMythic))
			}
			readErr = websocketClient.WriteJSON(reply)
			if readErr != nil {
				if s.Debug {
					log.Println(fmt.Sprintf("Error writing json to client %s", err.Error()))
				}
				return
			}
		}
	}()
	<-closeConnection
	<-closeConnection
	log.Printf("closing push client connection\n")
}

// ServeDefaultPage - HTTP handler
func (s *WebsocketC2) ServeDefaultPage(w http.ResponseWriter, r *http.Request) {
	if (r.URL.Path == "/" || r.URL.Path == "/index.html") && r.Method == "GET" {
		// Serve the default page if we receive a GET request at the base URI
		http.ServeFile(w, r, s.GetDefaultPage())
	}
	http.Error(w, "Not Found", http.StatusNotFound)
	return
}
func (s *WebsocketC2) ServeFileWrapper(fileUUID string) func(http.ResponseWriter, *http.Request) {
	mythicServerHost := os.Getenv("MYTHIC_SERVER_HOST")
	mythicServerPort := os.Getenv("MYTHIC_SERVER_PORT")
	directorForFiles := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = fmt.Sprintf("%s:%s", mythicServerHost, mythicServerPort)
		req.Host = fmt.Sprintf("%s:%s", mythicServerHost, mythicServerPort)
		req.URL.Path = "/direct/download/" + fileUUID
	}
	proxyForFiles := &httputil.ReverseProxy{Director: directorForFiles,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:    10,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}
	return func(w http.ResponseWriter, r *http.Request) {
		proxyForFiles.ServeHTTP(w, r)
	}
}
func (s *WebsocketC2) ServeFile(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request: ", r.URL)
	log.Println("URI Path ", r.URL.Path)
	if (r.URL.Path == "/" || r.URL.Path == "/index.html") && r.Method == "GET" {
		// Serve the default page if we receive a GET request at the base URI
		http.ServeFile(w, r, s.GetDefaultPage())
	}
	http.Error(w, "Not Found", http.StatusNotFound)
	return
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

	// Handle requests to the base uri
	for url, fileID := range cf.Payloads {
		localFileID := fileID
		localURL := url
		logging.LogInfo("Hosting file", "path", url, "uuid", localFileID)
		http.HandleFunc(localURL, s.ServeFileWrapper(localFileID))
	}
	http.HandleFunc("/", s.ServeDefaultPage)
	// Handle requests to the websockets uri
	logging.LogInfo("Serving websocket", "path", s.SocketURI)
	http.HandleFunc(fmt.Sprintf("/%s", s.SocketURI), s.SocketHandler)

	// Setup all the options according to the configuration
	if !strings.Contains(cf.SSLKey, "") && !strings.Contains(cf.SSLCert, "") {

		// copy the key and cert to the local directory
		if keyFile, err := os.Open(cf.SSLKey); err != nil {
			log.Println("Unable to open key file ", err.Error())
		} else if keyfile, err := io.ReadAll(keyFile); err != nil {
			log.Println("Unable to read key file ", err.Error())
		} else if err = os.WriteFile("key.pem", keyfile, 0644); err != nil {
			log.Println("Unable to write key file ", err.Error())
		} else if certFile, err := os.Open(cf.SSLCert); err != nil {
			log.Println("Unable to open cert file ", err.Error())
		} else if certfile, err := io.ReadAll(certFile); err != nil {
			log.Println("Unable to read cert file ", err.Error())
		} else if err = os.WriteFile("cert.pem", certfile, 0644); err != nil {
			log.Println("Unable to write cert file ", err.Error())
		}
	}

	if cf.UseSSL {
		err := httpscerts.Check("cert.pem", "key.pem")
		if err != nil {
			if s.Debug {
				log.Println(fmt.Sprintf("Error for cert.pem or key.pem %s", err.Error()))
			}
			err = httpscerts.Generate("cert.pem", "key.pem", cf.BindAddress)
			if err != nil {
				log.Fatal("Error generating https cert")
				os.Exit(1)
			}
		}
		if s.Debug {
			log.Println(fmt.Sprintf("Starting SSL server at https://%s and wss://%s", cf.BindAddress, cf.BindAddress))
		}
		err = http.ListenAndServeTLS(cf.BindAddress, "cert.pem", "key.pem", nil)
		if err != nil {
			log.Fatal("Failed to start raven server: ", err)
		}
	} else {
		if s.Debug {
			log.Println(fmt.Sprintf("Starting server at http://%s and ws://%s", cf.BindAddress, cf.BindAddress))
		}
		err := http.ListenAndServe(cf.BindAddress, nil)
		if err != nil {
			log.Fatal("Failed to start websocket server: ", err)
		}
	}
}
