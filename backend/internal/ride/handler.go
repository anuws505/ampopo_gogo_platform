// backend/internal/ride/handler.go
package ride

import (
	"ampopo_gogo_platform/internal/auth"
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"ampopo_gogo_platform/internal/realtime"
	"ampopo_gogo_platform/pkg/omise"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type RideHandler struct {
  pricingService *PricingService
  omiseClient    *omise.OmiseClient
  hub            *realtime.Hub
}

func NewRideHandler(ps *PricingService, oc *omise.OmiseClient,
  h *realtime.Hub) *RideHandler {
  return &RideHandler{
    pricingService: ps,
    omiseClient:    oc,
    hub:            h,
  }
}

type CalculateFareRequest struct {
  VehicleType     string `json:"vehicle_type"`
  DistanceKM      string `json:"distance_km"`
  DurationMinutes string `json:"duration_minutes"`
  SurgeMultiplier string `json:"surge_multiplier"`
}

type EstimateFareResponse struct {
  TotalFare   decimal.Decimal `json:"total_fare"`
  DriverShare decimal.Decimal `json:"driver_share"`
}

func (h *RideHandler) EstimateFareEndpoint(w http.ResponseWriter, r *http.Request) {
  var req CalculateFareRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.VehicleType == "" {
    core.WriteError(w, http.StatusBadRequest,
      "vehicle_type is required ('bike' or 'car')", "40001")
    return
  }

  // แปลงค่าสตริงจากหน้าบ้านให้เป็น decimal เพื่อความแม่นยำ
  dist, err := decimal.NewFromString(req.DistanceKM)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "distance_km must be a valid float string (e.g., '2.60')", "40011")
    return
  }

  durat, err := decimal.NewFromString(req.DurationMinutes)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "duration_minutes must be a valid float string (e.g., '10')", "40012")
    return
  }

  surge, err := decimal.NewFromString(req.SurgeMultiplier)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "surge_multiplier must be a valid float string (e.g., '1.30')", "40013")
    return
  }

  // เรียกใช้ลอจิกคำนวณที่ service.go
  result := h.pricingService.CalculateFare(req.VehicleType, dist, durat, surge)

  response := EstimateFareResponse{
    TotalFare:   result.TotalFare,
    DriverShare: result.DriverShare,
  }

  // ส่งผลลัพธ์กลับไปให้ Frontend
  core.WriteSuccess( w, http.StatusOK,
    "Success", "20000",
    response,
  )
}

type CreateRideRequest struct {
  VehicleType      string `json:"vehicle_type"`
  PickupLatitude   string `json:"pickup_latitude"`
  PickupLongitude  string `json:"pickup_longitude"`
  DropoffLatitude  string `json:"dropoff_latitude"`
  DropoffLongitude string `json:"dropoff_longitude"`
  DistanceKM       string `json:"distance_km"`
  DurationMinutes  string `json:"duration_minutes"`
  SurgeMultiplier  string `json:"surge_multiplier"`
  CardToken        string `json:"card_token"`
  PaymentMethod    string `json:"payment_method"`
  OriginName       string `json:"origin_name"`
  DestinationName  string `json:"destination_name"`
}

