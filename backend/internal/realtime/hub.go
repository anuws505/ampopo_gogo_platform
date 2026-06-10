// backend/internal/realtime/hub.go
package realtime

import (
	"ampopo_gogo_platform/internal/core"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// Upgrader ตัวแปลงโปรโตคอลจาก HTTPธรรมดา ให้กลายเป็น WebSocket (ท่อสื่อสารสองทาง)
var upgrader = websocket.Upgrader{
  CheckOrigin: func(r *http.Request) bool {
    return true // อนุญาตให้แอป Flutter ทุกเครื่องยิงเชื่อมต่อได้
  },
}

// DriverClient โครงสร้างเก็บท่อเชื่อมต่อของไรเดอร์แต่ละคน
type DriverClient struct {
  DriverID uuid.UUID
  Conn     *websocket.Conn
}

// Hub ศูนย์กระจายสัญญาณหลักประจำเซิร์ฟเวอร์
type Hub struct {
  // ใช้ sync.Map เพื่อความปลอดภัยเวลาคนขับหลายคน เชื่อมต่อ/ตัดสาย พร้อมกัน (Thread-safe)
  ActiveDrivers sync.Map 
}

func NewHub() *Hub {
  return &Hub{}
}

// LocationMessage โครงสร้างรับพิกัด GPS ย้อนศรมาจากแอปไรเดอร์ผ่านท่อ WebSocket
type LocationMessage struct {
  Event     string  `json:"event"` // e.g., "update_location"
  Latitude  float64 `json:"latitude"`
  Longitude float64 `json:"longitude"`
}

// HandleDriverConnection เอนพอยต์รับคนขับเข้ามาต่อท่อสายสัญญาณ
func (h *Hub) HandleDriverConnection(w http.ResponseWriter, r *http.Request, driverID uuid.UUID) {
  conn, err := upgrader.Upgrade(w, r, nil)
  if err != nil {
    fmt.Printf("WebSocket Upgrade Error: %v\n", err)
    return
  }

  client := &DriverClient{
    DriverID: driverID,
    Conn:     conn,
  }

  // บันทึกลงสมุดรายชื่อว่าคนขับไอดีนี้ กำลังออนไลน์อยู่
  h.ActiveDrivers.Store(driverID, client)
  fmt.Printf("[WS Connected] Driver ID: %s ต่อท่อเรียลไทม์สำเร็จ\n", driverID)

  // ลูปเปิดหูรอฟัง เผื่อคนขับส่งข้อมูลอะไรกลับมา หรือถ้าตัดสายไปจะรีบเคลียร์รายชื่อออก
  defer func() {
    h.ActiveDrivers.Delete(driverID)
    conn.Close()
    fmt.Printf("[WS Disconnected] Driver ID: %s ออกจากระบบเรียลไทม์\n", driverID)
  }()

  for {
    // นั่งฟังสายค้างไว้ ถ้าระบบหลุดลูปนี้แปลว่าสายตัด
    _, msgBytes, err := conn.ReadMessage()
    if err != nil {
      break
    }

    var msg LocationMessage
    if err := json.Unmarshal(msgBytes, &msg); err == nil && msg.Event == "update_location" {
      // เมื่อได้พิกัด ละติจูด/ลองจิจูด มาแล้ว
      fmt.Printf("[GPS Update] Driver %s อยู่ที่ Lat: %f, Lng: %f\n", driverID, msg.Latitude, msg.Longitude)
      
      // สเต็ปถัดไป: ประกอบร่างพิกัดส่งเข้า Redis GEO
      // ใช้คำสั่ง GeoAdd โดยตั้งชื่อ Key หลักของกระดานว่า "drivers:locations"
      err := core.RDB.GeoAdd(r.Context(), "drivers:locations", &redis.GeoLocation{
        Name:      driverID.String(), // ใช้ ID คนขับเป็นตัวระบุตัวตนในแผนที่
        Latitude:  msg.Latitude,
        Longitude: msg.Longitude,
      }).Err()

      if err != nil {
        fmt.Printf("Redis GEOADD Error: %v\n", err)
      } else {
        fmt.Printf("[Redis GEO Saved] อัปเดตตำแหน่งไรเดอร์ %s ลงแรมเรียบร้อย\n", driverID)
      }
    }
  }
}

// BroadcastToDrivers ส่งข้อมูลกระจายไปหาคนขับทุกคนที่กำลังออนไลน์อยู่
func (h *Hub) BroadcastToDrivers(message interface{}) {
  h.ActiveDrivers.Range(func(key, value interface{}) bool {
    client := value.(*DriverClient)

    // ส่งข้อมูลในรูปแบบ JSON ออกไปหาแอปไรเดอร์คนนั้นๆ ทันที
    err := client.Conn.WriteJSON(message)
    if err != nil {
      fmt.Printf("ส่งข้อมูลหา Driver %s ล้มเหลว: %v\n", client.DriverID, err)
      client.Conn.Close()
      h.ActiveDrivers.Delete(key)
    }
    return true
  })
}
