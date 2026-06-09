// backend/internal/ride/handler.go
package ride

import (
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"ampopo_gogo_platform/pkg/omise"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type RideHandler struct {
  pricingService *PricingService
  omiseClient    *omise.OmiseClient
}

func NewRideHandler(ps *PricingService, oc *omise.OmiseClient) *RideHandler {
  return &RideHandler{
    pricingService: ps,
    omiseClient:    oc,
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
  CustomerID      string `json:"customer_id"`
  VehicleType     string `json:"vehicle_type"`
  DistanceKM      string `json:"distance_km"`
  DurationMinutes string `json:"duration_minutes"`
  SurgeMultiplier string `json:"surge_multiplier"`
  CardToken       string `json:"card_token"`
  OriginName      string `json:"origin_name"`
  DestinationName string `json:"destination_name"`
}

func (h *RideHandler) CreateRideEndpoint(w http.ResponseWriter, r *http.Request) {
  var req CreateRideRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  if req.CardToken == "" || req.CustomerID == "" || req.VehicleType == "" {
    core.WriteError(w, http.StatusBadRequest,
      "customer_id and card_token and vehicle_type are required", "40002")
    return
  }

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

  // ยิงไป Hold เงินลูกค้าไว้ก่อนเดินทาง
  charge, err := h.omiseClient.CreateHoldCharge(req.CardToken, pricingResult.TotalFare)
  if err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "ล็อกวงเงินบัตรเครดิตไม่สำเร็จ: "+err.Error(), "50001")
    return
  }

  // พิมพ์ Log ดูผลลัพธ์ที่ Omise ตอบกลับมาทางหน้าจอคอนโซลหลังบ้าน
  fmt.Printf("[Omise Hold Success] Charge ID: %s | Amount: %s THB\n",
  charge.ID, pricingResult.TotalFare)

  // บันทึกลงฐานข้อมูล Rides เพื่อเปิดทริปจับคู่คนขับ
  omiseChargeIDPtr := &charge.ID
  customerUUID, _ := uuid.Parse(req.CustomerID)

  newRide := models.Ride{
    ID:              uuid.New(),
    CustomerID:      customerUUID,
    VehicleType:     req.VehicleType,
    DistanceKM:      distance,
    DurationMinutes: duration,
    SurgeMultiplier: surge,
    Status:          "searching",
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
    core.WriteError(w, http.StatusInternalServerError, "ไม่สามารถบันทึกข้อมูลทริปลงฐานข้อมูลได้", "50002")
    return
  }
  fmt.Printf("[DB Saved] Ride ID: %s created with status: %s\n", newRide.ID, newRide.Status)

  // Return response
  core.WriteSuccess(w, http.StatusCreated,
    "สร้างทริปและล็อกวงเงินเรียบร้อย กำลังจับคู่คนขับ", "20000",
    map[string]interface{}{
      "ride_id":    newRide.ID.String(),
      "charge_id":  newRide.OmiseChargeID,
      "total_fare": newRide.TotalFare,
      "status":     newRide.Status,
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
    core.WriteError(w, http.StatusNotFound, "ไม่พบข้อมูลทริปดังกล่าวในระบบ", "40401")
    return
  }

  // Safety Check: ป้องกันเคสคนขับ 2 คนกดปุ่มรับงานพร้อมกัน (Race Condition)
  // งานที่จะกดรับได้ ต้องมีสถานะเป็น "searching" และยังไม่มีคนขับผูกไว้เท่านั้น
  if ride.Status != "searching" || ride.DriverID != nil {
    core.WriteError(w, http.StatusConflict, "งานนี้ถูกคนขับท่านอื่นรับไปเรียบร้อยแล้วครับ", "40901")
    return
  }

  // สั่งอัปเดตข้อมูลผูกตัวคนขับและเปลี่ยนสถานะทริปผ่าน GORM
  // ใช้ลอจิก Transaction หรือ Updates เจาะจงฟิลด์เพื่อความปลอดภัย
  updates := map[string]interface{}{
    "driver_id":  driverUUID,
    "status":     "accepted", // เปลี่ยนสถานะเป็น "คนรับงานแล้ว"
    "updated_at": time.Now(),
  }

  if err := core.DB.Model(&ride).Updates(updates).Error; err != nil {
    core.WriteError(w, http.StatusInternalServerError, "ไม่สามารถบันทึกการรับงานลงฐานข้อมูลได้", "50003")
    return
  }

  fmt.Printf("[Driver Accepted] Ride ID: %s is taken by Driver ID: %s\n", ride.ID, req.DriverID)

  // Return response กลับไปบอกแอปคนขับว่า "คุณกดรับงานสำเร็จแล้วนะ"
  core.WriteSuccess(w, http.StatusOK,
    "รับงานสำเร็จ เรียบร้อย", "20000",
    map[string]interface{}{
      "ride_id":   ride.ID.String(),
      "status":    "accepted",
      "driver_id": req.DriverID,
    },
  )
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
    core.WriteError(w, http.StatusNotFound, "ไม่พบข้อมูลทริปดังกล่าว", "40401")
    return
  }

  if ride.Status == "completed" {
    core.WriteError(w, http.StatusConflict,
      "ทริปนี้ถูกปิดงานและเก็บเงินไปเรียบร้อยแล้ว", "40902")
    return
  }

  // 2. สั่งถอนเงินจริงจาก Omise (Capture Charge) ด้วย Charge ID ที่เราบันทึกไว้ในตาราง
  if ride.OmiseChargeID == nil || *ride.OmiseChargeID == "" {
    core.WriteError(w, http.StatusBadRequest,
      "ทริปนี้ไม่มีรหัสการชำระเงินผ่านบัตรเครดิต", "40003")
    return
  }

  // ยิง Capture ตัวที่เราเขียนแก้ไว้รอบก่อน
  _, err := h.omiseClient.CaptureCharge(*ride.OmiseChargeID)
  if err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "ไม่สามารถเรียกเก็บเงินจาก Omise ได้: "+err.Error(), "50004")
    return
  }
  fmt.Printf("[Omise Capture Success] Charged %s THB from Charge ID: %s\n",
    ride.TotalFare, *ride.OmiseChargeID)

  // 3. ใช้ GORM Transaction เพื่ออัปเดตสถานะทริป และบวกเงินเข้า Wallet ไรเดอร์พร้อมๆ กัน (ป้องกันระบบเอ๋อเงินไม่เข้า)
  tx := core.DB.Begin()

  // 3.1 อัปเดตสถานะทริปเป็น completed
  rideUpdates := map[string]interface{}{
    "status":         "completed",
    "payment_status": "paid", // เปลี่ยนจาก pending เป็น paid
    "updated_at":     time.Now(),
  }
  if err := tx.Model(&ride).Updates(rideUpdates).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError, "อัปเดตสถานะทริปไม่สำเร็จ", "50005")
    return
  }

  // 3.2 โอนเงินส่วนแบ่ง เข้า Wallet คนขับ balance = balance + driver_share
  if err := tx.Exec("UPDATE driver_wallets SET balance = balance + ?, updated_at = ? WHERE driver_id = ?",
    ride.DriverShare, time.Now(), ride.DriverID).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "ไม่สามารถโอนส่วนแบ่งเข้ากระเป๋าคนขับได้", "50006")
    return
  }

  // 3.3 บันทึกประวัติการเงินลงตารางบัญชีแยกประเภท (FinancialTransactions) หลังจบงาน
  txLog := models.FinancialTransaction{
    ID:          uuid.New(),
    RideID:      &ride.ID,         // ผูกรหัสทริป
    DriverID:    ride.DriverID,    // ผูกรหัสคนขับ (ได้ค่ามาจากตาราง ride ที่ accept ไว้)
    TxType:      "earning",        // ระบุประเภทชัดเจนว่าเป็นรายได้จากการวิ่งงาน
    Amount:      ride.DriverShare, // ยอดเงิน 81.70 บาท
    Description: fmt.Sprintf("รายได้จากทริปสั้น %s -> %s", ride.OriginName, ride.DestinationName),
    CreatedAt:   time.Now(),
  }
  if err := tx.Create(&txLog).Error; err != nil {
    tx.Rollback()
    core.WriteError(w, http.StatusInternalServerError,
      "ไม่สามารถบันทึกประวัติธุรกรรมได้", "50007")
    return
  }

  // มั่นใจว่าบันทึกทุกอย่างลงฐานข้อมูลพร้อมกัน
  tx.Commit()
  fmt.Printf("[DB Transaction Committed] Ride %s Completed. Wallet Updated for Driver: %v\n",
    ride.ID, ride.DriverID)

  // 4. ส่ง Response ตอบกลับแอปคนขับว่า ปิดงานเสร็จสิ้น เงินเข้ากระเป๋าแล้วนะ!
  core.WriteSuccess(w, http.StatusOK,
    "ปิดทริปและชำระเงินเสร็จสิ้น", "20000",
    map[string]interface{}{
      "ride_id":      ride.ID.String(),
      "status":       "completed",
      "driver_share": ride.DriverShare,
    },
  )
}
