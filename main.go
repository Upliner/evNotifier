package evNotifier

import (
	"log"
	"net"
	"time"
)

func main() {

	ln, err := net.Listen("unix", "/tmp/evsock")
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept failed:", err)
		}
		go handleConnection(conn)
	}
}

var ch = make(chan struct{})

func handleConnection(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(time.Second * 2))
	var b [1]byte
	n, err := conn.Read(b[:])
	if err != nil {
		log.Println("Failed to candle conn:", err)
	}
	if n == 0 {
		log.Println("Nothing was sent to us")
	}
	switch b[0] {
	case byte('w'):
		select {
		case <-ch:
		case <-time.After(time.Second * 20):
		}
	case byte('n'):
		close(ch)
		ch = make(chan struct{})
	}

}
