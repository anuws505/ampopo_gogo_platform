// backend/internal/realtime/hub.go
package realtime

import (
	"ampopo_gogo_platform/internal/auth"
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

// Upgrader ตัวแปลงโปรโตคอลจาก HTTP ธรรมดา ให้กลายเป็น WebSocket (ท่อสื่อสารสองทาง)
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
  DispatchedPairs sync.Map
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
func (h *Hub) HandleDriverConnection(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  driverIDStr := ctxUserID.(string)
  driverUUID, _ := uuid.Parse(driverIDStr)

  conn, err := upgrader.Upgrade(w, r, nil)
  if err != nil {
    fmt.Printf("WebSocket Upgrade Error: %v\n", err)
    return
  }

  client := &DriverClient{
    DriverID: driverUUID,
    Conn:     conn,
  }

  h.ActiveDrivers.Store(driverUUID, client)

  // [DRIVER STATE] ทันทีที่ต่อท่อสำเร็จ สั่งตั้งค่าคนขับให้เป็นสถานะออนไลน์ในฐานข้อมูล
  _ = core.DB.Model(&models.Driver{}).Where("id = ?", driverUUID).Update("status", "online").Error
  fmt.Printf("[WS Connected] Driver ID: %s status changed to online\n", driverIDStr)

  // ลูปเปิดหูรอฟัง เผื่อคนขับส่งข้อมูลอะไรกลับมา หรือถ้าตัดสายไปจะได้เคลียร์รายชื่อออก
  defer func() {
    h.ActiveDrivers.Delete(driverUUID)
    conn.Close()
    
    // [DRIVER STATE] สายหลุด/ปิดแอป สั่งเปลี่ยนเป็น offline และลบพิกัดออกจากเรดาร์ทันที
    _ = core.DB.Model(&models.Driver{}).Where("id = ?", driverUUID).Update("status", "offline").Error
    _ = core.RDB.ZRem(context.Background(), "drivers:locations", driverIDStr).Err()
    
    fmt.Printf("[WS Disconnected] Driver ID: %s status changed to offline and removed from GEO\n", driverIDStr)
  }()

  for {
    // นั่งฟังสายค้างไว้ ถ้าระบบหลุดลูปนี้แปลว่าสายตัด
    _, msgBytes, err := conn.ReadMessage()
    if err != nil {
      break
    }

    var msg LocationMessage
    if err := json.Unmarshal(msgBytes, &msg); err == nil && msg.Event == "update_location" {
      // [PROD CHECK] เช็กสเตตัสใน DB ก่อน หากคนขับกดพักงาน (offline) หรือติดทริปอื่น (busy) ไม่ต้องอัปเดตตำแหน่งลงเรดาร์สแกนงาน
      var currentStatus string
      err := core.DB.Model(&models.Driver{}).Where("id = ?", driverUUID).Pluck("status", &currentStatus).Error
      if err != nil || currentStatus != "online" {
        continue // ข้ามการบันทึกพิกัดรอบนี้ไปเลย ไม่กวนระบบเรดาร์
      }

      fmt.Printf("[GPS Update] Driver %s at Lat: %f, Lng: %f\n", driverIDStr, msg.Latitude, msg.Longitude)
      
      // ส่งพิกัดเข้า Redis GEO เฉพาะตอนที่พร้อมรับงานจริง ๆ เท่านั้น
      _ = core.RDB.GeoAdd(r.Context(), "drivers:locations", &redis.GeoLocation{
        Name:      driverIDStr,
        Latitude:  msg.Latitude,
        Longitude: msg.Longitude,
      }).Err()
    }
  }
}

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

// StartDispatchWorker smart loop คอยปลุกเรดาร์สแกนกระจายงานค้างทุกๆ 10 วินาที
func (h *Hub) StartDispatchWorker(ctx context.Context) {
  ticker := time.NewTicker(10 * time.Second)
  defer ticker.Stop()

  fmt.Println("[Background Worker] The radar broadcast automatically has been started...")

  for {
    select {
    case <-ctx.Done():
      fmt.Println("[Background Worker] The radar broadcast systems were closed")
      return
    case <-ticker.C:
      // 1: ดึงเฉพาะทริปที่ยังค้างอยู่ที่สถานะ searching
      var pendingRides []models.Ride
      err := core.DB.Where("status = ?", "searching").Find(&pendingRides).Error
      if err != nil || len(pendingRides) == 0 {
        continue // ถ้าช่วงเวลานั้นไม่มีงานค้างอยู่ ก็ข้ามรอบนี้ ไปรอรอบถัดไป
      }

      // 2: เมื่อมีงานค้าง เอาพิกัดจุดรับของงานชิ้นนั้นไปสแกนหาไรเดอร์กลุ่มล่าสุดใน Redis GEO
      for _, ride := range pendingRides {
        lat := ride.PickupLatitude.InexactFloat64()
        lon := ride.PickupLongitude.InexactFloat64()

        // สแกนหาไรเดอร์รอบตัวลูกค้าในรัศมี 3 กิโลเมตรจากแรม Redis
        nearbyDriverIDs, err := h.FindNearbyDrivers(ctx, lat, lon, 3.0)
        if err != nil || len(nearbyDriverIDs) == 0 {
          continue // ถ้ารอบนี้ยังไม่มีไรเดอร์อยู่ในรัศมีของจุดรับลูกค้า ก็ข้ามไปสแกนรอบถัดไป
        }

        // [PRODUCTION FILTER] กรองข้อมูลหาเฉพาะไรเดอร์ที่มีสเตตัส "online" เท่านั้น
        // คนที่ติดทริปส่งคนอื่นอยู่ (busy/on_trip) หรือปิดระบบไปแล้ว จะโดนคัดออกตรงนี้ทันที
        var availableDriverIDs []string
        err = core.DB.Model(&models.Driver{}).
          Where("id IN ? AND status = ?", nearbyDriverIDs, "online").
          Pluck("id", &availableDriverIDs).Error

        if err != nil || len(availableDriverIDs) == 0 {
          continue // ถ้ารอบนี้ไรเดอร์แถวนั้นติดงานกันหมด ให้รอรอบถัดไป
        }

        jobOfferMessage := map[string]interface{}{
          "event":            "new_ride_requested",
          "ride_id":          ride.ID.String(),
          "vehicle_type":     ride.VehicleType,
          "origin_name":      ride.OriginName,
          "destination_name": ride.DestinationName,
          "distance_km":      ride.DistanceKM,
          "total_fare":       ride.TotalFare,
        }

        // ส่งข้อเสนอเจาะจงหาเฉพาะคนขับกลุ่มที่ว่างอยู่จริง ๆ เท่านั้น
        for _, dIDStr := range availableDriverIDs {
          lockKey := fmt.Sprintf("%s:%s", ride.ID.String(), dIDStr)

          // เช็กว่า job and rider เจ้านี้ เพิ่งส่ง noti ไปในรอบ 30 วินาทีที่ผ่านมาหรือไม่
          if lastSent, exists := h.DispatchedPairs.Load(lockKey); exists {
            if time.Since(lastSent.(time.Time)) < 30*time.Second {
              continue // ถ้าเพิ่งส่งไปไม่ถึง 30 วิ ให้ข้ามไป ไม่ต้องพ่น Noti ซ้ำ!
            }
          }

          // ถ้ายังไม่เคยส่ง หรือส่งนานแล้ว ให้บันทึกเวลาล่าสุดไว้
          h.DispatchedPairs.Store(lockKey, time.Now())

          // สั่งส่งสัญญาญงานพุ่งตรงหาไรเดอร์
          dUUID, _ := uuid.Parse(dIDStr)
          h.SendToSpecificDriver(dUUID, jobOfferMessage)
        }
      }
    }
  }
}

type ToggleStatusRequest struct {
  IsOnline bool `json:"is_online"`
}

func (h *Hub) ToggleStatusEndpoint(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  driverIDStr := ctxUserID.(string)
  driverUUID, _ := uuid.Parse(driverIDStr)

  var req ToggleStatusRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "Invalid JSON body format", "40000")
    return
  }

  status := "offline"
  if req.IsOnline {
    status = "online"
  }

  // อัปเดตสเตตัสลงฐานข้อมูล Postgres
  if err := core.DB.Model(&models.Driver{}).Where("id = ?", driverUUID).
    Update("status", status).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update driver status", "50099")
    return
  }

  // ถ้ากดปิดระบบ (Offline) ให้สั่งลบพิกัดออกจาก Redis GEO ด้วยเพื่อไม่ให้ระบบจ่ายงานมาหา
  if !req.IsOnline {
    _ = core.RDB.ZRem(r.Context(), "drivers:locations", driverIDStr).Err()
  }

  core.WriteSuccess(w, http.StatusOK,
    "Driver status updated successfully", "20000",
    map[string]interface{}{
      "status": status,
    },
  )
}
