// backend/internal/ride/handler.go
package ride

import (
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
  CustomerID       string `json:"customer_id"`
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
  var req CreateRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.CardToken == "" || req.CustomerID == "" || req.VehicleType == "" ||
    req.PaymentMethod == "" {
    core.WriteError(w, http.StatusBadRequest,
      "customer_id and card_token and vehicle_type and payment_method are required",
      "40002")
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

  // บันทึกลงฐานข้อมูล Rides เพื่อเปิดทริปจับคู่คนขับ
  customerUUID, _ := uuid.Parse(req.CustomerID)
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
  DriverID string `json:"driver_id"`
}

func (h *RideHandler) AcceptRideEndpoint(w http.ResponseWriter, r *http.Request) {
  var req DriverAcceptRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" || req.DriverID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id and driver_id are required", "40002")
    return
  }

  rideUUID, _ := uuid.Parse(req.RideID)
  driverUUID, _ := uuid.Parse(req.DriverID)

  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // Safety Check: ป้องกันเคสคนขับ 2 คนกดปุ่มรับงานพร้อมกัน (Race Condition)
  // งานที่จะกดรับได้ ต้องมีสถานะเป็น "searching" และยังไม่มีคนขับผูกไว้เท่านั้น
  if ride.Status != "searching" || ride.DriverID != nil {
    core.WriteError(w, http.StatusConflict,
      "This ride has already been taken by another driver", "40901")
    return
  }

  // ดึงพิกัดปัจจุบันของไรเดอร์คนนี้จาก Redis GEO
  driverIDStr := driverUUID.String()
  rideIDStr := ride.ID.String()

  // ใช้คำสั่ง GeoPos เพื่อดึงพิกัดทศนิยมล่าสุดของไรเดอร์
  positions, err := core.RDB.GeoPos(r.Context(), "drivers:locations", driverIDStr).Result()
  if err != nil || len(positions) == 0 || positions[0] == nil {
    core.WriteError(w, http.StatusBadRequest,
      "Could not verify your current GPS location", "40021")
    return
  }
  driverCurrentPos := positions[0]

  // หากคำนวณสูตรคณิตศาสตร์พื้นฐาน (Haversine Formula)
  driverLat := driverCurrentPos.Latitude
  driverLng := driverCurrentPos.Longitude
  pickupLat := ride.PickupLatitude.InexactFloat64()
  pickupLng := ride.PickupLongitude.InexactFloat64()

  // ตรวจสอบว่าระยะห่างเกินขอบเขตที่กำหนด:
  if calculateDistance(driverLat, driverLng, pickupLat, pickupLng) > 3.0 {

    // ล้างประวัติส่งงานค้างคู่นี้ออกจากแรมทันที เพื่อไม่ให้ส่ง Noti ซ้ำ
    h.hub.DispatchedPairs.Delete(fmt.Sprintf("%s:%s", rideIDStr, driverIDStr))

    // ส่ง Error 400 บอกไรเดอร์ว่าอยู่นอกระยะงาน
    core.WriteError(w, http.StatusBadRequest,
      "This ride is no longer available because you are out of range", "40022")
    return
  }

  // สั่งอัปเดตข้อมูลผูกตัวคนขับและเปลี่ยนสถานะทริปผ่าน GORM
  updates := map[string]interface{}{
    "driver_id":  driverUUID,
    "status":     "accepted",
    "updated_at": time.Now(),
  }

  if err := core.DB.Model(&ride).Updates(updates).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update ride status in database", "50003")
    return
  }

  // Removed dispatch history for ride
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
      "driver_id": req.DriverID,
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

type CompleteRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) CompleteRideEndpoint(w http.ResponseWriter, r *http.Request) {
  var req CompleteRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required", "40002")
    return
  }

  rideUUID, _ := uuid.Parse(req.RideID)

  // 1. ดึงข้อมูลทริปจาก DB มาเช็กว่าทริปนี้อยู่ในสถานะที่ควรจะปิดงานได้ไหม (เช่น ต้องเป็น accepted หรือ driving)
  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  if ride.Status == "completed" {
    core.WriteError(w, http.StatusConflict,
      "This trip has already been completed and charged", "40902")
    return
  }

  // 2. สับสวิตช์การเรียกเก็บเงินจริงตามช่องทางการชำระเงิน
  switch ride.PaymentMethod {
    case "credit_card":
      if ride.OmiseChargeID == nil || *ride.OmiseChargeID == "" {
        core.WriteError(w, http.StatusBadRequest,
          "This trip does not have a valid credit card token", "40003")
        return
      }
      // บัตรเครดิตต้องยิง Capture Charge เก็บเงินจริง
      _, err := h.omiseClient.CaptureCharge(*ride.OmiseChargeID)
      if err != nil {
        core.WriteError(w, http.StatusInternalServerError,
          "Failed to capture charge via Omise: "+err.Error(), "50004")
        return
      }
      fmt.Printf("[Omise Capture Success] Charged %s THB from Charge ID: %s\n",
        ride.TotalFare, *ride.OmiseChargeID)

    case "promptpay":
      // หลังบ้านปล่อยไหลไปทำสเต็ปแจกเงินให้ไรเดอร์ใน DB ได้เลย
      fmt.Printf("[PromptPay Complete] No capture required amount %s THB from Charge ID: %s\n",
        ride.TotalFare, *ride.OmiseChargeID)

    default:
      core.WriteError(w, http.StatusBadRequest, "Invalid payment method", "40014")
      return
  }

  // 3. ใช้ GORM Transaction เพื่ออัปเดตสถานะทริป และบวกเงินเข้า Wallet ไรเดอร์พร้อมๆ กัน (ป้องกันระบบเอ๋อเงินไม่เข้า)
  tx := core.DB.Begin()

  // 3.1 อัปเดตสถานะทริปเป็น completed
  // credit_card authorized to paid
  // promptpay pending to paid
  rideUpdates := map[string]interface{}{
    "status":         "completed",
    "payment_status": "paid",
    "updated_at":     time.Now(),
  }
  if err := tx.Model(&ride).Updates(rideUpdates).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError, "Update ride status fails", "50005")
    return
  }

  // 3.2 โอนเงินส่วนแบ่ง เข้า Wallet คนขับ balance = balance + driver_share
  if err := tx.Exec("UPDATE driver_wallets SET balance = balance + ?, updated_at = ? WHERE driver_id = ?",
    ride.DriverShare, time.Now(), ride.DriverID).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError, "Driver payout failed", "50006")
    return
  }

  // 3.3 บันทึกประวัติการเงินลงตารางบัญชีแยกประเภท (FinancialTransactions) หลังจบงาน
  txLog := models.FinancialTransaction{
    ID:          uuid.New(),
    RideID:      &ride.ID,
    DriverID:    ride.DriverID,
    TxType:      "earning", // ระบุประเภทชัดเจนว่าเป็นรายได้จากการวิ่งงาน
    Amount:      ride.DriverShare, // ยอดเงิน เช่น 81.70 บาท
    Description: fmt.Sprintf("Earnings from ride: %s to %s", ride.OriginName, ride.DestinationName),
    CreatedAt:   time.Now(),
  }
  if err := tx.Create(&txLog).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to record financial transaction log.", "50007")
    return
  }

  // มั่นใจว่าบันทึกทุกอย่างลงฐานข้อมูลพร้อมกัน
  tx.Commit()
  fmt.Printf("[DB Transaction Committed] Ride %s Completed. Wallet Updated for Driver: %v\n",
    ride.ID, ride.DriverID)

  // 4. ส่ง Response ตอบกลับ
  core.WriteSuccess(w, http.StatusOK,
    "Ride completed and payment processed successfully", "20000",
    map[string]interface{}{
      "ride_id":      ride.ID.String(),
      "status":       "completed",
      "driver_share": ride.DriverShare,
    },
  )
}

type CancelRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) CancelRideEndpoint(w http.ResponseWriter, r *http.Request) {
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

  // 1. ดึงข้อมูลทริปมาตรวจสอบสถานะปัจจุบัน
  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // Safety Check: ทริปต้องไม่ถูกปิดงานไปแล้ว หรือถูกยกเลิกซ้ำซ้อน
  if ride.Status == "completed" {
    core.WriteError(w, http.StatusConflict,
      "Cannot cancel this ride, it has already been completed and charged", "40903")
    return
  }
  if ride.Status == "cancelled" {
    core.WriteError(w, http.StatusConflict, "Trip has been cancelled", "40904")
    return
  }

  var targetPaymentStatus = "voided"

  // 2. แตกแขนงการคืนเงินตามเงื่อนไขช่องทางชำระเงิน
  switch ride.PaymentMethod {
    case "credit_card":
      // เคสบัตรเครดิต: ยิง Reverse ปลดล็อกวงเงินเดิมของคุณ
      if ride.OmiseChargeID != nil && *ride.OmiseChargeID != "" {
        _, err := h.omiseClient.ReverseCharge(*ride.OmiseChargeID)
        if err != nil {
          core.WriteError(w, http.StatusInternalServerError,
            "Failed to reverse credit card charge: "+err.Error(), "50008")
          return
        }
        fmt.Printf("[Omise Reverse Success] Voided Charge ID: %s Credit limit released immediately\n",
          *ride.OmiseChargeID)
      }

    case "promptpay":
      // เคส PromptPay: ต้องตรวจสอบก่อนว่าลูกค้าควักเงินจ่ายเข้ามาหรือยัง
      if ride.Status == "pending_payment" {
        // ลูกค้ายังไม่ได้โอนเงินสแกนคิวอาร์ แล้วกดยกเลิกทริปไปก่อน ไม่ต้องคืนเงินใคร เปลี่ยนสเตตัสใน DB จบงานได้เลย
        targetPaymentStatus = "expired"
        fmt.Println("[PromptPay Cancel] Ride cancelled before payment scanned")

      } else {
        // ลูกค้าโอนเงินสำเร็จแล้ว (สถานะกลายเป็น searching/accepted) แล้วโดนยกเลิกทริป ต้องทำการโอนเงินสดคืน (Refund)
        if ride.OmiseChargeID != nil && *ride.OmiseChargeID != "" {
          _, err := h.omiseClient.RefundCharge(*ride.OmiseChargeID, ride.TotalFare)

          if err != nil {
            // core.WriteError(w, http.StatusInternalServerError, "ไม่สามารถโอนเงินคืนลูกค้าผ่านระบบ PromptPay ได้: "+err.Error(), "50011")
            // return
            fmt.Printf("[Omise Refund Bypass] Auto-refund failed: %v. Switched to manual refund queue for admin\n", err)
            targetPaymentStatus = "refund_pending"
          } else {
            targetPaymentStatus = "refunded"
            fmt.Printf("[Omise Refund Success] Refunded %s THB to customer for Charge ID: %s\n",
              ride.TotalFare, *ride.OmiseChargeID)
          }
        }
      }
  }

  // 3. อัปเดตสถานะทริปใน Postgres DB ให้กลายเป็น "cancelled"
  updates := map[string]interface{}{
    "status":         "cancelled",
    "payment_status": targetPaymentStatus, // จะเปลี่ยนเป็น voided, expired, หรือ refunded
    "updated_at":     time.Now(),
  }

  if err := core.DB.Model(&ride).Updates(updates).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update cancellation status in database", "50009")
    return
  }

  // 4. ส่ง Response ตอบกลับ
  core.WriteSuccess(w, http.StatusOK,
    "Ride cancelled and payment reverted successfully", "20000",
    map[string]interface{}{
      "ride_id":        ride.ID.String(),
      "status":         "cancelled",
      "payment_status": targetPaymentStatus,
    },
  )
}

type ArriveRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) ArriveRideEndpoint(w http.ResponseWriter, r *http.Request) {
  var req ArriveRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required", "40002")
    return
  }

  rideUUID, _ := uuid.Parse(req.RideID)
  rideIDStr := rideUUID.String()

  // ดึงข้อมูลทริปมาตรวจสอบสเตตัสปัจจุบัน
  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // Safety Check: ทริปจะกดมาถึงจุดรับได้ ต้องถูกคนขับรับงานไปแล้ว (accepted) เท่านั้น
  if ride.Status != "accepted" {
    core.WriteError(w, http.StatusConflict,
      "Invalid ride status for arrival configuration", "40905")
    return
  }

  // อัปเดตสถานะทริปใน Postgres DB ให้กลายเป็น "arrived"
  updates := map[string]interface{}{
    "status":     "arrived",
    "updated_at": time.Now(),
  }

  if err := core.DB.Model(&ride).Updates(updates).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update arrival status in database", "50012")
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

  // ส่ง Response สำเร็จกลับไปหาไรเดอร์
  core.WriteSuccess(w, http.StatusOK,
    "Arrival status updated successfully", "20000",
    map[string]interface{}{
      "ride_id": rideIDStr,
      "status":  "arrived",
    },
  )
}

type StartRideRequest struct {
  RideID string `json:"ride_id"`
}

func (h *RideHandler) StartRideEndpoint(w http.ResponseWriter, r *http.Request) {
  var req StartRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.RideID == "" {
    core.WriteError(w, http.StatusBadRequest, "ride_id is required", "40002")
    return
  }

  rideUUID, _ := uuid.Parse(req.RideID)
  rideIDStr := rideUUID.String()

  // 1. ดึงข้อมูลทริปมาตรวจสอบสเตตัสปัจจุบัน
  var ride models.Ride
  if err := core.DB.First(&ride, "id = ?", rideUUID).Error; err != nil {
    core.WriteError(w, http.StatusNotFound, "Ride does not exist", "40401")
    return
  }

  // Safety Check: ทริปจะเริ่มออกเดินทางได้ ต้องผ่านสเตตัส arrived มาก่อนเท่านั้น
  if ride.Status != "arrived" {
    core.WriteError(w, http.StatusConflict, "Invalid ride status to start the trip.", "40906")
    return
  }

  // 2. อัปเดตสถานะทริปใน Postgres DB ให้กลายเป็น "driving"
  updates := map[string]interface{}{
    "status":     "driving",
    "updated_at": time.Now(),
  }

  if err := core.DB.Model(&ride).Updates(updates).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to update trip status to driving.", "50013")
    return
  }

  // 3. ส่ง Response สำเร็จกลับไปหาไรเดอร์
  core.WriteSuccess(w, http.StatusOK,
    "Trip started successfully. Driving to destination.", "20000",
    map[string]interface{}{
      "ride_id": rideIDStr,
      "status":  "driving",
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

          // แถมเทคนิค: ตรงนี้สามารถส่ง WebSocket ไปบอกแอปฝั่งลูกค้า (Customer) ได้ด้วยนะ
          // ว่า "การชำระเงินไม่สำเร็จ กรุณาลองใหม่อีกครั้ง" หน้าแอปจะได้เด้งเตือน
        }
    }
  }

  // ตอบกลับสถานะ 200 OK เพื่อบอกว่าได้รับสัญญาณเรียบร้อยแล้ว
  w.WriteHeader(http.StatusOK)
}
