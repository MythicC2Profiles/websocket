package main

import (
	"encoding/json"
	"github.com/MythicC2Profiles/websocket/servers"
	"github.com/MythicMeta/MythicContainer/logging"
	"io"
	"os"
)

func main() {
	c2config := servers.C2Config{}
	cf, err := os.Open("config.json")
	if err != nil {
		logging.LogError(err, "Error opening config file")
		os.Exit(-1)
	}
	config, err := io.ReadAll(cf)
	if err != nil {
		logging.LogError(err, "Error in reading config file")
		os.Exit(-1)
	}
	err = json.Unmarshal(config, &c2config)
	if err != nil {
		logging.LogError(err, "Error in unmarshal call for config")
		os.Exit(-1)
	}
	// start the server instance with the config
	for i, _ := range c2config.Instances {
		c2server := servers.NewInstance()
		go c2server.Run(c2config.Instances[i])
	}
	forever := make(chan bool)
	<-forever

}
