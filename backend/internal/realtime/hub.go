// backend/internal/realtime/hub.go
package realtime

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
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
    _, _, err := conn.ReadMessage()
    if err != nil {
      break
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
