package apns

import "crypto/tls"
import "strings"
import "time"
import "sync"

type ConnectionPool struct {
	sync.Mutex
	
	NumConnections int
	Gateway string

	TlsConfig *tls.Config
	
	connections chan *Connection
	pushQueue chan *PushNotification
	responseQueue chan []byte

	stopper chan bool
}

func NewConnectionPool(numConnections int, gateway string, certificate tls.Certificate) *ConnectionPool {
	config := &tls.Config {
		Certificates: []tls.Certificate{certificate},
		ServerName: strings.Split(gateway, ":")[0],
	}

	connections := make(chan *Connection, numConnections)
	responseQueue := make(chan []byte, 1000)
	
	for i := numConnections; i >= 1; i-- {
		newConnection := NewConnection(gateway, config, 2 * time.Second, responseQueue)					
		newConnection.Connect()
		connections <- newConnection
	}
	
	return &ConnectionPool {
		NumConnections: numConnections,
		Gateway: gateway,
		TlsConfig: config,
		connections: connections,
		pushQueue: make(chan *PushNotification, numConnections * 2),
		responseQueue: responseQueue,
	}
}

func (connection_pool *ConnectionPool) Start() {
	go connection_pool.sendLoop()
}

func (connection_pool *ConnectionPool) GetResponses() chan []byte {
	return connection_pool.responseQueue
}

func (connection_pool *ConnectionPool) SendMessage(pushNotification *PushNotification) {
	connection_pool.pushQueue <- pushNotification
}

func (connection_pool *ConnectionPool) getConnection() *Connection {
	connection_pool.Lock()
	defer connection_pool.Unlock()
	connection := <- connection_pool.connections
	return connection
}

func (connection_pool *ConnectionPool) releaseConnection(connection *Connection) {
	connection_pool.connections <- connection
}

func (connection_pool *ConnectionPool) acquireNewConnection() {
	newConnection := NewConnection(
		connection_pool.Gateway,
		connection_pool.TlsConfig,
		2 * time.Second,
		connection_pool.responseQueue)
	newConnection.Connect()
	defer connection_pool.releaseConnection(newConnection)
}

func (connection_pool *ConnectionPool) sendLoop() {
	for push := range connection_pool.pushQueue {
		connection := connection_pool.getConnection()
		go connection_pool.sendPush(push, connection)
	}
}

func (connection_pool *ConnectionPool) sendPush(push *PushNotification, connection *Connection) {
	err := connection.Send(push)
	if err != nil {
		connection.Disconnect()
		connection_pool.acquireNewConnection()
		connection_pool.pushQueue <- push
		return
	}
	connection_pool.releaseConnection(connection)
}

func (connection_pool *ConnectionPool) Close() {
	close(connection_pool.pushQueue)
	time.Sleep(1 * time.Second)
	for connection := range connection_pool.connections {
		connection.Disconnect()
	}
	close(connection_pool.connections)
}




