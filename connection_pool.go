package apns

import "crypto/tls"
import "strings"

type ConnectionPool struct {
	NumConnections int
	Gateway string

	TlsConfig *tls.Config
	
	connections chan *Connection
	pushQueue chan *PushNotification
	responseQueue chan []byte
}

func NewConnectionPool(numConnections int, gateway string, certificate tls.Certificate) *ConnectionPool {
	config := &tls.Config {
		Certificates: []tls.Certificate{certificate},
		ServerName: strings.Split(gateway, ":")[0],
	}

	connections := make(chan *Connection, numConnections)

	for i := numConnections; i >= 1; i-- {
		newConnection := NewConnection(gateway, config, 5)					
		newConnection.Connect()
		connections <- newConnection
	}
	
	return &ConnectionPool {
		NumConnections: numConnections,
		Gateway: gateway,
		TlsConfig: config,
		connections: connections,
		pushQueue: make(chan *PushNotification, numConnections * 2),
		responseQueue: make(chan []byte),
	}
}

func (connection_pool *ConnectionPool) Start() {
	go connection_pool.sendLoop()
}

func (connection_pool *ConnectionPool) SendMessage(pushNotification *PushNotification) {
	connection_pool.pushQueue <- pushNotification
}

func (connection_pool *ConnectionPool) acquireConnection() *Connection {
	connection := <- connection_pool.connections
	return connection
}

func (connection_pool *ConnectionPool) putConnection(connection *Connection) {
	connection_pool.connections <- connection
}

func (connection_pool *ConnectionPool) sendLoop() {
	for push := range connection_pool.pushQueue {
		connection := connection_pool.acquireConnection()
		connection.Send(push)
		connection_pool.putConnection(connection)
	}
}






