package main

import (
	"WebSocket/impl"
	"bufio"
	"encoding/json"
	"fmt"
	beego "github.com/beego/beego/v2/server/web"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/pion/logging"
	"github.com/pion/turn"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Room struct {
	ID      uint   `gorm:"primaryKey"`
	Name    string // 房间名称
	PowerId int    // 拥有什么权限才可进入
}

var (
	upgrade = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	db            *gorm.DB
	publicIp      string
	certPath      string
	serverName    string
	serverDesc    string
	serverVersion = 0.03
)

func createAuthHandler(usersMap map[string]string) turn.AuthHandler {
	return func(username string, srcAddr net.Addr) (string, bool) {
		if password, ok := usersMap[username]; ok {
			return password, true
		}
		return "", false
	}
}

func createTurnServer() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	usersMap := map[string]string{}

	usersMap["deteam"] = "hhuiopasbdhasbd"

	realm := "deteam.cn"
	udpPort := 9765

	var channelBindTimeout time.Duration

	s := turn.NewServer(&turn.ServerConfig{
		Realm:              realm,
		AuthHandler:        createAuthHandler(usersMap),
		ChannelBindTimeout: channelBindTimeout,
		ListeningPort:      udpPort,
		LoggerFactory:      logging.NewDefaultLoggerFactory(),
	})

	err := s.Start()
	if err != nil {
		fmt.Println("启动失败", err.Error())
	} else {
		fmt.Println("Turn服务器初始化成功")
	}

	<-sigs

	err = s.Close()
	if err != nil {
		fmt.Println("关闭失败", err.Error())
	}
}

func main() {
	// 读取conf文件
	dbHost, _ := beego.AppConfig.String("Mysql::dbHost")
	dbPort, _ := beego.AppConfig.String("Mysql::dbPort")
	dbUser, _ := beego.AppConfig.String("Mysql::dbUser")
	dbPassword, _ := beego.AppConfig.String("Mysql::dbPassword")
	dbName, _ := beego.AppConfig.String("Mysql::dbName")
	webPort, _ := beego.AppConfig.String("DeAudio::webPort")
	publicIp, _ = beego.AppConfig.String("DeAudio::publicIp")
	certPath, _ = beego.AppConfig.String("DeAudio::certPath")
	serverName, _ = beego.AppConfig.String("DeAudio::serverName")
	serverDesc, _ = beego.AppConfig.String("DeAudio::serverDesc")
	if dbPort == "" {
		dbPort = "3306"
	}

	g := gin.Default()
	dsn := dbUser + ":" + dbPassword + "@tcp(" + dbHost + ":" + dbPort + ")/" + dbName + "?charset=utf8"
	c, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Println("数据库连接失败", err.Error())
		return
	}
	c.AutoMigrate(&Room{})
	db = c
	g.GET("/", wsHandler)
	// Turn 服务器
	go createTurnServer()
	err = g.Run(":" + webPort)
	if err != nil {
		fmt.Println("启动失败")
	}
}

