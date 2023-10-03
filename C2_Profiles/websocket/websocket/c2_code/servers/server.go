package servers

// C2Config - struct for server configuration
type C2Config struct {
	Instances []C2ConfigEntry `json:"instances"`
}
type C2ConfigEntry struct {
	BindAddress string            `json:"bindaddress"`
	SocketURI   string            `json:"websocketuri"`
	SSLKey      string            `json:"sslkey"`
	SSLCert     string            `json:"sslcert"`
	UseSSL      bool              `json:"usessl"`
	Defaultpage string            `json:"defaultpage"`
	Logfile     string            `json:"logfile"`
	Debug       bool              `json:"debug"`
	Payloads    map[string]string `json:"payloads"`
}

// Server - interface used for all c2 profiles
type Server interface {
	MythicBaseURL() string
	SetMythicBaseURL(url string)
	Run(cf C2ConfigEntry)
}

// Message - struct definition for messages between clients and the server
type Message struct {
	Data string `json:"data"`
}

func NewInstance() Server {
	return newServer()
}
