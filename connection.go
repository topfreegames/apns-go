package apns

import (
	"crypto/tls"
	"encoding/binary"
	"strings"
	"net"
	"time"
  "github.com/uber-go/zap"
)

//ResponseQueueSize indicates how many APNS responses may be buffered.
var ResponseQueueSize = 10000

//SentBufferSize is the maximum number of sent notifications which may be buffered.
var SentBufferSize = 10000

var maxBackoff = 20 * time.Second

//Connection represents a single connection to APNS.
type Connection struct {
	Client
	id              int
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
func NewConnection(client *Client, id int, errorQueue chan BadPushNotification) *Connection {
	c := new(Connection)
	c.Client = *client
	c.id = id
	c.queue = make(chan PushNotification, 10000)
	c.errors = errorQueue
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
	conn.queue <- *pn
}

//Errors gives you a channel of the push notifications Apple rejected.
func (conn *Connection) Errors() (errors <-chan BadPushNotification) {
	return conn.errors
}

//Start initiates a connection to APNS and asnchronously sends notifications which have been queued.
func (conn *Connection) Start(logger zap.Logger) error {
	//Connect to APNS. The reason this is here as well as in sender is that this probably catches any unavoidable errors in a synchronous fashion, while in sender it can reconnect after temporary errors (which should work most of the time.)
	err := conn.connect(logger)
	if err != nil {
    logger.Fatal("APNS: Failed to connect",
      zap.Int("connectionId", conn.id),
      zap.Error(err),
    )
		return err
	}
	//Start sender goroutine
	sent := make(chan PushNotification, 10000)
	go conn.sender(conn.queue, sent, logger)
	//Start limbo goroutine
	go conn.limbo(sent, conn.responses, conn.errors, conn.queue, logger)
	return nil
}

//Stop gracefully closes the connection - it waits for the sending queue to clear, and then shuts down.
func (conn *Connection) Stop(logger zap.Logger) chan bool {
  logger.Info("APNS: Shutting down one of the connections",
    zap.Int("connectionId", conn.id),
  )
	conn.stopping <- true
	return conn.stopped
	//Thought: Don't necessarily need a channel here. Could signal finishing by closing errors?
}

func (conn *Connection) sender(queue <-chan PushNotification, sent chan PushNotification, logger zap.Logger) {
	i := 0
	stopping := false
	defer conn.conn.Close()
	defer conn.connAux.Close()
  logger.Info("APNS: Starting sender for connection",
    zap.Int("connectionId", conn.id),
  )
	for {
		select {
		case pn, ok := <-conn.queue:
			if !ok {
        logger.Info("APNS: Connection not okay; queue closed",
          zap.Int("connectionId", conn.id),
        )
				//That means the Connection is stopped
				//close sent?
				return
			}
			//This means we saw a response; connection is over.
			select {
			case <-conn.shouldReconnect:
				conn.conn.Close()
				conn.conn = nil
				conn.connAux.Close()
				conn.connAux = nil
				conn.spinUntilReconnect(logger)
			default:
			}
			//Then send the push notification
			pn.Priority = 10
			payload, err := pn.ToBytes()
			if err != nil {
        logger.Info("APNS: Connection Error",
          zap.Int("connectionId", conn.id),
          zap.Error(err),
        )
				//Should report this on the bad notifications channel probably
			} else {
				if conn.conn == nil {
					conn.spinUntilReconnect(logger)
				}
				_, err = conn.conn.Write(payload)
				if err != nil {
          logger.Info("APNS: Error writing payload",
            zap.Error(err),
          )
					go func() {
						conn.shouldReconnect <- true
					}()
					//Disconnect?
				} else {
					i++
					sent <- pn
					if stopping && len(queue) == 0 {
						conn.senderFinished <- true
					}
				}
			}
		case <-conn.stopping:
      logger.Info("APNS: Connection sender - Got a stop message",
        zap.Int("connectionId", conn.id),
      )
			stopping = true
			if len(queue) == 0 {
        logger.Info("APNS: Connection sender - Stopping because ran out of things to send. Let's see if limbo is empty",
          zap.Int("connectionId", conn.id),
        )
				conn.senderFinished <- true
			}
		case <-conn.ackFinished:
      logger.Info("APNS: Connection sender - limbo is empty",
        zap.Int("connectionId", conn.id),
      )
			if len(queue) == 0 {
        logger.Info("APNS: Connection sender - limbo is empty and so am I",
          zap.Int("connectionId", conn.id),
        )
				close(sent)
				return
			}
		}
	}
}

func (conn *Connection) reader(responses chan<- Response, logger zap.Logger) {
	buffer := make([]byte, 6)
	for {
		n, err := conn.conn.Read(buffer)
		if err != nil && n < 6 {
      logger.Info("APNS: Connection error before reading complete response",
        zap.Int("connectionId", conn.id),
        zap.Int("n", n),
        zap.Error(err),
      )
			conn.shouldReconnect <- true
			return
    } else if err != nil {
      logger.Info("APNS: Connection error before reading complete response",
        zap.Int("connectionId", conn.id),
        zap.Error(err),
      )
    }
		command := uint8(buffer[0])
		if command != 8 {
      logger.Info("APNS: Something went wrong in a connection - Command should have been 8 but it had other value instead",
        zap.Int("connectionId", conn.id),
        zap.Object("commandValue", command),
      )
		}
		resp := newResponse()
		resp.Identifier = binary.BigEndian.Uint32(buffer[2:6])
		resp.Status = uint8(buffer[1])
		responses <- resp
		conn.shouldReconnect <- true
		return
	}
}

func (conn *Connection) limbo(sent <-chan PushNotification, responses chan Response, errors chan BadPushNotification, queue chan PushNotification, logger zap.Logger) {
	stopping := false
	limbo := make([]timedPushNotification, 0, SentBufferSize)
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case pn, ok := <-sent:
			limbo = append(limbo, pn.timed())
			stopping = false
			if !ok {
        logger.Info("APNS: Connection limbo - sent is closed, so sender is done. So am I, then",
          zap.Int("connectionId", conn.id),
        )
				close(errors)
				conn.stopped <- true
				return
			}
		case <-conn.senderFinished:
			//senderFinished means the sender thinks it's done.
			//However, sender might not be - limbo could resend some, if there are any left here.
			//So we just take note of this until limbo is empty too.
			stopping = true
		case resp, ok := <-responses:
			if !ok {
				//If the responses channel is closed,
				//that means we're shutting down the connection.
			}
			for i, pn := range limbo {
				if pn.Identifier == resp.Identifier {
					if resp.Status != 10 {
						//It was an error, we should report this on the error channel
						bad := BadPushNotification{PushNotification: pn.PushNotification, Status: resp.Status}
						errors <- bad
					}
					if len(limbo) > i {
						toRequeue := len(limbo) - (i + 1)
						if toRequeue > 0 {
							conn.requeue(limbo[i+1:])
							//We resent some notifications: that means we should wait for sender to tell us it's done, again.
							stopping = false
						}
					}
				}
			}
			limbo = make([]timedPushNotification, 0, SentBufferSize)
		case <-ticker.C:
			flushed := false
			for i := range limbo {
				if limbo[i].After(time.Now().Add(-TimeoutSeconds * time.Second)) {
					if i > 0 {
						newLimbo := make([]timedPushNotification, len(limbo[i:]), SentBufferSize)
						copy(newLimbo, limbo[i:])
						limbo = newLimbo
						flushed = true
						break
					}
				}
			}
			if !flushed {
				limbo = make([]timedPushNotification, 0, SentBufferSize)
			}
			if stopping && len(limbo) == 0 {
				//sender() is finished and so is limbo - so the connection is done.
        logger.Info("APNS: Connection limbo - I've flushed all my notifications. Tell sender I'm done",
          zap.Int("connectionId", conn.id),
        )
				conn.ackFinished <- true
			}
		}
	}
}

