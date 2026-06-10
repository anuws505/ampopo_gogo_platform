// backend/internal/realtime/hub.go
package realtime

import (
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

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
  Event     string  `json:"event"`
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
      fmt.Printf("[GPS Update] Driver %s อยู่ที่ Lat: %f, Lng: %f\n", driverID, msg.Latitude, msg.Longitude)
      
      // ประกอบร่างพิกัดส่งเข้า Redis GEO
      // ใช้คำสั่ง GeoAdd โดยตั้งชื่อ Key ว่า "drivers:locations"
      err := core.RDB.GeoAdd(r.Context(), "drivers:locations", &redis.GeoLocation{
        Name:      driverID.String(),
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
/* func (h *Hub) BroadcastToDrivers(message interface{}) {
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
} */

// FindNearbyDrivers ทำหน้าที่ยิงเรดาร์สแกนหาคนขับที่อยู่รอบตัวลูกค้าในรัศมีที่กำหนด (กิโลเมตร)
func (h *Hub) FindNearbyDrivers(ctx context.Context, lat, lng float64, radiusKm float64) ([]string, error) {
  // ใช้คำสั่ง GeoSearch ของ Redis ค้นหาจากกระดาน "drivers:locations"
  result, err := core.RDB.GeoSearch(ctx, "drivers:locations", &redis.GeoSearchQuery{
    Longitude:  lng,
    Latitude:   lat,
    Radius:     radiusKm,
    RadiusUnit: "km",
  }).Result()

  if err != nil {
    return nil, err
  }

  // คืนค่ากลับไปเป็นรายชื่อ Array ของ Driver ID (String) ทั้งหมดที่อยู่ในรัศมี
  return result, nil
}

// SendToSpecificDriver ส่งข้อความเจาะจงไปที่ท่อ WebSocket ของไรเดอร์คนนั้นๆ เพียงคนเดียว
func (h *Hub) SendToSpecificDriver(driverID uuid.UUID, message interface{}) {
  if value, found := h.ActiveDrivers.Load(driverID); found {
    client := value.(*DriverClient)
    _ = client.Conn.WriteJSON(message)
  }
}

// StartDispatchWorker ลูปอัจฉริยะ คอยปลุกเรดาร์สแกนกระจายงานค้างทุกๆ 10 วินาที
func (h *Hub) StartDispatchWorker(ctx context.Context) {
  ticker := time.NewTicker(10 * time.Second)
  defer ticker.Stop()

  fmt.Println("[Background Worker] ระบบเรดาร์กระจายงานอัตโนมัติ สตาร์ทเครื่องแล้ว...")

  for {
    select {
    case <-ctx.Done():
      fmt.Println("[Background Worker] ปิดระบบเรดาร์กระจายงาน")
      return
    case <-ticker.C:
      // 1: ดึงเฉพาะทริปที่ยังค้างอยู่ที่สถานะ searching
      var pendingRides []models.Ride
      err := core.DB.Where("status = ?", "searching").Find(&pendingRides).Error
      if err != nil || len(pendingRides) == 0 {
        continue // ถ้าช่วงเวลานั้นไม่มีลูกค้าเรียกรถค้างอยู่เลย ก็ข้ามลูปนี้ไปนอนรอรอบถัดไป
      }

      // 2: เมื่อมีงานค้าง เอาพิกัดจุดรับของงานชิ้นนั้นไปสแกนหาไรเดอร์กลุ่มล่าสุดใน Redis GEO
      for _, ride := range pendingRides {
        lat := ride.PickupLatitude.InexactFloat64()
        lon := ride.PickupLongitude.InexactFloat64()

        // ยิงเรดาร์สแกนหาคนขับรอบตัวลูกค้าในรัศมี 3 กิโลเมตร 
        nearbyDriverIDs, err := h.FindNearbyDrivers(ctx, lat, lon, 3.0)
        if err != nil || len(nearbyDriverIDs) == 0 {
          continue // ถ้ารอบนี้ยังไม่มีใครขับรถเฉียดเข้ามาใกล้จุดรับลูกค้า ก็ข้ามไปสแกนทริปถัดไป
        }

        // ประกอบร่าง JSON ข้อมูลงานเตรียมพ่นออกท่อ
        jobOfferMessage := map[string]interface{}{
          "event":            "new_ride_requested",
          "ride_id":          ride.ID.String(),
          "vehicle_type":     ride.VehicleType,
          "origin_name":      ride.OriginName,
          "destination_name": ride.DestinationName,
          "distance_km":      ride.DistanceKM,
          "total_fare":       ride.TotalFare,
        }

        // ส่งซ้ำเจาะจงรายบุคคลหาคนขับกลุ่มล่าสุดที่อยู่ใกล้พิกัดลูกค้า ณ วินาทีนี้ทันที
        for _, dIDStr := range nearbyDriverIDs {
          dUUID, _ := uuid.Parse(dIDStr)
          h.SendToSpecificDriver(dUUID, jobOfferMessage)
        }
        fmt.Printf("[Worker Dispatch] ยิงเรดาร์ซ้ำสแกนเจอไรเดอร์ %d คน และส่งสัญญาณงานทริป %s ให้เรียบร้อย\n",
          len(nearbyDriverIDs), ride.ID)
      }
    }
  }
}
