// backend/internal/ride/handler.go
package ride

import (
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"ampopo_gogo_platform/internal/realtime"
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
  CustomerID      string `json:"customer_id"`
  VehicleType     string `json:"vehicle_type"`
  DistanceKM      string `json:"distance_km"`
  DurationMinutes string `json:"duration_minutes"`
  SurgeMultiplier string `json:"surge_multiplier"`
  CardToken       string `json:"card_token"`
  PaymentMethod   string `json:"payment_method"`
  OriginName      string `json:"origin_name"`
  DestinationName string `json:"destination_name"`
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
        core.WriteError(w, http.StatusPaymentRequired, "ล็อกวงเงินบัตรไม่สำเร็จ: "+err.Error(), "50001")
        return
      }
      omiseChargeIDPtr = &charge.ID
      fmt.Printf("[Omise Hold Success] Charge ID: %s | Amount: %s THB\n", charge.ID, pricingResult.TotalFare)

    case "promptpay":
      charge, err := h.omiseClient.CreatePromptPayCharge(pricingResult.TotalFare)
      if err != nil {
        core.WriteError(w, http.StatusInternalServerError, "ไม่สามารถสร้าง QR Code ได้: "+err.Error(), "50010")
        return
      }
      omiseChargeIDPtr = &charge.ID
      
      if charge.Source != nil && charge.Source.References != nil {
        qrCodeURL = charge.Source.References.Barcode
      }

    default:
      core.WriteError(w, http.StatusBadRequest, "ช่องทางการชำระเงินไม่ถูกต้อง", "40014")
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
      "ไม่สามารถบันทึกข้อมูลทริปลงฐานข้อมูลได้", "50002")
    return
  }
  fmt.Printf("[DB Saved] Ride ID: %s created with status: %s\n",
    newRide.ID, newRide.Status)

  // [REAL-TIME DISPATCH] ยิงสัญญาณปลุกแอปคนขับทุกคนที่กำลัง Online!
  if req.PaymentMethod != "promptpay" {
    jobOfferMessage := map[string]interface{}{
      "event":            "new_ride_requested",
      "ride_id":          newRide.ID.String(),
      "vehicle_type":     newRide.VehicleType,
      "origin_name":      newRide.OriginName,
      "destination_name": newRide.DestinationName,
      "distance_km":      newRide.DistanceKM,
      "total_fare":       newRide.TotalFare,
    }

    // สั่งพ่นข้อมูล JSON นี้ส่งกระจายออกไปทุกสายที่เชื่อมต่ออยู่ทันที
    h.hub.BroadcastToDrivers(jobOfferMessage)
    fmt.Println("[WS Broadcast] ดีดข้อมูลงานใหม่พุ่งไปหาไรเดอร์ที่ออนไลน์อยู่เรียบร้อย")
  }

  // Return response
  core.WriteSuccess(w, http.StatusCreated,
    "สร้างทริปและล็อกวงเงินเรียบร้อย กำลังจับคู่คนขับ", "20000",
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

  // 2. สับสวิตช์การเรียกเก็บเงินจริงตามช่องทางการชำระเงิน
  switch ride.PaymentMethod {
    case "credit_card":
      if ride.OmiseChargeID == nil || *ride.OmiseChargeID == "" {
        core.WriteError(w, http.StatusBadRequest, "ทริปนี้ไม่มีรหัสการชำระเงินผ่านบัตรเครดิต", "40003")
        return
      }
      // บัตรเครดิตต้องยิง Capture Charge เก็บเงินจริง
      _, err := h.omiseClient.CaptureCharge(*ride.OmiseChargeID)
      if err != nil {
        core.WriteError(w, http.StatusInternalServerError, "ไม่สามารถเรียกเก็บเงินจาก Omise ได้: "+err.Error(), "50004")
        return
      }
      fmt.Printf("[Omise Capture Success] Charged %s THB from Charge ID: %s\n",
        ride.TotalFare, *ride.OmiseChargeID)

    case "promptpay":
      // หลังบ้านปล่อยไหลไปทำสเต็ปแจกเงินให้ไรเดอร์ใน DB ได้เลย
      fmt.Printf("[PromptPay Complete] ไม่ต้องยิง Capture เพราะเงินสดอยู่ในคลัง Omise เรียบร้อยแล้ว ยอด: %s THB\n",
        ride.TotalFare)

    default:
      core.WriteError(w, http.StatusBadRequest, "ช่องทางการชำระเงินไม่ถูกต้อง", "40014")
      return
  }

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
    Description: fmt.Sprintf("รายได้จากทริป %s -> %s", ride.OriginName, ride.DestinationName),
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
    core.WriteError(w, http.StatusNotFound, "ไม่พบข้อมูลทริปดังกล่าว", "40401")
    return
  }

  // Safety Check: ทริปต้องไม่ถูกปิดงานไปแล้ว หรือถูกยกเลิกซ้ำซ้อน
  if ride.Status == "completed" {
    core.WriteError(w, http.StatusConflict,
      "ไม่สามารถยกเลิกได้ เนื่องจากทริปนี้เดินทางสำเร็จและเก็บเงินไปแล้ว", "40903")
    return
  }
  if ride.Status == "cancelled" {
    core.WriteError(w, http.StatusConflict, "ทริปนี้ถูกยกเลิกไปก่อนหน้านี้เรียบร้อยแล้ว", "40904")
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
            "ไม่สามารถปลดล็อกวงเงินบัตรเครดิตได้: "+err.Error(), "50008")
          return
        }
        fmt.Printf("[Omise Reverse Success] Voided Charge ID: %s เรียบร้อย บัตรเครดิตลูกค้าได้วงเงินคืนทันที\n",
          *ride.OmiseChargeID)
      }

    case "promptpay":
      // เคส PromptPay: ต้องตรวจสอบก่อนว่าลูกค้าควักเงินจ่ายเข้ามาหรือยัง
      if ride.Status == "pending_payment" {
        // ลูกค้ายังไม่ได้โอนเงินสแกนคิวอาร์ แล้วกดยกเลิกทริปไปก่อน ไม่ต้องคืนเงินใคร เปลี่ยนสเตตัสใน DB จบงานได้เลย
        targetPaymentStatus = "expired"
        fmt.Println("[PromptPay Cancel] ยกเลิกงานก่อนสแกนจ่าย")

      } else {
        // ลูกค้าโอนเงินสำเร็จแล้ว (สถานะกลายเป็น searching/accepted) แล้วโดนยกเลิกทริป ต้องทำการโอนเงินสดคืน (Refund)
        if ride.OmiseChargeID != nil && *ride.OmiseChargeID != "" {
          _, err := h.omiseClient.RefundCharge(*ride.OmiseChargeID, ride.TotalFare)

          if err != nil {
            // core.WriteError(w, http.StatusInternalServerError, "ไม่สามารถโอนเงินคืนลูกค้าผ่านระบบ PromptPay ได้: "+err.Error(), "50011")
            // return
            fmt.Printf("[Omise Refund Bypass] คืนเงินออโต้ไม่สำเร็จเนื่องจาก: %v แต่ระบบจะปรับเป็นสถานะ รอแอดมินคืนเงิน เพื่อให้ทริปยกเลิกได้ปกติ\n", err)
            targetPaymentStatus = "refund_pending"
          } else {
            targetPaymentStatus = "refunded"
            fmt.Printf("[Omise Refund Success] โอนเงินคืนเข้าบัญชีลูกค้าสำเร็จยอด %s THB จาก Charge: %s\n",
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
      "ไม่สามารถอัปเดตสถานะการยกเลิกทริปลงฐานข้อมูลได้", "50009")
    return
  }

  fmt.Printf("[Ride Cancelled] Ride ID: %s สถานะเปลี่ยนเป็น cancelled\n", ride.ID)

  // 4. ส่ง Response ตอบกลับ
  core.WriteSuccess(w, http.StatusOK,
    "ยกเลิกทริปและจัดการระบบการเงินเรียบร้อย", "20000",
    map[string]interface{}{
      "ride_id":        ride.ID.String(),
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
      fmt.Printf("[Webhook Error] ไม่พบข้อมูลทริปสำหรับ Charge ID: %s\n", chargeID)
      w.WriteHeader(http.StatusOK) // ตอบ 200 กลับไปก่อนเพื่อไม่ให้ Omise พยายามยิงซ้ำ
      return
    }

    // แตกแขนงลอจิกตามความจริงที่เกิดขึ้นบนหน้า Dashboard
    switch event.Data.Status {
      case "successful":
        // เคสที่ 1: จ่ายสำเร็จ update pending_payment ดีดกลายเป็น searching พร้อมหาไรเดอร์
        if ride.Status == "pending_payment" {
          updates := map[string]interface{}{
            "status":         "searching",
            "payment_status": "paid",
            "updated_at":     time.Now(),
          }
          core.DB.Model(&ride).Updates(updates)
          fmt.Printf("[Webhook Success] เงินเข้าจับคู่ทริปสำเร็จ! Ride ID: %s เปลี่ยนสถานะเป็น searching\n", ride.ID)

          // ดีดสัญญาณหาไรเดอร์ตามปกติ
          jobOfferMessage := map[string]interface{}{
            "event":            "new_ride_requested",
            "ride_id":          ride.ID.String(),
            "vehicle_type":     ride.VehicleType,
            "origin_name":      ride.OriginName,
            "destination_name": ride.DestinationName,
            "distance_km":      ride.DistanceKM,
            "total_fare":       ride.TotalFare,
          }
          h.hub.BroadcastToDrivers(jobOfferMessage)
        }

      case "failed":
        // เคสที่ 2: จ่ายล้มเหลว / ปล่อยคิวอาร์โค้ดหมดอายุ (เพิ่มเข้าไปใหม่)
        if ride.Status == "pending_payment" {
          updates := map[string]interface{}{
            "status":         "cancelled",
            "payment_status": "failed",
            "updated_at":     time.Now(),
          }
          core.DB.Model(&ride).Updates(updates)
          fmt.Printf("[Webhook Failed Log] ทริป Ride ID: %s ถูกปรับเป็น cancelled อัตโนมัติ เนื่องจากสแกนเงินล้มเหลว\n", ride.ID)
          
          // แถมเทคนิค: ตรงนี้สามารถส่ง WebSocket ไปบอกแอปฝั่งลูกค้า (Customer) ได้ด้วยนะ
          // ว่า "การชำระเงินไม่สำเร็จ กรุณาลองใหม่อีกครั้ง" หน้าแอปจะได้เด้งเตือน
        }
    }
  }

  // ตอบกลับสถานะ 200 OK เพื่อบอก Omise ว่าหลังบ้านได้รับสัญญาณเรียบร้อยแล้ว
  w.WriteHeader(http.StatusOK)
}
