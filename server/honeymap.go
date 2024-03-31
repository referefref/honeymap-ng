package main

import (
	"context"
	"encoding/json"
	"github.com/fw42/go-hpfeeds"
	"io/ioutil"
	"log"
	"net/http"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
	"os"
	"path"
	"runtime"
	"sync"
	"time"
)

const staticDir = "../client"
const bind = "0.0.0.0:3000"

type Config struct {
	Host  string
	Port  int
	Ident string
	Auth  string
}

func dirname() string {
	_, myself, _, _ := runtime.Caller(1)
	return path.Dir(myself)
}

func readConfig() Config {
	blob, err := ioutil.ReadFile(dirname() + "/" + "config.json")
	checkFatalError(err)

	var conf Config
	err = json.Unmarshal(blob, &conf)
	checkFatalError(err)

	return conf
}

func checkFatalError(err error) {
	if err != nil {
		log.Printf("%s\n", err)
		os.Exit(-1)
	}
}

var clients sync.Map

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("WebSocket accept error: %v", err)
		return
	}

	clients.Store(conn, struct{}{})

	defer func() {
		clients.Delete(conn)
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, _, err := conn.Read(context.Background())
		if err != nil {
			break
		}
	}
}

func broadcast(input chan hpfeeds.Message) {
	for msg := range input {
		clients.Range(func(key, value interface{}) bool {
			conn, ok := key.(*websocket.Conn)
			if !ok {
				return true
			}

			go func(conn *websocket.Conn) {
				err := wsjson.Write(context.Background(), conn, msg.Payload)
				if err != nil {
					log.Printf("Error broadcasting message: %v", err)
				}
			}(conn)
			return true
		})
	}
}

func hpfeedsConnect(config Config, geolocEvents chan hpfeeds.Message) {
	backoff := 0
	hp := hpfeeds.NewHpfeeds(config.Host, config.Port, config.Ident, config.Auth)
	hp.Log = true
	log.Printf("Connecting to %s:%d...\n", config.Host, config.Port)
	for {
		err := hp.Connect()
		if err == nil {
			log.Printf("Connected to Hpfeeds server.")
			hp.Subscribe("geoloc.events", geolocEvents)
			<-hp.Disconnected
			log.Printf("Lost connection to %s:%d :-(\n", config.Host, config.Port)
		} else {
			log.Printf("Connection failed: %v", err)
		}
		log.Printf("Reconnecting in %d seconds...", backoff)
		time.Sleep(time.Duration(backoff) * time.Second)
		if backoff < 10 {
			backoff++
		}
	}
}

func main() {
	config := readConfig()

	http.Handle("/", http.FileServer(http.Dir(dirname() + "/" + staticDir + "/")))
	http.HandleFunc("/data", wsHandler)

	geolocEvents := make(chan hpfeeds.Message)
	go hpfeedsConnect(config, geolocEvents)
	go broadcast(geolocEvents)

	log.Printf("Binding Honeymap webserver to %s...", bind)
	err := http.ListenAndServe(bind, nil)
	checkFatalError(err)
}
