package apns

import "crypto/tls"
import "net"

// Abstracts a connection to a push notification gateway.
type Connection struct {
	gateway        string
	timeoutSeconds uint8

	TlsConfig *tls.Config
	
	tcpConnection  net.Conn
	socket         *tls.Conn
}

// Constructor. Pass the Apple production or sandbox gateways,
// or an endpoint of your choosing.
//
// Returns a Connection struct with the gateway configured and the
// push notification response timeout set to TIMEOUT_SECONDS.
func NewConnection(gateway string, config *tls.Config, timeoutSeconds uint8) (c *Connection) {
	c = new(Connection)
	c.gateway = gateway
	c.TlsConfig = config
	c.timeoutSeconds = timeoutSeconds
	return
}

// Sets the number of seconds to wait for the gateway to respond
// after sending a push notification.
func (connection *Connection) SetTimeoutSeconds(seconds uint8) {
	connection.timeoutSeconds = seconds
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

// Attempts to send a push notification to the connection's gateway.
// Apple will not respond if the push notification payload is accepted,
// so a timeout channel pattern is used. Two goroutines are started: one
// waits for connection.timeoutSeconds and the other listens for a response from Apple.
// Whichever returns first is the winner, so some false-positives are possible
// if the gateway takes an excessively long amount of time to reply.
//
// Returns a PushNotificationResponse indicating success / failure and what
// error occurred, if any.
func (connection *Connection) Send(pn *PushNotification) (resp *PushNotificationResponse) {
	resp = new(PushNotificationResponse)
	resp.Success = false

	payload, err := pn.ToBytes()
	if err != nil {
		resp.Error = err
		return
	}

	_, err = connection.socket.Write(payload)
	if err != nil {
		resp.Error = err
		return
	}

	return resp
}

// Disconnects from the gateway.
func (connection *Connection) Disconnect() {
	connection.tcpConnection.Close()
	connection.socket.Close()
}
