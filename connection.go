package apns

import (
	"crypto/tls"
	"encoding/binary"
	"log"
	"strings"
	"net"
	"time"
)

//ResponseQueueSize indicates how many APNS responses may be buffered.
var ResponseQueueSize = 10000

//SentBufferSize is the maximum number of sent notifications which may be buffered.
var SentBufferSize = 10000

var maxBackoff = 20 * time.Second

//Connection represents a single connection to APNS.
type Connection struct {
	Client
	conn            *tls.Conn
	connAux         net.Conn
	queue           chan PushNotification
	errors          chan BadPushNotification
	responses       chan Response
	shouldReconnect chan bool
	stopping        chan bool
	stopped         chan bool
	senderFinished  chan bool
	ackFinished     chan bool

}

//NewConnection initializes an APNS connection. Use Connection.Start() to actually start sending notifications.
func NewConnection(client *Client) *Connection {
	c := new(Connection)
	log.Printf("CREATING NEW CONNN...\n")
	c.Client = *client
	c.queue = make(chan PushNotification, 10000)
	c.errors = make(chan BadPushNotification, 10000)
	c.responses = make(chan Response, ResponseQueueSize)
	c.shouldReconnect = make(chan bool)
	c.stopping = make(chan bool)
	c.stopped = make(chan bool)

	c.senderFinished = make(chan bool)
	c.ackFinished = make(chan bool)

	return c
}

//Response is a reply from APNS - see apns.ApplePushResponses.
type Response struct {
	Status     uint8
	Identifier uint32
}

func newResponse() Response {
	r := Response{}
	return r
}

//BadPushNotification represents a notification which APNS didn't like.
type BadPushNotification struct {
	PushNotification
	Status uint8
}

type timedPushNotification struct {
	PushNotification
	time.Time
}

func (pn PushNotification) timed() timedPushNotification {
	return timedPushNotification{PushNotification: pn, Time: time.Now()}
}

//Enqueue adds a push notification to the end of the "sending" queue.
func (conn *Connection) Enqueue(pn *PushNotification) {
	go func(pn *PushNotification) {
		conn.queue <- *pn
	}(pn)
}

//Errors gives you a channel of the push notifications Apple rejected.
func (conn *Connection) Errors() (errors <-chan BadPushNotification) {
	return conn.errors
}

//Start initiates a connection to APNS and asnchronously sends notifications which have been queued.
func (conn *Connection) Start() error {
	//Connect to APNS. The reason this is here as well as in sender is that this probably catches any unavoidable errors in a synchronous fashion, while in sender it can reconnect after temporary errors (which should work most of the time.)
	err := conn.connect()
	if err != nil {
		log.Fatal("Failed to connect due to: %+v\n", err)
		return err
	}
	//Start sender goroutine
	go conn.sender(conn.queue)
	//Start limbo goroutine
	go conn.limbo(conn.responses, conn.errors, conn.queue)
	return nil
}

//Stop gracefully closes the connection - it waits for the sending queue to clear, and then shuts down.
func (conn *Connection) Stop() chan bool {
	log.Println("apns: shutting down.")
	conn.stopping <- true
	return conn.stopped
	//Thought: Don't necessarily need a channel here. Could signal finishing by closing errors?
}

func (conn *Connection) sender(queue <-chan PushNotification) {
	stopping := false
	defer conn.conn.Close()
	log.Println("Starting sender")
	for {
		select {
		case pn, ok := <-conn.queue:
			if !ok {
				log.Println("Not okay; queue closed.")
				//That means the Connection is stopped
				//close sent?
				return
			}
			//This means we saw a response; connection is over.
			select {
			case <-conn.shouldReconnect:
				conn.conn.Close()
				conn.conn = nil
				conn.spinUntilReconnect()
			default:
			}
			//Then send the push notification
			pn.Priority = 10
			payload, err := pn.ToBytes()
			if err != nil {
				log.Println(err)
				//Should report this on the bad notifications channel probably
			} else {
				if conn.conn == nil {
					conn.spinUntilReconnect()
				}
				ps, _ := pn.PayloadString()
				log.Printf("Sending token: %s identi: %d payload: %s -> %s\n", pn.DeviceToken, pn.Identifier, ps, string(payload))
				_, err := conn.conn.Write(payload)
				if err != nil {
					
					log.Printf("ERRORS IS: %s\n", err)
					go func() {
						conn.shouldReconnect <- true
					}()
					//Disconnect?
				} 
				if stopping && len(queue) == 0 {
					log.Println("sender: I'm stopping and I've run out of things to send. Let's see if limbo is empty.")
					conn.senderFinished <- true
				}
			}
		case <-conn.stopping:
			log.Println("sender: Got a stop message!")
			stopping = true
			if len(queue) == 0 {
				log.Println("sender: I'm stopping and I've run out of things to send. Let's see if limbo is empty.")
				conn.senderFinished <- true
			}
		case <-conn.ackFinished:
			log.Println("sender: limbo is empty!")
			if len(queue) == 0 {
				log.Println("sender: limbo is empty and so am I!")
				return
			}
		}
	}
}