func wsHandler(c *gin.Context) {
	var (
		//websocket 长连接
		wsConn *websocket.Conn
		err    error
		conn   *impl.Connection
		data   []byte
	)

	//header中添加Upgrade:websocket
	wsConn, err = upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		fmt.Println("升级失败", err.Error())
		return
	}
	conn, err = impl.InitConnection(wsConn)
	if err != nil {
		goto ERR
	}

	for {

		data, err = conn.ReadMessage()
		if err != nil {
			goto ERR
		}
		message := StringToJson(string(data))
		switch message["type"] {
		case "ping":
			message["type"] = "pong"
			if conn.UserName == message["userName"] {
				message["userName"] = conn.UserName
				err := conn.WriteMessage([]byte(JsonToString(message)))
				if err != nil {
					fmt.Println("发送失败", err.Error())
				}
			}
			break
		case "conn":
			conn.UserName = message["userName"].(string)
			password := message["password"].(string)
			user := getCertUser(conn.UserName)
			message["serverName"] = serverName
			message["serverDesc"] = serverDesc
			message["serverVersion"] = serverVersion
			if len(user) == 0 {
				message["isLogin"] = false
				message["message"] = "用户不存在"
				message["powerId"] = "0"
				message["roomList"] = map[string]interface{}{}
			} else if isCertUserConnect(conn) {
				message["isLogin"] = false
				message["message"] = "用户已经登录"
				message["powerId"] = "0"
				message["roomList"] = map[string]interface{}{}
			} else if user["password"] == password {
				message["isLogin"] = true
				message["message"] = "登录成功"
				message["publicIp"] = publicIp
				message["powerId"] = user["powerId"]
				var roomList []Room
				db.Find(&roomList)
				fmt.Println("roomList", roomList)
				message["roomList"] = roomList
			} else {
				message["isLogin"] = false
				message["message"] = "密码错误"
				message["powerId"] = "0"
				message["roomList"] = map[string]interface{}{}
			}
			err := conn.WriteMessage([]byte(JsonToString(message)))
			if err != nil {
				log.Println("发送失败", err)
			}
			break
		case "createRoom":
			userName := conn.UserName
			user := getCertUser(userName)
			power := user["powerId"]
			if len(user) == 0 {
				message["isCreate"] = false
				message["message"] = "用户不存在"
				message["powerId"] = "0"
			} else {
				switch power {
				// 暂时定义为只有12级用户可以创建房间
				case "12":
					room := Room{Name: message["roomName"].(string)}
					db.Create(&room)
					if room.ID > 0 {
						var roomList []Room
						db.Find(&roomList)
						message["isCreate"] = true
						message["roomId"] = room.ID
						message["message"] = "创建成功"
						message["roomList"] = roomList
					} else {
						message["isCreate"] = false
						message["roomId"] = 0
						message["message"] = "创建失败"
					}
				default:
					message["isCreate"] = false
					message["message"] = "权限不足"
				}
				sendMessage(conn, message)
			}
			break
		case "deleteRoom":
			userName := conn.UserName
			roomId := message["roomName"].(string)
			user := getCertUser(userName)
			power := user["powerId"]
			if len(user) == 0 {
				message["isDelete"] = false
				message["message"] = "用户不存在"
				message["powerId"] = "0"
				message["roomList"] = []Room{}
			} else {
				switch power {
				// 暂时定义为只有12级用户可以删除房间
				case "12":
					var room Room
					db.First(&room, roomId)
					if len(getAllByRoomName(conn, roomId)) > 0 {
						message["isDelete"] = false
						message["message"] = "房间内有人"
					} else {
						if room.ID > 0 {
							db.Delete(&room)
							var roomList []Room
							db.Find(&roomList)
							message["isDelete"] = true
							message["message"] = "删除成功"
							message["roomList"] = roomList
						} else {
							message["isDelete"] = false
							message["message"] = "删除失败"
						}
					}
					break
				default:
					message["isDelete"] = false
					message["message"] = "权限不足"
					break
				}
				var roomList []Room
				db.Find(&roomList)
				message["roomList"] = roomList
				sendMessage(conn, message)
			}
			break
		case "room":
			var romData Room
			roomId := message["roomName"]
			parseUint, err := strconv.ParseUint(message["roomName"].(string), 10, 64)
			if err == nil {
				roomId = parseUint
			}
			db.First(&romData, roomId)
			if romData.ID == 0 {
				data := map[string]interface{}{
					"type":         "room",
					"roomUserList": nil,
					"isJoin":       false,
					"message":      "没有该房间",
				}
				err := conn.WriteMessage([]byte(JsonToString(data)))
				if err != nil {
					log.Println("发送失败", err)
				}
			} else {
				conn.RoomName = message["roomName"].(string)
				conn.StreamId = message["streamId"].(string)
				userList := getRoomUser(conn)
				if len(userList) > 0 {
					data := map[string]interface{}{
						"type":         "room",
						"roomUserList": userList,
						"isJoin":       true,
					}
					err := conn.WriteMessage([]byte(JsonToString(data)))
					if err != nil {
						log.Println("发送失败", err)
					}
				}
			}
			break
		case "close":
			str := map[string]interface{}{
				"type":       "close",
				"sourceName": conn.UserName,
				"streamId":   conn.StreamId,
			}
			sendRoomMessage(conn, str)
			conn.RoomName = ""
			conn.StreamId = ""
			break
		default:
			sendUser(conn, message)
			break
		}
	}