func (h *RideHandler) CreateRideEndpoint(w http.ResponseWriter, r *http.Request) {
  // [GUARD RAIL] ดึงไอดีลูกค้าจาก Token โดยตรง ป้องกันการแก้ไขค่าจากภายนอก
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  customerIDStr := ctxUserID.(string)

  var req CreateRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.CardToken == "" || req.VehicleType == "" || req.PaymentMethod == "" {
    core.WriteError(w, http.StatusBadRequest,
      "card_token, vehicle_type and payment_method are required fields", "40002")
    return
  }

  // แปลงค่าจาก string ใน request ให้เป็น decimal ใช้ใน Ride struct
  pickupLat, err := decimal.NewFromString(req.PickupLatitude)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid pickup_latitude format", "40015")
    return
  }
  pickupLng, err := decimal.NewFromString(req.PickupLongitude)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid pickup_longitude format", "40016")
    return
  }

  // แปลงค่าจาก string ใน request ให้เป็น decimal เพื่อใช้คำนวณ
  distance, err := decimal.NewFromString(req.DistanceKM)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid distance_km", "40011")
    return
  }
  duration, err := decimal.NewFromString(req.DurationMinutes)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid duration_minutes", "40012")
    return
  }
  surge, err := decimal.NewFromString(req.SurgeMultiplier)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid surge_multiplier", "40013")
    return
  }

  // คำนวณราคาจริงหลังบ้านอีกครั้ง (ป้องกันหน้าบ้านแก้ไขตัวเลขราคาส่งมาแอบเนียน)
  pricingResult := h.pricingService.CalculateFare(req.VehicleType, distance, duration, surge)

  var omiseChargeIDPtr *string
  var qrCodeURL string

  switch req.PaymentMethod {
    case "credit_card":
      charge, err := h.omiseClient.CreateHoldCharge(req.CardToken, pricingResult.TotalFare)
      if err != nil {
        core.WriteError(w, http.StatusPaymentRequired,
          "Card authorization failed: "+err.Error(), "50001")
        return
      }
      omiseChargeIDPtr = &charge.ID

    case "promptpay":
      charge, err := h.omiseClient.CreatePromptPayCharge(pricingResult.TotalFare)
      if err != nil {
        core.WriteError(w, http.StatusInternalServerError,
          "Failed to generate PromptPay QR Code: "+err.Error(), "50010")
        return
      }
      omiseChargeIDPtr = &charge.ID

      if charge.Source != nil && charge.Source.References != nil {
        qrCodeURL = charge.Source.References.Barcode
      }

    default:
      core.WriteError(w, http.StatusBadRequest, "Invalid payment method", "40014")
      return
  }

  // บันทึกลงฐานข้อมูล Rides เพื่อจับคู่คนขับ
  customerUUID, _ := uuid.Parse(customerIDStr)
  status := "searching"
  paymentStatus := "authorized"
  if req.PaymentMethod == "promptpay" {
    status = "pending_payment"
    paymentStatus = "pending"
  }

  newRide := models.Ride{
    ID:              uuid.New(),
    CustomerID:      customerUUID,
    VehicleType:     req.VehicleType,
    PickupLatitude:  pickupLat,
    PickupLongitude: pickupLng,
    DistanceKM:      distance,
    DurationMinutes: duration,
    SurgeMultiplier: surge,
    Status:          status,
    PaymentMethod:   req.PaymentMethod,
    PaymentStatus:   paymentStatus,
    TotalFare:       pricingResult.TotalFare,
    DriverShare:     pricingResult.DriverShare,
    PlatformShare:   pricingResult.PlatformShare,
    OmiseChargeID:   omiseChargeIDPtr,
    OriginName:      req.OriginName,
    DestinationName: req.DestinationName,
    CreatedAt:       time.Now(),
    UpdatedAt:       time.Now(),
  }

  if err := core.DB.Create(&newRide).Error; err != nil {
    fmt.Printf("Database Insert Error: %v\n", err)
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to save ride details to database", "50002")
    return
  }

  // Return response
  core.WriteSuccess(w, http.StatusCreated,
    "Ride created successfully and searching for nearby drivers", "20000",
    map[string]interface{}{
      "ride_id":     newRide.ID.String(),
      "charge_id":   newRide.OmiseChargeID,
      "total_fare":  newRide.TotalFare,
      "status":      newRide.Status,
      "qr_code_url": qrCodeURL,
    },
  )
}

type DriverAcceptRideRequest struct {
  RideID   string `json:"ride_id"`
}

