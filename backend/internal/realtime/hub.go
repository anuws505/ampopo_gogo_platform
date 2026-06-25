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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
  CheckOrigin: func(r *http.Request) bool { return true },
}

type DriverClient struct {
  DriverID uuid.UUID
  Conn     *websocket.Conn
}

type Hub struct {
  ActiveDrivers   sync.Map
  DispatchedPairs sync.Map
}

func NewHub() *Hub {
  return &Hub{}
}

type LocationMessage struct {
  Event     string  `json:"event"`
  Latitude  float64 `json:"latitude"`
  Longitude float64 `json:"longitude"`
}

// HandleDriverConnection จัดการท่อสัญญาณและควบคุมสเตตัสบน Redis
func (h *Hub) HandleDriverConnection(w http.ResponseWriter, r *http.Request) {
  ctx := r.Context()
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

  client := &DriverClient{DriverID: driverUUID, Conn: conn}
  h.ActiveDrivers.Store(driverUUID, client)

  // [REDIS STATE] เช็กงานค้างจาก Postgres รอบเดียวตอนต่อสายเข้า
  var activeRideCount int64
  core.DB.Model(&models.Ride{}).
    Where("driver_id = ? AND status IN ?", driverUUID, []string{"accepted", "arrived", "on_trip"}).
    Count(&activeRideCount)

  // ดึงสเปกรถของไรเดอร์มาด่วน เพื่อใช้ประกอบร่างคีย์บน Redis
  var driverProfile models.Driver
  _ = core.DB.Select("vehicle_type").First(&driverProfile, "id = ?", driverUUID).Error

  if activeRideCount == 0 {
    // ซิงค์ทั้งคู่ให้ตรงกัน และ ประกอบร่างฝากลงแรมเป็น "online:bike" หรือ "online:car"
    redisStatus := fmt.Sprintf("online:%s", driverProfile.VehicleType)
  
    _ = core.RDB.HSet(ctx, "drivers:states", driverIDStr, redisStatus).Err()
    _ = core.DB.Model(&models.Driver{}).Where("id = ?", driverUUID).Update("status", "online").Error
    fmt.Printf("[Sync Connect] Driver ID: %s set to %s\n", driverIDStr, redisStatus)
  } else {
    _ = core.RDB.HSet(ctx, "drivers:states", driverIDStr, "busy").Err()
    _ = core.DB.Model(&models.Driver{}).Where("id = ?", driverUUID).Update("status", "busy").Error
    fmt.Printf("[Sync Connect] Driver ID: %s locked to busy due to active ride\n", driverIDStr)
  }

  // เปิดสายรอฟัง เผื่อคนขับส่งข้อมูลอะไรกลับมา หรือถ้าตัดสายไปจะได้เคลียร์รายชื่อออก
  defer func() {
    h.ActiveDrivers.Delete(driverUUID)
    conn.Close()

    bgCtx := context.Background()
    var liveRideCount int64

    // [CRITICAL FIX] เปลี่ยนจาก driverUUID มาใช้ driverIDStr แทนเพื่อความแม่นยำสูงสุดใน SQL
		err := core.DB.Model(&models.Ride{}).
			Where("driver_id = ? AND status IN ?", driverIDStr, []string{"accepted", "arrived", "on_trip"}).
			Count(&liveRideCount).Error
    if err != nil {
			fmt.Printf("[DB Defer Error] Failed to count live rides for driver %s: %v\n", driverIDStr, err)
			// ถ้า DB พัง ชิงเคลียร์ Redis สเตตัสพื้นฐานก่อนเพื่อความปลอดภัย
			_ = core.RDB.HDel(bgCtx, "drivers:states", driverIDStr).Err()
			_ = core.RDB.ZRem(bgCtx, "drivers:locations", driverIDStr).Err()
			return
		}

    if liveRideCount == 0 {
      _ = core.RDB.HDel(bgCtx, "drivers:states", driverIDStr).Err()
      _ = core.RDB.ZRem(bgCtx, "drivers:locations", driverIDStr).Err()
      _ = core.DB.Model(&models.Driver{}).Where("id = ?", driverIDStr).Update("status", "offline").Error
      fmt.Printf("[Sync WS Disconnected] Driver ID: %s has logged out. Status updated to offline on both systems.\n", driverIDStr)
    } else {
      fmt.Printf("[Sync WS Disconnected Warning] Driver ID: %s lost net during trip. Maintained busy state on DB.\n", driverIDStr)
    }
  }()

  for {
    // นั่งฟังสายค้างไว้ ถ้าระบบหลุดลูปนี้แปลว่าสายตัด
    _, msgBytes, err := conn.ReadMessage()
    if err != nil {
      break
    }

    var msg LocationMessage
    if err := json.Unmarshal(msgBytes, &msg); err == nil && msg.Event == "update_location" {
      // [MEMORY SPEED] อ่านสเตตัสจาก Redis
      currentStatus, err := core.RDB.HGet(ctx, "drivers:states", driverIDStr).Result()
      if err != nil || !strings.HasPrefix(currentStatus, "online") {
        continue // ถ้าเป็น busy หรือ offline จะโดนเตะออกตรงนี้ตามปกติ
      }

      fmt.Printf("[GPS Update] Driver %s (%s) at Lat: %f, Lng: %f\n", driverIDStr, currentStatus, msg.Latitude, msg.Longitude)

      // บันทึกพิกัด driver ลง redis
      _ = core.RDB.GeoAdd(ctx, "drivers:locations", &redis.GeoLocation{
        Name:      driverIDStr,
        Latitude:  msg.Latitude,
        Longitude: msg.Longitude,
      }).Err()
    }
  }
}