ERR:
	str := map[string]interface{}{
		"type":       "close",
		"sourceName": conn.UserName,
		"streamId":   conn.StreamId,
	}
	sendRoomMessage(conn, str)
	log.Println("断开连接：", conn.GetUserList())
	conn.Close()
}

/**
* 判断用户是否连接
 */
func isCertUserConnect(conn *impl.Connection) bool {
	connectCount := 0
	for _, item := range conn.GetUserList() {
		if item.UserName == conn.UserName {
			if connectCount == 1 {
				return true
			}
			connectCount++
		}
	}
	return false
}
func getAllByRoomName(conn *impl.Connection, roomName string) []string {
	var userList []string
	for _, item := range conn.GetUserList() {
		if item.RoomName == roomName {
			userList = append(userList, item.UserName)
		}
	}
	return userList
}
func getCertUser(userCall string) map[string]string {
	userData := make(map[string]string)
	file, err := os.Open(certPath)
	if err != nil {
		log.Println("打开文件失败", err)
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
		split := strings.Split(scanner.Text(), " ")
		if len(split) == 3 {
			if split[0] == userCall {
				userData["userName"] = split[0]
				userData["password"] = split[1]
				userData["powerId"] = split[2]
				break
			}
		}
	}
	return userData
}

func sendMessage(conn *impl.Connection, message map[string]interface{}) {
	for _, item := range conn.GetUserList() {
		err := item.WriteMessage([]byte(JsonToString(message)))
		if err != nil {
			log.Println("发送失败", err)
		}
	}
}

/**
* 发送除自己以外的所有人 - Room
 */

func sendRoomMessage(conn *impl.Connection, message map[string]interface{}) {
	for _, item := range conn.GetUserList() {
		if item.UserName != conn.UserName &&
			item.RoomName == conn.RoomName {
			err := item.WriteMessage([]byte(JsonToString(message)))
			if err != nil {
				log.Println("发送失败", err)
			}
		}
	}

}

/**
* 发送消息
 */
func sendUser(conn *impl.Connection, json map[string]interface{}) {
	if conn.UserName != json["userName"] {
		for _, item := range conn.GetUserList() {
			if item.UserName == json["userName"] &&
				item.RoomName == conn.RoomName {
				temp := json
				delete(temp, "userName")
				temp["sourceName"] = conn.UserName
				temp["streamId"] = conn.StreamId
				if err := item.WriteMessage([]byte(JsonToString(temp))); err != nil {
					log.Println("发送失败", err)
				}
			}
		}
	}
}

/**
* 获取房间用户列表
 */
func getRoomUser(conn *impl.Connection) []string {
	var userList []string
	for _, item := range conn.GetUserList() {
		if item.UserName != conn.UserName &&
			item.RoomName == conn.RoomName {
			userList = append(userList, item.UserName)
		}
	}
	return userList
}

// StringToJson 字符串转json方法
func StringToJson(str string) (res map[string]interface{}) {
	var dat map[string]interface{}
	if err := json.Unmarshal([]byte(str), &dat); err == nil {
		return dat
	}
	return nil
}

// JsonToString json转字符串方法
func JsonToString(data map[string]interface{}) (res string) {
	if result, err := json.Marshal(data); err == nil {
		return string(result)
	}
	return ""
}