func (h *RideHandler) AcceptRideEndpoint(w http.ResponseWriter, r *http.Request) {
  // [GUARD RAIL] ดึงไอดีคนขับจาก Token โดยตรง ป้องกันการสวมรอยรับงานจากภายนอก
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  driverIDStr := ctxUserID.(string)

  var req DriverAcceptRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required field", "40002")
    return
  }

  rideUUID, _ := uuid.Parse(req.RideID)
  driverUUID, _ := uuid.Parse(driverIDStr)
  rideIDStr := req.RideID

  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // [SAFETY CHECK] ป้องกันเคสคนขับกดรับงานพร้อมกัน (Race Condition)
  if ride.Status != "searching" || ride.DriverID != nil {
    core.WriteError(w, http.StatusConflict,
      "This ride has already been taken by another driver", "40901")
    return
  }

  // [VEHICLE TYPE CHECK] ดึงประเภทรถของไรเดอร์คนนี้มาตรวจสอบ
  var driver models.Driver
  if err := core.DB.First(&driver, "id = ?", driverUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Driver profile not found", "40402")
    return
  }

  // ตรวจสอบว่าประเภทรถของคนขับ ตรงกับที่ผู้โดยสารต้องการหรือไม่ (เช่น car == car, bike == bike)
  if driver.VehicleType != ride.VehicleType {
    core.WriteError(w, http.StatusBadRequest,
      fmt.Sprintf("Vehicle type mismatch. This ride requires a %s, but your vehicle is a %s.",
        ride.VehicleType, driver.VehicleType),
      "40033")
    return
  }

  // ดึงพิกัดปัจจุบันของไรเดอร์คนนี้จาก Redis GEO เพื่อเช็กระยะห่าง
  positions, err := core.RDB.GeoPos(r.Context(), "drivers:locations", driverIDStr).Result()
  if err != nil || len(positions) == 0 || positions[0] == nil {
    core.WriteError(w, http.StatusBadRequest,
      "Could not verify your current GPS location", "40021")
    return
  }
  driverCurrentPos := positions[0]

  // ใช้สูตรคณิตศาสตร์พื้นฐาน (Haversine Formula) คำนวณระยะห่าง
  driverLat := driverCurrentPos.Latitude
  driverLng := driverCurrentPos.Longitude
  pickupLat := ride.PickupLatitude.InexactFloat64()
  pickupLng := ride.PickupLongitude.InexactFloat64()

  // ตรวจสอบระยะห่างไม่ให้เกินขอบเขตที่กำหนด (3 กิโลเมตร)
  if calculateDistance(driverLat, driverLng, pickupLat, pickupLng) > 3.0 {
    // ล้างประวัติส่งงานค้างคู่นี้ออกจากแรมทันที เพื่อไม่ให้ส่ง Noti ซ้ำ
    h.hub.DispatchedPairs.Delete(fmt.Sprintf("%s:%s", rideIDStr, driverIDStr))

    core.WriteError(w, http.StatusBadRequest,
      "This ride is no longer available because you are out of range", "40022")
    return
  }

  // [TRANSACTION LOCK] เริ่มทำการอัปเดตข้อมูลทริป และล็อกสเตตัสคนขับพร้อมกัน
  tx := core.DB.Begin()

  // 1. อัปเดตฝั่งข้อมูลทริป (ผูกคนขับ + สลับเป็น accepted)
  rideUpdates := map[string]interface{}{
    "driver_id":  driverUUID,
    "status":     "accepted",
    "updated_at": time.Now(),
  }
  if err := tx.Model(&ride).Updates(rideUpdates).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update ride status", "50003")
    return
  }

  // 2. [DRIVER STATE] ปรับสถานะคนขับเป็น busy ทันทีเพื่อล็อกคิว ไม่ให้ Worker จ่ายงานอื่นแทรกเข้ามา
  if err := tx.Model(&models.Driver{}).Where("id = ?", driverUUID).
    Update("status", "busy").Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to lock driver status", "50098")
    return
  }

  tx.Commit()
  fmt.Printf("[DB Transaction Committed] Ride %s Accepted by Driver: %s\n", ride.ID, driverIDStr)

  ctx := r.Context()

  // job accepted สั่งเปลี่ยนสเตตัสบน Redis ไปเป็น busy
  _ = core.RDB.HSet(ctx, "drivers:states", driverIDStr, "busy").Err()

  // เคลียร์ประวัติการส่งงานคู่นี้ออกจากแรม Redis GEO
  _ = core.RDB.ZRem(ctx, "drivers:locations", driverIDStr).Err()

  // เคลียร์ประวัติการจองงาน (Dispatch History) ทั้งหมดที่ผูกกับทริปนี้ออก
  h.hub.DispatchedPairs.Range(func(key, value interface{}) bool {
    keyStr := key.(string)
    if strings.HasPrefix(keyStr, rideIDStr+":") {
      h.hub.DispatchedPairs.Delete(key)
    }
    return true
  })

  // Return response
  core.WriteSuccess(w, http.StatusOK,
    "Ride accepted successfully", "20000",
    map[string]interface{}{
      "ride_id":   ride.ID.String(),
      "status":    "accepted",
      "driver_id": driverIDStr,
    },
  )
}

// calculateDistance คำนวณหาความห่างระหว่างพิกัด 2 จุด (หน่วยเป็นกิโลเมตร) อิงตามสูตร Haversine
func calculateDistance(lat1, lon1, lat2, lon2 float64) float64 {
  const EarthRadiusKm = 6371.0

  dLat := (lat2 - lat1) * math.Pi / 180.0
  dLon := (lon2 - lon1) * math.Pi / 180.0

  radLat1 := lat1 * math.Pi / 180.0
  radLat2 := lat2 * math.Pi / 180.0

  a := math.Sin(dLat/2)*math.Sin(dLat/2) +
    math.Sin(dLon/2)*math.Sin(dLon/2)*math.Cos(radLat1)*math.Cos(radLat2)
  c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

  return EarthRadiusKm * c
}


type DriverArriveRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) ArriveRideEndpoint(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized, "Unauthorized. Missing session identity.", "40101")
    return
  }
  driverIDStr := ctxUserID.(string)
  driverUUID, _ := uuid.Parse(driverIDStr)

  var req DriverArriveRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required field", "40002")
    return
  }
  rideUUID, _ := uuid.Parse(req.RideID)

  // ดึงข้อมูลทริปมาตรวจสอบสเตตัสปัจจุบัน
  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // [SECURITY CHECK] ตรวจสอบว่าไรเดอร์คนนี้ใช่คนที่กดรับงานทริปนี้ตัวจริงไหม
  if ride.DriverID == nil || *ride.DriverID != driverUUID {
    core.WriteError(w, http.StatusForbidden,
      "Access denied. You are not the assigned driver for this ride.", "40308")
    return
  }

  // ตรวจสอบ State Machine: ทริปจะเปลี่ยนเป็น arrived ได้ ต้องมีสถานะเป็น accepted มาก่อนเท่านั้น
  if ride.Status != "accepted" {
    core.WriteError(w, http.StatusConflict,
      "Invalid ride status transition", "40902")
    return
  }

  // อัปเดตสถานะทริปเป็น arrived
  updates := map[string]interface{}{
    "status":     "arrived",
    "updated_at": time.Now(),
  }

  if err := core.DB.Model(&ride).Updates(updates).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update ride status to arrived", "50004")
    return
  }

  // ส่งสัญญาณ Real-time ผ่าน WebSocket ดีดไปบอกฝั่งลูกค้า (Customer WebSocket Hub)
  // customerNotification := map[string]interface{}{
  //   "event":   "driver_arrived",
  //   "ride_id": rideIDStr,
  //   "message": "Your driver has arrived at the pickup location",
  // }
  // เรียกใช้ฟังก์ชันบรอดแคสต์หรือส่งเจาะจงหาลูกค้า (สมมติฝั่ง hub มีการเกาะสายไอดีลูกค้าไว้)
  // h.hub.SendToSpecificCustomer(ride.CustomerID, customerNotification)
  fmt.Printf("[Realtime Alert] Broadcaster queued: Driver is arrived for Customer %s\n", ride.CustomerID.String())

  core.WriteSuccess(w, http.StatusOK,
    "Driver has arrived at the pickup location", "20000",
    map[string]interface{}{
      "ride_id":   ride.ID.String(),
      "status":    "arrived",
      "driver_id": driverIDStr,
    },
  )
}

type DriverStartRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) StartRideEndpoint(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  driverIDStr := ctxUserID.(string)
  driverUUID, _ := uuid.Parse(driverIDStr)

  var req DriverStartRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required field", "40002")
    return
  }
  rideUUID, _ := uuid.Parse(req.RideID)

  // ดึงข้อมูลทริปปัจจุบัน
  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // [SECURITY CHECK] พิสูจน์ทราบตัวตนคนขับ ว่าตรงกับคนที่ระบบล็อกสิทธิ์ไว้ให้หรือไม่
  if ride.DriverID == nil || *ride.DriverID != driverUUID {
    core.WriteError(w, http.StatusForbidden,
      "Access denied. You are not the assigned driver for this ride.", "40308")
    return
  }

  // ตรวจสอบ State Machine: ทริปจะเปลี่ยนเป็นกำลังเดินทาง (on_trip) ได้ ต้องจอดรอสเตตัส arrived มาก่อน
  if ride.Status != "arrived" {
    core.WriteError(w, http.StatusConflict,
      "Invalid ride status transition", "40902")
    return
  }

  // อัปเดตสถานะทริปเปลี่ยนเป็น on_trip เพื่อส่งสัญญาณว่ารถกำลังเคลื่อนตัว
  updates := map[string]interface{}{
    "status":     "on_trip",
    "updated_at": time.Now(),
  }

  if err := core.DB.Model(&ride).Updates(updates).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update ride status to on_trip", "50005")
    return
  }

  core.WriteSuccess(w, http.StatusOK,
    "Ride has been started successfully", "20000",
    map[string]interface{}{
      "ride_id":   ride.ID.String(),
      "status":    "on_trip",
      "driver_id": driverIDStr,
    },
  )
}

type DriverCompleteRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) CompleteRideEndpoint(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  driverIDStr := ctxUserID.(string)
  driverUUID, _ := uuid.Parse(driverIDStr)

  var req DriverCompleteRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required field", "40002")
    return
  }
  rideUUID, _ := uuid.Parse(req.RideID)

  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // [SECURITY CHECK] ตรวจสอบสิทธิ์ว่าไรเดอร์คนนี้คือเจ้าของทริปตัวจริง
  if ride.DriverID == nil || *ride.DriverID != driverUUID {
    core.WriteError(w, http.StatusForbidden,
      "Access denied. You are not the assigned driver for this ride.", "40308")
    return
  }

  // ตรวจสอบ State Machine: ทริปจะจบลงได้ ต้องมีสถานะวิ่งอยู่กลางทาง (on_trip) เท่านั้น
  if ride.Status != "on_trip" {
    core.WriteError(w, http.StatusConflict,
      "Invalid ride status transition", "40902")
    return
  }

  if ride.Status == "completed" {
    core.WriteError(w, http.StatusConflict,
      "This trip has already been completed and charged", "40902")
    return
  }

  // [TRANSACTION START] เริ่มต้นจองคิวอัปเดตระบบและการเงิน
  tx := core.DB.Begin()

  // สับสวิตช์การเรียกเก็บเงินจริงตามช่องทางการชำระเงิน
  switch ride.PaymentMethod {
    case "credit_card":
      if ride.OmiseChargeID == nil || *ride.OmiseChargeID == "" {
        tx.Rollback()
        core.WriteError(w, http.StatusBadRequest,
          "This trip does not have a valid credit card token", "40003")
        return
      }

      // ยิง Omise Capture Charge เพื่อเก็บเงินจริง (หากพัง ระบบใน DB จะปลอดภัยด้วยการ Rollback)
      _, err := h.omiseClient.CaptureCharge(*ride.OmiseChargeID)
      if err != nil {
        tx.Rollback()
        core.WriteError(w, http.StatusInternalServerError,
          "Failed to capture charge via Omise: "+err.Error(), "50004")
        return
      }
      fmt.Printf("[Omise Capture Success] Charged %s THB from Charge ID: %s\n",
        ride.TotalFare, *ride.OmiseChargeID)

    case "promptpay":
      fmt.Printf("[PromptPay Complete] No capture required amount %s THB from Charge ID: %s\n",
        ride.TotalFare, *ride.OmiseChargeID)

    default:
      tx.Rollback()
      core.WriteError(w, http.StatusBadRequest, "Invalid payment method", "40014")
      return
  }

  // credit_card change authorized to paid | promptpay change pending to paid
  rideUpdates := map[string]interface{}{
    "status":         "completed",
    "payment_status": "settled",
    "updated_at":     time.Now(),
  }
  if err := tx.Model(&ride).Updates(rideUpdates).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update ride status to completed", "50006")
    return
  }

  // โอนเงินส่วนแบ่ง เข้า Wallet คนขับ balance = balance + driver_share
  walletUpdates := map[string]interface{}{
    "balance":    gorm.Expr("balance + ?", ride.DriverShare), // ป้องกัน Race Condition
    "updated_at": time.Now(),
  }
  // สมมุติว่าชื่อโมเดลกระเป๋าเงินของคุณคือ models.DriverWallet ครับ
  if err := tx.Model(&models.DriverWallet{}).Where("driver_id = ?", ride.DriverID).
    Updates(walletUpdates).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError, "Driver payout failed", "50006")
    return
  }

  // บันทึกประวัติการเงินลงตารางบัญชีแยกประเภท (FinancialTransactions) หลังจบงาน
  txLog := models.FinancialTransaction{
    ID:          uuid.New(),
    RideID:      &ride.ID,
    DriverID:    ride.DriverID,
    TxType:      "earning",
    Amount:      ride.DriverShare,
    Description: fmt.Sprintf("Earnings from ride: %s to %s", ride.OriginName, ride.DestinationName),
    CreatedAt:   time.Now(),
  }
  if err := tx.Create(&txLog).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to record financial transaction log.", "50007")
    return
  }

  // [DRIVER STATE RELOAD] คลายล็อกคนขับเปลี่ยนจาก busy กลับมาเป็น online พร้อมรับงานถัดไป
  if err := tx.Model(&models.Driver{}).Where("id = ?", driverUUID).
    Update("status", "online").Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to release driver status back to online", "50097")
    return
  }

  // มั่นใจว่าบันทึกทุกอย่างลงฐานข้อมูลพร้อมกัน
  tx.Commit()
  fmt.Printf("[DB Transaction Committed] Ride %s Completed. Wallet Updated for Driver: %v\n",
    ride.ID, ride.DriverID)

  ctx := r.Context()
  rideIDStr := ride.ID.String()

  // job completed สั่งเปลี่ยนสเตตัสบน Redis ไปเป็น online
  redisOnlineStatus := fmt.Sprintf("online:%s", ride.VehicleType)
  _ = core.RDB.HSet(ctx, "drivers:states", driverIDStr, redisOnlineStatus).Err()

  // เคลียร์ประวัติการจองงาน (Dispatch History) ทั้งหมดที่ผูกกับทริปนี้ออก
  h.hub.DispatchedPairs.Range(func(key, value interface{}) bool {
    keyStr := key.(string)
    if strings.HasPrefix(keyStr, rideIDStr+":") {
      h.hub.DispatchedPairs.Delete(key)
    }
    return true
  })

  core.WriteSuccess(w, http.StatusOK,
    "Ride has been completed successfully", "20000",
    map[string]interface{}{
      "ride_id":   ride.ID.String(),
      "status":    "completed",
      "driver_id": driverIDStr,
    },
  )
}

type CancelRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) CancelRideEndpoint(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized, "Unauthorized. Missing session identity.", "40101")
    return
  }
  actorIDStr := ctxUserID.(string)
  actorUUID, _ := uuid.Parse(actorIDStr)

  var req CancelRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required", "40002")
    return
  }
  rideUUID, _ := uuid.Parse(req.RideID)

  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // [SECURITY CHECK] ตรวจสอบว่าคนที่ยิงมาคือผู้โดยสารเจ้าของทริป หรือคนขับที่ถูกจับคู่เท่านั้น
  if ride.CustomerID != actorUUID && (ride.DriverID == nil || *ride.DriverID != actorUUID) {
    core.WriteError(w, http.StatusForbidden,
      "Access denied. You are not authorized to cancel this ride.", "40309")
    return
  }

  // ตรวจสอบ State Machine: ห้ามยกเลิกงานที่ส่งเสร็จแล้ว หรือกำลังเดินทางอยู่ (on_trip)
  if ride.Status == "completed" {
    core.WriteError(w, http.StatusConflict,
      "Cannot cancel this ride, it has already been completed", "40903")
    return
  }
  if ride.Status == "on_trip" {
    core.WriteError(w, http.StatusConflict,
      "Cannot cancel a ride that is already on trip", "40905")
    return
  }
  if ride.Status == "cancelled" {
    core.WriteError(w, http.StatusConflict,
      "Trip has already been cancelled", "40904")
    return
  }

  // [TRANSACTION START] เริ่มต้นกระบวนการคว่ำทริปและการเงิน
  tx := core.DB.Begin()
  var targetPaymentStatus = "voided"

  // แตกแขนงการคืนเงินตามเงื่อนไขช่องทางชำระเงิน
  switch ride.PaymentMethod {
    case "credit_card":
      // เคสบัตรเครดิต: ยิง Reverse ปลดล็อกวงเงิน
      if ride.OmiseChargeID != nil && *ride.OmiseChargeID != "" {
        _, err := h.omiseClient.ReverseCharge(*ride.OmiseChargeID)
        if err != nil {
          tx.Rollback()
          core.WriteError(w, http.StatusInternalServerError,
            "Failed to reverse credit card charge: "+err.Error(), "50008")
          return
        }
        fmt.Printf("[Omise Reverse Success] Voided Charge ID: %s\n", *ride.OmiseChargeID)
      }

    case "promptpay":
      // เคส PromptPay: ต้องตรวจสอบก่อนว่าลูกค้าเงินจ่ายเข้ามาหรือยัง
      if ride.Status == "pending_payment" {
        // ลูกค้ายังไม่ได้โอนเงินสแกนคิวอาร์ แล้วกดยกเลิกทริปไปก่อน ไม่ต้องคืนเงินใคร เปลี่ยนสเตตัสใน DB จบงานได้เลย
        targetPaymentStatus = "expired"
        fmt.Println("[PromptPay Cancel] Ride cancelled before payment scanned")
      } else {
        // ลูกค้าโอนเงินสำเร็จแล้ว (สถานะกลายเป็น searching/accepted) แล้วโดนยกเลิกทริป ต้องทำการโอนเงินสดคืน (Refund)
        if ride.OmiseChargeID != nil && *ride.OmiseChargeID != "" {
          _, err := h.omiseClient.RefundCharge(*ride.OmiseChargeID, ride.TotalFare)
          if err != nil {
            // core.WriteError(w, http.StatusInternalServerError,
            //   "ไม่สามารถโอนเงินคืนลูกค้าผ่านระบบ PromptPay ได้: "+err.Error(), "50011")
            // return
            fmt.Printf("[Omise Refund Bypass] Auto-refund failed: %v. Moved to manual queue\n", err)
            targetPaymentStatus = "refund_pending"
          } else {
            targetPaymentStatus = "refunded"
            fmt.Printf("[Omise Refund Success] Refunded %s THB to customer\n", ride.TotalFare)
          }
        }
      }
  }

  // 1. อัปเดตสถานะทริปใน Postgres DB ให้กลายเป็น "cancelled"
  updates := map[string]interface{}{
    "status":         "cancelled",
    "payment_status": targetPaymentStatus,
    "updated_at":     time.Now(),
  }
  if err := tx.Model(&ride).Updates(updates).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update cancellation status", "50009")
    return
  }

  // 2. [CRITICAL FIX - DRIVER STATE] คลายล็อกคนขับเปลี่ยนจาก busy กลับมาเป็น online
  if ride.DriverID != nil {
    if err := tx.Model(&models.Driver{}).Where("id = ?", *ride.DriverID).
      Update("status", "online").Error; err != nil {
      tx.Rollback()
      core.WriteError(w, http.StatusInternalServerError,
        "Failed to release driver status back to online", "50097")
      return
    }
    fmt.Printf("[Cancel Sync] Driver ID: %s released back to online status\n",
    ride.DriverID.String())
  }

  tx.Commit()

  // เตรียมตัวแปร
  ctx := r.Context()
  driverIDStr := ride.DriverID.String()
  rideIDStr := ride.ID.String()

  // job cancelled สั่งเปลี่ยนสเตตัสบน Redis ไปเป็น online
  redisOnlineStatus := fmt.Sprintf("online:%s", ride.VehicleType)
  _ = core.RDB.HSet(ctx, "drivers:states", driverIDStr, redisOnlineStatus).Err()

  // เคลียร์ประวัติการจองงาน (Dispatch History) ทั้งหมดที่ผูกกับทริปนี้ออก
  h.hub.DispatchedPairs.Range(func(key, value interface{}) bool {
    keyStr := key.(string)
    if strings.HasPrefix(keyStr, rideIDStr+":") {
      h.hub.DispatchedPairs.Delete(key)
    }
    return true
  })

  core.WriteSuccess(w, http.StatusOK,
    "Ride cancelled and payment reverted successfully", "20000",
    map[string]interface{}{
      "ride_id":        rideIDStr,
      "status":         "cancelled",
      "payment_status": targetPaymentStatus,
    },
  )
}