func (conn *Connection) requeue(queue []timedPushNotification) {
	for _, pn := range queue {
		conn.Enqueue(&pn.PushNotification)
	}
}

func (conn *Connection) connect(logger zap.Logger) error {
	if conn.conn != nil {
		conn.conn.Close()
	}
	if conn.connAux != nil {
		conn.connAux.Close()
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
    logger.Fatal("APNS: Failed to obtain certificate",
      zap.Error(err),
    )
		return err
	}

	conf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ServerName: strings.Split(conn.Gateway, ":")[0],
	}

	connAux, err := net.Dial("tcp", conn.Gateway)
	if err != nil {
    logger.Fatal("APNS: Failed while dialing gateway",
      zap.String("gateway", conn.Gateway),
      zap.Error(err),
    )
		return err
	}
	tlsConn := tls.Client(connAux, conf)
	err = tlsConn.Handshake()
	if err != nil {
    logger.Fatal("APNS: Failed while handshaking",
      zap.Error(err),
    )
		_ = tlsConn.Close()
		return err
	}
	conn.conn = tlsConn
	conn.connAux = connAux
	//Start reader goroutine
	go conn.reader(conn.responses, logger)
	return nil
}

func (c *Connection) spinUntilReconnect(logger zap.Logger) {
	var backoff = time.Duration(100)
	for {
    logger.Info("APNS: Connection lost. Reconnecting",
      zap.Int("connectionId", c.id),
    )
		err := c.connect(logger)
		if err != nil {
			//Exponential backoff up to a limit
      logger.Info("APNS: Error connecting to server",
        zap.Int("connectionId", c.id),
        zap.Error(err),
      )
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			time.Sleep(backoff)
		} else {
			backoff = 100
      logger.Info("APNS: New connection established",
        zap.Int("connectionId", c.id),
      )
			break
		}
	}
}
