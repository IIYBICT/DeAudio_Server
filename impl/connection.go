package impl

import (
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"math/rand"
	"sync"
	"time"
)

type Connection struct {
	wsConn *websocket.Conn
	//读取websocket的channel
	inChan chan []byte
	//给websocket写消息的channel
	outChan   chan []byte
	closeChan chan byte
	mutex     sync.Mutex
	//closeChan 状态
	isClosed bool
	UserName string
	RoomName string
	StreamId string
	connId   string
}

var (
	// UserList 用户列表
	UserList []*Connection
	// RoomList 房间列表
	RoomList []*Connection
)

// InitConnection 初始化长连接
func InitConnection(wsConn *websocket.Conn) (conn *Connection, err error) {
	conn = &Connection{
		wsConn:    wsConn,
		inChan:    make(chan []byte, 1000),
		outChan:   make(chan []byte, 1000),
		closeChan: make(chan byte, 1),
	}
	if conn.wsConn == nil {
		return
	}
	// 给连接分配一个唯一的ID
	conn.connId = GetRandomString(10)
	UserList = append(UserList, conn)
	fmt.Println(conn)
	//启动读协程
	go conn.readLoop()
	//启动写协程
	go conn.writeLoop()
	return
}
func GetRandomString(l int) string {
	str := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := []byte(str)
	result := []byte{}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < l; i++ {
		result = append(result, bytes[r.Intn(len(bytes))])
	}
	return string(result)
}

// GetUserList 获取用户列表
func (conn *Connection) GetUserList() []*Connection {
	return UserList
}

// ReadMessage 读取websocket消息
func (conn *Connection) ReadMessage() (data []byte, err error) {
	select {
	case data = <-conn.inChan:
	case <-conn.closeChan:
		err = errors.New("connection is closed")
	}
	return
}

// WriteMessage 发送消息到websocket
func (conn *Connection) WriteMessage(data []byte) (err error) {
	select {
	case conn.outChan <- data:
	case <-conn.closeChan:
		err = errors.New("connection is closed")
	}
	return
}

// Close 关闭连接
func (conn *Connection) Close() {
	//线程安全的Close,可重入
	conn.wsConn.Close()
	for i, item := range UserList {
		if conn.connId == item.connId {
			UserList = append(UserList[:i], UserList[i+1:]...)
		}
	}
	//只执行一次
	conn.mutex.Lock()
	if !conn.isClosed {
		close(conn.closeChan)
		conn.isClosed = true
	}
	conn.mutex.Unlock()
}

// IsConnect 判断是否连接
func (conn *Connection) isConnect() bool {
	return !conn.isClosed
}

func (conn *Connection) readLoop() {
	var (
		data []byte
		err  error
	)
	for {
		if _, data, err = conn.wsConn.ReadMessage(); err != nil {
			goto ERR
		}
		//阻塞在这里,等待inChan有空闲的位置
		select {
		case conn.inChan <- data:
		case <-conn.closeChan:
			//closeChan关闭的时候
			goto ERR

		}
	}
ERR:
	conn.Close()
}

func (conn *Connection) writeLoop() {
	var (
		data []byte
		err  error
	)
	for {
		select {
		case data = <-conn.outChan:
		case <-conn.closeChan:
			goto ERR
		}
		if err = conn.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
			goto ERR
		}
	}
ERR:
	conn.Close()
}