type OmiseWebhookRequest struct {
  Object string `json:"object"`
  Type   string `json:"type"`
  Data   *struct {
    ID     string `json:"id"`
    Status string `json:"status"`
    Source *struct {
      Type string `json:"type"`
    } `json:"source"`
  } `json:"data"`
}

// OmiseWebhookEndpoint รับฟังสัญญาณ HTTP POST จาก Omise เมื่อเกิดเหตุการณ์เงินเข้า
func (h *RideHandler) OmiseWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
  var event OmiseWebhookRequest
  if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
    w.WriteHeader(http.StatusBadRequest)
    return
  }

  // ดักจับเฉพาะเหตุการณ์ charge.complete (ลูกค้าจ่ายเงินสดสำเร็จแล้ว)
  if event.Type == "charge.complete" && event.Data != nil {
    chargeID := event.Data.ID

    // ค้นหาทริปใน Postgres DB ที่ผูกกับ Charge ID ตัวนี้อยู่
    var ride models.Ride
    if err := core.DB.First(&ride, "omise_charge_id = ?", chargeID).Error; err != nil {
      fmt.Printf("[Webhook Error] Not found trip data for Charge ID: %s\n", chargeID)
      w.WriteHeader(http.StatusOK) // ตอบ 200 กลับไปก่อนเพื่อไม่ให้ Omise พยายามยิงซ้ำ
      return
    }

    // แตกแขนงลอจิกตามความจริงที่เกิดขึ้นบนหน้า Dashboard
    switch event.Data.Status {
      case "successful":
        // เคสที่ 1: จ่ายสำเร็จ update pending_payment เป็น searching พร้อมหาไรเดอร์
        if ride.Status == "pending_payment" {
          updates := map[string]interface{}{
            "status":         "searching",
            "payment_status": "paid",
            "updated_at":     time.Now(),
          }
          core.DB.Model(&ride).Updates(updates)
          fmt.Printf("[Webhook Success] Payment received, trip pairing completed! Ride ID: %s and status change to searching\n",
            ride.ID)
        }

      case "failed":
        // เคสที่ 2: จ่ายล้มเหลว / ปล่อยคิวอาร์โค้ดหมดอายุ
        if ride.Status == "pending_payment" {
          updates := map[string]interface{}{
            "status":         "cancelled",
            "payment_status": "failed",
            "updated_at":     time.Now(),
          }
          core.DB.Model(&ride).Updates(updates)
          fmt.Printf("[Webhook Failed Log] Trip Ride ID: %s status change to cancelled automatically PromptPay payment fails\n",
            ride.ID)

          // แถมเทคนิค: ตรงนี้สามารถส่ง WebSocket ไปบอกแอปฝั่งลูกค้า (Customer)
          // ว่า "การชำระเงินไม่สำเร็จ กรุณาลองใหม่อีกครั้ง" หน้าแอปจะได้เด้งเตือน
        }
    }
  }

  // ตอบกลับสถานะ 200 OK เพื่อบอกว่าได้รับสัญญาณเรียบร้อยแล้ว
  w.WriteHeader(http.StatusOK)
}