// FindNearbyDrivers ทำหน้าที่ยิงเรดาร์สแกนหาคนขับที่อยู่รอบตัวลูกค้าในรัศมีที่กำหนด (กิโลเมตร)
func (h *Hub) FindNearbyDrivers(ctx context.Context, lat, lng float64, radiusKm float64) ([]string, error) {
  result, err := core.RDB.GeoSearch(ctx, "drivers:locations", &redis.GeoSearchQuery{
    Longitude:  lng,
    Latitude:   lat,
    Radius:     radiusKm,
    RadiusUnit: "km",
  }).Result()
  return result, err
}

// SendToSpecificDriver ส่งข้อความเจาะจงไปที่ท่อ WebSocket ของไรเดอร์คนนั้นๆ
func (h *Hub) SendToSpecificDriver(driverID uuid.UUID, message interface{}) {
  if value, found := h.ActiveDrivers.Load(driverID); found {
    client := value.(*DriverClient)
    _ = client.Conn.WriteJSON(message)
  }
}

// StartDispatchWorker ลูปกระจายงานเวอร์ชันประหยัดทรัพยากร (Postgres-Free Filter)
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
        var pendingRides []models.Ride
        err := core.DB.Where("status = ?", "searching").Find(&pendingRides).Error
        if err != nil || len(pendingRides) == 0 {
          continue 
        }

        for _, ride := range pendingRides {
          lat := ride.PickupLatitude.InexactFloat64()
          lon := ride.PickupLongitude.InexactFloat64()

          nearbyDriverIDs, err := h.FindNearbyDrivers(ctx, lat, lon, 3.0)
          if err != nil || len(nearbyDriverIDs) == 0 {
            continue 
          }

          // [REDIS CACHE FILTER] ดึงสถานะของกลุ่มคนขับรอบพิกัดจาก Redis Hash มาตรวจพร้อมกันในคำสั่งเดียว
          statuses, err := core.RDB.HMGet(ctx, "drivers:states", nearbyDriverIDs...).Result()
          if err != nil {
            continue
          }

          var availableDriverIDs []string
          for i, statusItem := range statuses {
            if statusItem != nil {
              statusStr := statusItem.(string)
              // ประกอบร่างค่าที่คาดหวัง เช่น "online:bike" หรือ "online:car"
              expectedValue := fmt.Sprintf("online:%s", ride.VehicleType)
              
              if statusStr == expectedValue {
                availableDriverIDs = append(availableDriverIDs, nearbyDriverIDs[i])
              }
            }
          }

          if len(availableDriverIDs) == 0 {
            continue 
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

          for _, dIDStr := range availableDriverIDs {
            lockKey := fmt.Sprintf("%s:%s", ride.ID.String(), dIDStr)

            if lastSent, exists := h.DispatchedPairs.Load(lockKey); exists {
              if time.Since(lastSent.(time.Time)) < 30*time.Second {
                continue 
              }
            }

            h.DispatchedPairs.Store(lockKey, time.Now())
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

// ToggleStatusEndpoint ปรับเปลี่ยนสถานะรับงานผ่านระดับแรมอย่างรวดเร็ว
func (h *Hub) ToggleStatusEndpoint(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  driverIDStr := ctxUserID.(string)

  var req ToggleStatusRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "Invalid JSON body format", "40000")
    return
  }

  status := "offline"
  pgStatus := "offline"

  if req.IsOnline {
    var driverProfile models.Driver
    _ = core.DB.Select("vehicle_type").First(&driverProfile, "id = ?", driverIDStr).Error
    
    status = fmt.Sprintf("online:%s", driverProfile.VehicleType) // กลายเป็น "online:bike"
    pgStatus = "online"
  }

  // [REDIS UPDATE] เขียนสเตตัสลง redis
  _ = core.RDB.HSet(r.Context(), "drivers:states", driverIDStr, status).Err()
  _ = core.DB.Model(&models.Driver{}).Where("id = ?", driverIDStr).Update("status", pgStatus).Error

  if !req.IsOnline {
    _ = core.RDB.ZRem(r.Context(), "drivers:locations", driverIDStr).Err()
  }

  core.WriteSuccess(w, http.StatusOK,
    "Driver status updated successfully on cache", "20000",
    map[string]interface{}{
      "status": status,
    },
  )
}
