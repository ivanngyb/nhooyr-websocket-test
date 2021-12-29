package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

const (

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 1024
)

type Client struct {
	Id     uint64
	Idsent bool
	hub    *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte
}

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close(websocket.StatusNormalClosure, "")
	}()
	c.conn.SetReadLimit(maxMessageSize)
	ctx := context.Background()

	for {
		_, msg, err := c.conn.Read(ctx)
		if err != nil {
			break
		}
		msg = bytes.TrimSpace(bytes.Replace(msg, newline, space, -1))
		log.Println(msg)
		c.hub.broadcast <- msg
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.hub.clientNum = removeClient(c.hub.clientNum, strconv.Itoa(int(c.Id)))
		c.conn.Close(websocket.StatusNormalClosure, "")
		log.Println("Client left: ", c.hub.clientNum)
	}()
	ctx := context.Background()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}

			if c.Idsent {
				c.conn.Write(ctx, websocket.MessageBinary, msg)
			} else if !c.Idsent {
				var curPlayers = strings.Join(c.hub.clientNum, "_")
				message := fmt.Sprintf("PlayerID_%d_%s", int(c.Id), curPlayers)
				c.conn.Write(ctx, websocket.MessageBinary, []byte(message))
				c.Idsent = true
			}
		case <-ticker.C:
			if err := c.conn.Write(ctx, websocket.MessageBinary, nil); err != nil {
				return
			}
		}
	}
}

func removeClient(s []string, r string) []string {
	for i, v := range s {
		if v == r {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

var id uint64

func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	log.Println("New connection!")
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Println("Error: ", err)
		return
	}
	client := &Client{hub: hub, conn: c, send: make(chan []byte, 256), Id: id}
	client.hub.register <- client

	client.hub.clientNum = append(client.hub.clientNum, strconv.Itoa(int(id)))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		atomic.AddUint64(&id, 1)
		wg.Done()
	}()

	wg.Wait()

	go client.readPump()
	go client.writePump()
}