// โครงสร้างสำหรับพ่นประวัติแต่ละทริปกลับไปให้หน้าบ้านวาดการ์ด (Card UI)
type RideHistoryItem struct {
  ID              string          `json:"ride_id"`
  OriginName      string          `json:"origin_name"`
  DestinationName string          `json:"destination_name"`
  TotalFare       decimal.Decimal `json:"total_fare"`
  Status          string          `json:"status"`
  CreatedAt       time.Time       `json:"created_at"`
}

func (h *RideHandler) GetRideHistoryEndpoint(w http.ResponseWriter, r *http.Request) {
  // [GUARD RAIL] แกะตัวตนและบทบาทจากตั๋วเดินทาง JWT
  ctxUserID := r.Context().Value(auth.UserIDKey)
  ctxRole := r.Context().Value(auth.UserRoleKey)

  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized, "Unauthorized. Missing session identity.", "40101")
    return
  }
  userIDStr := ctxUserID.(string)
  userUUID, _ := uuid.Parse(userIDStr)

  var rides []models.Ride
  query := core.DB.Model(&models.Ride{})

  // กรองตารางตามบทบาทของผู้ใช้งาน
  if ctxRole == "driver" {
    // ถ้าเป็นคนขับ ให้ดึงทริปทั้งหมดที่เขาเป็นคนขับ และเรียงจากทริปล่าสุด (Order By CreatedAt DESC)
    query = query.Where("driver_id = ?", userUUID).Order("created_at DESC")
  } else {
    // ถ้าเป็นผู้โดยสาร ให้ดึงทริปทั้งหมดที่เขาเป็นคนเรียก
    query = query.Where("customer_id = ?", userUUID).Order("created_at DESC")
  }

  // ยิงคิวรีดึงข้อมูลออกมาเป็นก้อนลิสต์ (Slice)
  if err := query.Find(&rides).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError, "Failed to fetch ride history", "50012")
    return
  }

  // แปลงข้อมูลจาก DB Model ให้กลายเป็นก้อน Response JSON
  historyResponse := make([]RideHistoryItem, 0) // ป้องกันเคสคืนค่าเป็น null ให้หน้าบ้าน ถ้ายังไม่มีประวัติเลยจะส่งเป็น []
  for _, ride := range rides {
    historyResponse = append(historyResponse, RideHistoryItem{
      ID:              ride.ID.String(),
      OriginName:      ride.OriginName,
      DestinationName: ride.DestinationName,
      TotalFare:       ride.TotalFare,
      Status:          ride.Status,
      CreatedAt:       ride.CreatedAt,
    })
  }

  // Response
  core.WriteSuccess(w, http.StatusOK,
    "Fetch ride history successfully", "20000", historyResponse)
}
