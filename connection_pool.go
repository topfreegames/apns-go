package apns

import "sync"
import "log"

type ConnectionPool struct {
	sync.Mutex
	client *Client

	NumConnections int
	
	connections chan *Connection
	pushQueue chan *PushNotification
	responseQueue chan BadPushNotification

	stopped sync.WaitGroup
}

func NewConnectionPool(client *Client, numConnections int) (*ConnectionPool, error) {
	connections := make(chan *Connection, numConnections)
	responseQueue := make(chan BadPushNotification, 10000)

	for i := numConnections; i >= 1; i-- {
		newConnection := NewConnection(client, i, responseQueue)
		err := newConnection.Start()
		if err != nil {
			return nil, err
		}
		connections <- newConnection
	}
	connPool := &ConnectionPool {
		client: client,
		NumConnections: numConnections,
		connections: connections,
		pushQueue: make(chan *PushNotification, numConnections * 3),
		responseQueue: responseQueue,
	}
	connPool.Start()
	return connPool, nil
}

func (conn_pool *ConnectionPool) Start() {
	log.Println("Starting pool of connections...")	
	go conn_pool.sendLoop()
}

func (conn_pool *ConnectionPool) Errors() <-chan BadPushNotification {
	return conn_pool.responseQueue
}

func (conn_pool *ConnectionPool) Enqueue(pn *PushNotification) {
	conn_pool.pushQueue <- pn
}

func (conn_pool *ConnectionPool) sendLoop() {
	conn_pool.stopped.Add(1)
	defer conn_pool.stopped.Done()
	for {
		pn, ok := <- conn_pool.pushQueue
		if !ok {
			return
		}
		conn := <- conn_pool.connections
		conn.Enqueue(pn)
		// release connection
		conn_pool.connections <- conn
	}	
}

func (conn_pool *ConnectionPool) Stop() {
	close(conn_pool.pushQueue)
	conn_pool.stopped.Wait()
	close(conn_pool.connections)
	for {
		conn, ok := <- conn_pool.connections
		if !ok {
			break
		}
		<- conn.Stop()
	}	
}