func (conn *Connection) reader(responses chan<- Response) {
	buffer := make([]byte, 6)
	for {		
		n, err := conn.conn.Read(buffer)
		if err != nil && n < 6 {
			log.Println("APNS: Error before reading complete response", n, err)
			conn.conn.Close()
			conn.conn = nil
			return
		}
		command := uint8(buffer[0])
		if command != 8 {
			log.Println("Something went wrong: command should have been 8; it was actually", command)
		}
		resp := newResponse()
		resp.Identifier = binary.BigEndian.Uint32(buffer[2:6])
		resp.Status = uint8(buffer[1])
		responses <- resp
		conn.shouldReconnect <- true
		return
	}
}

func (conn *Connection) limbo(responses chan Response, errors chan BadPushNotification, queue chan PushNotification) {
	stopping := false
	for {
		select {
		case <-conn.senderFinished:
			//senderFinished means the sender thinks it's done.
			//However, sender might not be - limbo could resend some, if there are any left here.
			//So we just take note of this until limbo is empty too.
			stopping = true
		case resp, ok := <-responses:
			if !ok && stopping {
				conn.ackFinished <- true
				//If the responses channel is closed,
				//that means we're shutting down the connection.
			}
			log.Printf("GOT A BAD RESPONSE IDENT: %d STATUS: %d\n", resp.Identifier, resp.Status)
			if resp.Status != 10 {
				//It was an error, we should report this on the error channel
				bad := BadPushNotification{Status: resp.Status}
				go func(bad BadPushNotification) {
					errors <- bad
				}(bad)
			}			
		}
	}
}

func (conn *Connection) connect() error {
	if conn.conn != nil {
		conn.conn.Close()
	}

	var cert tls.Certificate
	var err error
	if len(conn.CertificateBase64) == 0 && len(conn.KeyBase64) == 0 {
		// The user did not specify raw block contents, so check the filesystem.
		cert, err = tls.LoadX509KeyPair(conn.CertificateFile, conn.KeyFile)
	} else {
		// The user provided the raw block contents, so use that.
		cert, err = tls.X509KeyPair([]byte(conn.CertificateBase64), []byte(conn.KeyBase64))
	}

	if err != nil {
		log.Fatal("Failed to obtain cert: %+v\n", err)
		return err
	}

	conf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName: strings.Split(conn.Gateway, ":")[0],
	}

	connAux, err := net.Dial("tcp", conn.Gateway)
	if err != nil {
		log.Fatal("Failed while dialing %s with error: %+v\n", conn.Gateway, err)
		return err
	}
	tlsConn := tls.Client(connAux, conf)
	err = tlsConn.Handshake()
	if err != nil {
		log.Fatal("Failed while handshaking %+v...\n", err)
		_ = tlsConn.Close()
		return err
	}
	conn.conn = tlsConn
	conn.connAux = connAux
	//Start reader goroutine
	go conn.reader(conn.responses)
	return nil
}

func (c *Connection) spinUntilReconnect() {
	var backoff = time.Duration(100)
	for {
		log.Println("Connection lost; reconnecting.")
		err := c.connect()
		if err != nil {
			//Exponential backoff up to a limit
			log.Println("APNS: Error connecting to server: ", err)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			time.Sleep(backoff)
		} else {
			backoff = 100
			log.Println("Connected...")
			break
		}
	}
}
