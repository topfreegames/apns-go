package apns

import "crypto/tls"
import "net"
import "time"
import log "github.com/Sirupsen/logrus"
// Abstracts a connection to a push notification gateway.
type Connection struct {
	gateway        string
	timeoutResponse time.Duration

	TlsConfig *tls.Config

	responses chan []byte
	
	tcpConnection  net.Conn
	socket         *tls.Conn
}

// Constructor. Pass the Apple production or sandbox gateways,
// or an endpoint of your choosing.
//
// Returns a Connection struct with the gateway configured and the
// push notification response timeout set to TIMEOUT_SECONDS.
func NewConnection(gateway string, config *tls.Config, timeoutResponse time.Duration, responses chan []byte) (c *Connection) {
	c = new(Connection)
	c.gateway = gateway
	c.TlsConfig = config
	c.timeoutResponse = timeoutResponse
	c.responses = responses
	return
}


// Creates and sets connections to the gateway. Apple treats repeated
// connections / disconnections as a potential DoS attack; you should
// connect and reuse the connection for multiple push notifications.
//
// Returns an error if there is a problem connecting to the gateway.
func (connection *Connection) Connect() (err error) {
	conn, err := net.Dial("tcp", connection.gateway)
	if err != nil {
		return err
	}
	tlsConn := tls.Client(conn, connection.TlsConfig)
	err = tlsConn.Handshake()
	if err != nil {
		return err
	}
	connection.tcpConnection = conn
	connection.socket = tlsConn

	return nil
}

func (connection *Connection) Reconnect() (err error) {
	connection.Disconnect()
	return connection.Connect()
}

// Attempts to send a push notification to the connection's gateway.
// Apple will not respond if the push notification payload is accepted,
// so a timeout channel pattern is used. Two goroutines are started: one
// waits for connection.timeoutSeconds and the other listens for a response from Apple.
// Whichever returns first is the winner, so some false-positives are possible
// if the gateway takes an excessively long amount of time to reply.
//
// Returns a PushNotificationResponse indicating success / failure and what
// error occurred, if any.
func (connection *Connection) Send(pn *PushNotification) error {
	payload, err := pn.ToBytes()
	if err != nil {
		return nil
	}

	connection.socket.Handshake()
	_, err = connection.socket.Write(payload)
	if err != nil {
		return err
	}
	go connection.Rec()
	
	return nil
}

func (connection *Connection) Rec()  {
	responseChan := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, 6, 6)
		connection.socket.Read(buffer)
		responseChan <- buffer
	}()
	select {
	case buffer := <- responseChan:
		connection.responses <- []byte(APPLE_PUSH_RESPONSES[buffer[1]])
	case <- time.After(5 * time.Second):
	}
}

// Disconnects from the gateway.
func (connection *Connection) Disconnect() {
	log.Info("Closing TCP connection.")
	connection.tcpConnection.Close()
	log.Info("Closing TLS/TCP Connection.")
	connection.socket.Close()
	log.Info("Successfully closed connection.")
}
