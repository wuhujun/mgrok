package server

import (
	"fmt"
	"net"

	vhost "github.com/inconshreveable/go-vhost"
	//"net"

	"mgrok/log"
	"strings"
	"time"
)

const (
	notAuthorized = `HTTP/1.0 401 Not Authorized
WWW-Authenticate: Basic realm="mgrok"
Content-Length: 23

Authorization required
`

	notFound = `HTTP/1.0 404 Not Found
Content-Length: %d

Tunnel %s not found
`

	badRequest = `HTTP/1.0 400 Bad Request
Content-Length: 12

Bad Request
`
)

// Listens for new http(s) connections from the public internet
func startHTTPListener(addr string) (addr1 net.Addr) {
	var err error
	// var addr1 net.Addr
	if addr1, err = listen(addr, "pub"); err != nil {
		panic(err)
	}

	log.Info("Listening for public http connections on %v", addr1.String())

	return
}

func listen(addr, typ string) (addr1 net.Addr, err error) {
	// listen for incoming connections
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return
	}

	// l = &conn.Listener{
	// 	Addr: listener.Addr(),
	// }

	addr1 = listener.Addr()

	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				log.Error("Failed to accept new TCP connection of type %s: %v", typ, err)
				continue
			}

			go httpHandler(c, "http")

			log.Info("New connection from %v", c.RemoteAddr())
		}
	}()

	return
}

// Handles a new http connection from the public internet
func httpHandler(c net.Conn, proto string) {
	defer c.Close()
	defer func() {
		// recover from failures
		if r := recover(); r != nil {
			log.Warn("httpHandler failed with error %v", r)
		}
	}()

	// Make sure we detect dead connections while we decide how to multiplex
	c.SetDeadline(time.Now().Add(connReadTimeout))

	// multiplex by extracting the Host header, the vhost library
	vhostConn, err := vhost.HTTP(c)
	if err != nil {
		log.Warn("Failed to read valid %s request: %v", proto, err)
		c.Write([]byte(badRequest))
		return
	}

	// read out the Host header and auth from the request
	host := vhostConn.Request.Header.Get("X-Host")
	if len(host) == 0 {
		host = vhostConn.Host()
	}
	host = strings.ToLower(host)
	auth := vhostConn.Request.Header.Get("Authorization")

	// done reading mux data, free up the request memory
	vhostConn.Free()

	// We need to read from the vhost conn now since it mucked around reading the stream
	c = vhostConn //conn.Wrap(vhostConn, "pub")

	// multiplex to find the right backend host
	log.Debug("Found hostname %s in request", host)
	tunnel := tunnelRegistry.get(fmt.Sprintf("%s://%s", proto, host))
	if tunnel == nil {
		log.Info("No tunnel found for hostname %s", host)
		c.Write([]byte(fmt.Sprintf(notFound, len(host)+18, host)))
		return
	}

	// If the client specified http auth and it doesn't match this request's auth
	// then fail the request with 401 Not Authorized and request the client reissue the
	// request with basic authdeny the request
	if tunnel.req.HTTPAuth != "" && auth != tunnel.req.HTTPAuth {
		log.Info("Authentication failed: %s", auth)
		c.Write([]byte(notAuthorized))
		return
	}

	// dead connections will now be handled by tunnel heartbeating and the client
	c.SetDeadline(time.Time{})

	// let the tunnel handle the connection now
	tunnel.handlePublicConnection(c)
}
