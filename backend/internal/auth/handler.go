// backend/internal/auth/handler.go
package auth

import (
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
  OTP_PREFIX     = "otp:"
  VERIFY_PREFIX  = "verified:"
  SESSION_PREFIX = "session:"
  OTP_TTL        = 5 * time.Minute  // รหัส OTP มีอายุขัย 5 นาที (300 วินาที)
  VERIFY_TTL     = 10 * time.Minute // ยืนยันผ่านแล้ว มีเวลาให้ดำเนินการสเต็ปถัดไป 10 นาที
  SESSION_TTL    = 720 * time.Hour  // เท่ากับอายุของตั๋ว JWT (30 วัน = 720 ชั่วโมง)
)

var secretKey = core.GetEnv("AUTH_SECRET_KEY", "a-string-secret-at-least-256-bits-long")
var jwtSecretKey = []byte(secretKey)

type AuthHandler struct{}

func NewAuthHandler() *AuthHandler {
  return &AuthHandler{}
}

// -------------------------------------
// REQUEST OTP ENDPOINT
// -------------------------------------
type OTPRequest struct {
  PhoneNumber string `json:"phone_number"`
}

func (h *AuthHandler) RequestOTPEndpoint(w http.ResponseWriter, r *http.Request) {
  var req OTPRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "Invalid JSON body format", "40000")
    return
  }

  if req.PhoneNumber == "" {
    core.WriteError(w, http.StatusBadRequest,
      "phone_number is required", "40004")
    return
  }

  ctx := r.Context()
  redisKey := OTP_PREFIX + req.PhoneNumber

  // ทำลายรหัส OTP ทันทีเมื่อตรวจสอบผ่าน (One-Time Use)
  _ = core.RDB.Del(ctx, redisKey).Err()

  // สุ่มตัวเลข OTP 6 หลักชุดใหม่
  otpCode := generateSecureOTP()

  // ฝากใน Redis พร้อมตั้งเวลาหมดอายุ
  err := core.RDB.Set(ctx, redisKey, otpCode, OTP_TTL).Err()
  if err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to store verification code", "50014")
    return
  }

  // แสดงรหัส OTP ออกคอนโซล Terminal เฉพาะ Development เท่านั้น
  fmt.Printf("[OTP Dispatched] Phone: %s | Code: %s (Expires in 5 mins)\n",
    req.PhoneNumber, otpCode)

  // คืนค่าวินาทีเต็ม (300 วินาที) ให้หน้าบ้านใช้ Countdown จะได้ตรงกับเซิร์ฟเวอร์
  expiresInSeconds := int64(OTP_TTL.Seconds())

  // Response Success
  core.WriteSuccess(w, http.StatusOK,
    "Verification code sent successfully", "20000",
    map[string]interface{}{
      "phone_number":       req.PhoneNumber,
      "expires_in_seconds": expiresInSeconds,
      "message": "Check your message for the 6-digit OTP code",
    },
  )
}

func generateSecureOTP() string {
  b := make([]byte, 4)
  _, _ = rand.Read(b)
  // ปรับสเกลให้ออกมาเป็นเลขฐานสิบช่วง 100000 - 999999
  num := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 900000
  return fmt.Sprintf("%06d", 100000+num)
}

// -------------------------------------
// VERIFY OTP ENDPOINT
// -------------------------------------
type VerifyOTPRequest struct {
  PhoneNumber string `json:"phone_number"`
  OTPCode     string `json:"otp_code"`
  Role        string `json:"role"` // 'customer' หรือ 'driver'
}

func (h *AuthHandler) VerifyOTPEndpoint(w http.ResponseWriter, r *http.Request) {
  var req VerifyOTPRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "Invalid JSON body format", "40000")
    return
  }

  if req.PhoneNumber == "" || req.OTPCode == "" || req.Role == "" {
    core.WriteError(w, http.StatusBadRequest,
      "phone_number, otp_code, and role are required", "40005")
    return
  }

  ctx := r.Context()
  redisKey := OTP_PREFIX + req.PhoneNumber

  savedOTP, err := core.RDB.Get(ctx, redisKey).Result()
  if err != nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Verification code has expired or is invalid", "40101")
    return
  }

  if savedOTP != req.OTPCode {
    core.WriteError(w, http.StatusUnauthorized,
      "Incorrect verification code", "40102")
    return
  }

  // ทำลายรหัส OTP ทันทีเมื่อตรวจสอบผ่าน (One-Time Use)
  _ = core.RDB.Del(ctx, redisKey).Err()

  // เตรียมสร้าง key ใบผ่านทางรอไว้
  verifyKey := VERIFY_PREFIX + req.PhoneNumber

  // ตรวจสอบประวัติเบอร์โทรศัพท์ในฐานข้อมูลตาม Role ที่เข้ามา
  switch req.Role {
    case "customer":
      var customer models.Customer
      err := core.DB.First(&customer, "phone_number = ?", req.PhoneNumber).Error

      // เคสที่ 1.1: เป็นคนเก่าในระบบ -> ต้องส่งคำใบ้ Masking Name กลับไปให้กรอกชื่อยืนยัน
      if err == nil && customer.IsProfileComplete {
        // ใบผ่านทางชั่วคราวให้คนเก่าไว้ในแรม 10 นาทีเท่ากัน
        _ = core.RDB.Set(ctx, verifyKey, req.Role, VERIFY_TTL).Err()

        masked := maskName(customer.FirstName)
        core.WriteSuccess(w, http.StatusOK,
          "Verification passed. Existing user confirmation required", "20001",
          map[string]interface{}{
            "is_new_user": false,
            "masked_name": masked,
          },
        )
        return
      }

    case "driver":
      var driver models.Driver
      err := core.DB.First(&driver, "phone_number = ?", req.PhoneNumber).Error

      if err == nil && driver.IsProfileComplete {
        // ใบผ่านทางชั่วคราวให้คนเก่าไว้ในแรม 10 นาทีเท่ากัน
        _ = core.RDB.Set(ctx, verifyKey, req.Role, VERIFY_TTL).Err()

        masked := maskName(driver.FirstName)
        core.WriteSuccess(w, http.StatusOK,
          "Verification passed. Existing user confirmation required", "20001",
          map[string]interface{}{
            "is_new_user": false,
            "masked_name": masked,
          },
        )
        return
      }

    default:
      core.WriteError(w, http.StatusBadRequest, "Invalid role", "40014")
      return
  }

  // เคสที่ 1.2: เป็นเบอร์ใหม่ หรือยังกรอกโปรไฟล์ไม่เสร็จ -> ปล่อยไหลไปหน้า Register กรอกชื่อใหม่
  _ = core.RDB.Set(ctx, verifyKey, req.Role, VERIFY_TTL).Err()

  core.WriteSuccess(w, http.StatusOK,
    "Verification passed. Profile registration required", "20000",
    map[string]interface{}{
      "is_new_user": true,
    },
  )
}

// maskName ฟังก์ชันสับคำย่อคำใบ้ (เช่น อนุชา -> อนุxxx, อน -> อxxx)
func maskName(name string) string {
  runes := []rune(name)
  if len(runes) >= 3 {
    return string(runes[:3]) + "xxx"
  }
  if len(runes) > 0 {
    return string(runes[:1]) + "xxx"
  }
  return "xxx"
}

// -------------------------------------
// CONFIRM OWNER ENDPOINT
// -------------------------------------
type ConfirmOwnerRequest struct {
  PhoneNumber   string `json:"phone_number"`
  ConfirmedName string `json:"confirmed_name"` // ชื่อเต็มที่ผู้ใช้กรอกมาแบบไม่เว้นวรรค
  Role          string `json:"role"`           // 'customer' หรือ 'driver'
}

func (h *AuthHandler) ConfirmOwnerEndpoint(w http.ResponseWriter, r *http.Request) {
  var req ConfirmOwnerRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "Invalid JSON body format", "40000")
    return
  }

  if req.PhoneNumber == "" || req.ConfirmedName == "" || req.Role == "" {
    core.WriteError(w, http.StatusBadRequest,
      "Missing required parameters", "40005")
    return
  }

  ctx := r.Context()
  verifyKey := VERIFY_PREFIX + req.PhoneNumber

  // [GUARD RAIL] ดักจับการแอบยิงสวมรอยตรงๆ โดยไม่ผ่านด่านตรวจ OTP
  savedRole, err := core.RDB.Get(ctx, verifyKey).Result()
  if err != nil {
    // หาไม่เจอ แปลว่าแอบยิงตรง หรือทิ้งหน้าจอป๊อปอัปถามชื่อไว้นานเกิน 10 นาทีจนสิทธิ์หมดอายุ
    core.WriteError(w, http.StatusForbidden,
      "Session has expired or is invalid. Complete OTP step first", "40301")
    return
  }

  // เช็กความปลอดภัยแถมให้อีกชั้น: บทบาท (Role) ต้องตรงกับตอนที่ Verify OTP มาตั้งแต่แรกด้วย
  if savedRole != req.Role {
    core.WriteError(w, http.StatusForbidden,
      "Token role mismatch tampering detected", "40303")
    return
  }

  var userID uuid.UUID
  cleanInput := sanitizeName(req.ConfirmedName)

  if req.Role == "customer" {
    var customer models.Customer
    if err := core.DB.First(&customer, "phone_number = ?", req.PhoneNumber).Error; err != nil {
      core.WriteError(w, http.StatusNotFound,
        "User profile not found", "40401")
      return
    }

    if sanitizeName(customer.FirstName) != cleanInput {
      core.WriteError(w, http.StatusUnauthorized,
        "Identity verification failed. Name does not match", "40103")
      return
    }
    userID = customer.ID
  } else {
    var driver models.Driver
    if err := core.DB.First(&driver, "phone_number = ?", req.PhoneNumber).Error; err != nil {
      core.WriteError(w, http.StatusNotFound,
        "Driver profile not found", "40401")
      return
    }

    if sanitizeName(driver.FirstName) != cleanInput {
      core.WriteError(w, http.StatusUnauthorized,
        "Identity verification failed. Name does not match", "40103")
      return
    }
    userID = driver.ID
  }

  // ล้างตั๋วผ่านทางใน Redis ออกเมื่อมีการยืนยันความเป็นเจ้าของบัญชีสำเร็จ
  _ = core.RDB.Del(ctx, verifyKey).Err()

  // ยืนยันตัวตนคนเก่าผ่านแล้ว -> ออก Token เข้าใช้งานระบบทันที
  tokenStr, err := h.createToken(userID, req.PhoneNumber, req.Role)
  if err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to generate security token", "50018")
    return
  }

  core.WriteSuccess(w, http.StatusOK,
    "Authentication successful", "20000",
    AuthTokenResponse{
      AccessToken: tokenStr,
      TokenType:   "Bearer",
      Role:        req.Role,
      UserID:      userID.String(),
    },
  )
}

func sanitizeName(name string) string {
  return strings.ReplaceAll(name, " ", "")
}

// -------------------------------------
// REGISTER PROFILE ENDPOINT
// -------------------------------------
type RegisterProfileRequest struct {
  PhoneNumber  string `json:"phone_number"`
  Role         string `json:"role"` // 'customer' หรือ 'driver'
  FirstName    string `json:"first_name"`
  LastName     string `json:"last_name"`
  VehicleType  string `json:"vehicle_type,omitempty"`  // เฉพาะ driver: 'bike' หรือ 'car'
  VehiclePlate string `json:"vehicle_plate,omitempty"` // เฉพาะ driver
}

type AuthTokenResponse struct {
  AccessToken string `json:"access_token"`
  TokenType   string `json:"token_type"`
  Role        string `json:"role"`
  UserID      string `json:"user_id"`
}

func (h *AuthHandler) RegisterEndpoint(w http.ResponseWriter, r *http.Request) {
  var req RegisterProfileRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  ctx := r.Context()
  verifyKey := VERIFY_PREFIX + req.PhoneNumber

  // [GUARD RAIL] ดักตรวจการแอบยิงสมัครตรงข้ามขั้นตอน OTP
  savedRole, err := core.RDB.Get(ctx, verifyKey).Result()
  if err != nil {
    core.WriteError(w, http.StatusForbidden,
      "Registration session has expired or is invalid. Complete OTP step first", "40301")
    return
  }

  if savedRole != req.Role {
    core.WriteError(w, http.StatusForbidden,
      "Token role mismatch tampering detected", "40303")
    return
  }

  if req.PhoneNumber == "" || req.FirstName == "" || req.LastName == "" {
    core.WriteError(w, http.StatusBadRequest,
      "Missing required profile details", "40007")
    return
  }

  if req.Role == "driver" {
    if req.VehicleType == "" || req.VehiclePlate == "" {
      core.WriteError(w, http.StatusBadRequest,
        "Vehicle information is required for drivers", "40008")
      return
    }
  }

  // ล้างตั๋วผ่านทางใน Redis เมื่อผ่านเกณฑ์การสมัครแล้ว ป้องกันการยิง Replay Attack
  _ = core.RDB.Del(ctx, verifyKey).Err()

  var userID uuid.UUID

  if req.Role == "customer" {
    var customer models.Customer
    err := core.DB.First(&customer, "phone_number = ?", req.PhoneNumber).Error

    // return เมื่อพบเบอร์ลงทะเบียนซ้ำ
    if err == nil {
      core.WriteError(w, http.StatusConflict,
        "This phone number is already registered. Please verify ownership first.", "40901")
      return
    }

    // สร้าง customer คนใหม่ UUID ใหม่
    newCustomer := models.Customer{
      ID:                uuid.New(),
      PhoneNumber:       req.PhoneNumber,
      FirstName:         req.FirstName,
      LastName:          req.LastName,
      IsProfileComplete: true,
      CreatedAt:         time.Now(),
    }

    if err := core.DB.Create(&newCustomer).Error; err != nil {
      core.WriteError(w, http.StatusInternalServerError,
        "Failed to register profile", "50015")
      return
    }
    userID = newCustomer.ID

  } else {
    // ฝั่งไรเดอร์ (Driver)
    var driver models.Driver
    err := core.DB.First(&driver, "phone_number = ?", req.PhoneNumber).Error

    // return เมื่อพบเบอร์ลงทะเบียนซ้ำ
    if err == nil {
      core.WriteError(w, http.StatusConflict,
        "This phone number is already registered. Please verify ownership first.", "40901")
      return
    }

    // new gorm transaction
    tx := core.DB.Begin()

    // สร้าง driver ใหม่ UUID ใหม่
    newDriver := models.Driver{
      ID:                uuid.New(),
      PhoneNumber:       req.PhoneNumber,
      FirstName:         req.FirstName,
      LastName:          req.LastName,
      VehicleType:       req.VehicleType,
      VehiclePlate:      req.VehiclePlate,
      IsProfileComplete: true,
      Status:            "offline",
      CreatedAt:         time.Now(),
    }
    if err := tx.Create(&newDriver).Error; err != nil {
      tx.Rollback()
      core.WriteError(w, http.StatusInternalServerError,
        "Failed to register driver profile", "50016")
      return
    }

    // เปิด driver wallet ผูกกับ Driver.ID ใหม่
    newWallet := models.DriverWallet{
      DriverID:  newDriver.ID,
      Balance:   decimal.NewFromFloat(0.00),
      CreatedAt: time.Now(),
      UpdatedAt: time.Now(),
    }
    if err := tx.Create(&newWallet).Error; err != nil {
      tx.Rollback()
      core.WriteError(w, http.StatusInternalServerError,
        "Failed to initialize driver wallet.", "50017")
      return
    }
    tx.Commit()
    userID = newDriver.ID
  }

  // สมัครสำเร็จ -> จ่าย Token ใหม่เข้าใช้งานระบบทันที
  tokenStr, err := h.createToken(userID, req.PhoneNumber, req.Role)
  if err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to generate security token", "50018")
    return
  }

  core.WriteSuccess(w, http.StatusCreated,
    "Profile registered successfully", "20000",
    AuthTokenResponse{
      AccessToken: tokenStr,
      TokenType:   "Bearer",
      Role:        req.Role,
      UserID:      userID.String(),
    },
  )
}

func (h *AuthHandler) createToken(userID uuid.UUID, phone, role string) (string, error) {
  token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
    "user_id":      userID.String(),
    "phone_number": phone,
    "role":         role,
    "exp":          time.Now().Add(SESSION_TTL).Unix(), // อายุ 30 วัน
    "iat":          time.Now().Unix(),
  })

  tokenStr, err := token.SignedString(jwtSecretKey)
  if err != nil {
    return "", err
  }

  // ผูกตั๋วใบนี้ไว้ในแรม Redis "session:user-uuid-xxxx" เก็บค่าเป็นตัว "jwt-string"
  ctx := context.Background()
  redisKey := SESSION_PREFIX + userID.String()

  err = core.RDB.Set(ctx, redisKey, tokenStr, SESSION_TTL).Err()
  if err != nil {
    return "", fmt.Errorf("failed to save token to redis: %v", err)
  }

  return tokenStr, nil
}

// -------------------------------------
// RECYCLE REGISTER PROFILE ENDPOINT
// -------------------------------------
// RecycleAndRegisterRequest สำหรับเคสที่ผู้ใช้ยืนยันว่า "ไม่ใช่ฉันคนเดิม" และต้องการสลัดบัญชีเก่าทิ้งทันที
type RecycleAndRegisterRequest struct {
  PhoneNumber  string `json:"phone_number"`
  Role         string `json:"role"` // 'customer' หรือ 'driver'
  FirstName    string `json:"first_name"`
  LastName     string `json:"last_name"`
  VehicleType  string `json:"vehicle_type,omitempty"`  // เฉพาะ driver
  VehiclePlate string `json:"vehicle_plate,omitempty"` // เฉพาะ driver
}

func (h *AuthHandler) RecycleAndRegisterEndpoint(w http.ResponseWriter, r *http.Request) {
  var req RecycleAndRegisterRequest
  if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    core.WriteError(w, http.StatusBadRequest, "Invalid JSON body format", "40000")
    return
  }

  ctx := r.Context()
  verifyKey := VERIFY_PREFIX + req.PhoneNumber

  // [GUARD RAIL] ตรวจตั๋ว OTP เหมือนเดิม ป้องกันการยิงตรงมาล้างระบบคนอื่น
  savedRole, err := core.RDB.Get(ctx, verifyKey).Result()
  if err != nil {
    core.WriteError(w, http.StatusForbidden,
      "Session has expired or is invalid.", "40301")
    return
  }

  if savedRole != req.Role {
    core.WriteError(w, http.StatusForbidden,
      "Token role mismatch tampering detected", "40303")
    return
  }

  if req.PhoneNumber == "" || req.FirstName == "" || req.LastName == "" {
    core.WriteError(w, http.StatusBadRequest,
      "Missing required profile details", "40007")
    return
  }

  if (req.Role == "driver") {
    if req.VehicleType == "" || req.VehiclePlate == "" {
      core.WriteError(w, http.StatusBadRequest,
        "Vehicle details are required for drivers", "40008")
      return
    }
  }

  // เคลียร์ตั๋วใน Redis
  _ = core.RDB.Del(ctx, verifyKey).Err()

  var userID uuid.UUID
  recycledPhone := fmt.Sprintf("%s_recycled_%d", req.PhoneNumber, time.Now().Unix())

  if req.Role == "customer" {
    var customer models.Customer
    // ย้ายเบอร์เก่าลงถังขยะทันทีที่มีการกด "No"
    if err := core.DB.First(&customer, "phone_number = ?", req.PhoneNumber).Error; err == nil {
      core.DB.Model(&customer).Update("phone_number", recycledPhone)
    }

    // สร้างผู้ใช้คนใหม่
    newCustomer := models.Customer{
      ID:                uuid.New(),
      PhoneNumber:       req.PhoneNumber,
      FirstName:         req.FirstName,
      LastName:          req.LastName,
      IsProfileComplete: true,
      CreatedAt:         time.Now(),
    }
    if err := core.DB.Create(&newCustomer).Error; err != nil {
      core.WriteError(w, http.StatusInternalServerError,
        "Failed to register profile", "50015")
      return
    }
    userID = newCustomer.ID

  } else {
    // ฝั่งไรเดอร์ (Driver)
    var driver models.Driver
    tx := core.DB.Begin()

    // ย้ายเบอร์ไรเดอร์คนเก่าลงถังขยะ
    if err := tx.First(&driver, "phone_number = ?", req.PhoneNumber).Error; err == nil {
      tx.Model(&driver).Update("phone_number", recycledPhone)
    }

    // สร้าง driver คนใหม่
    newDriver := models.Driver{
      ID:                uuid.New(),
      PhoneNumber:       req.PhoneNumber,
      FirstName:         req.FirstName,
      LastName:          req.LastName,
      VehicleType:       req.VehicleType,
      VehiclePlate:      req.VehiclePlate,
      IsProfileComplete: true,
      Status:            "offline",
      CreatedAt:         time.Now(),
    }
    if err := tx.Create(&newDriver).Error; err != nil {
      tx.Rollback()
      core.WriteError(w, http.StatusInternalServerError,
        "Failed to recycle and register driver profile", "50016")
      return
    }

    // เปิดกระเป๋าเงินใหม่ให้คนขับคนใหม่
    newWallet := models.DriverWallet{
      DriverID:  newDriver.ID,
      Balance:   decimal.NewFromFloat(0.00),
      CreatedAt: time.Now(),
      UpdatedAt: time.Now(),
    }
    if err := tx.Create(&newWallet).Error; err != nil {
      tx.Rollback()
      core.WriteError(w, http.StatusInternalServerError,
        "Failed to initialize wallet", "50017")
      return
    }

    tx.Commit()
    userID = newDriver.ID
  }

  // ออก Token ตัวใหม่ให้คนใหม่ได้ใช้งานระบบทันที
  tokenStr, err := h.createToken(userID, req.PhoneNumber, req.Role)
  if err != nil {
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to generate security token", "50018")
    return
  }

  core.WriteSuccess(w, http.StatusCreated,
    "Old profile recycled and new profile registered successfully.", "20000",
    AuthTokenResponse{
      AccessToken: tokenStr,
      TokenType:   "Bearer",
      Role:        req.Role,
      UserID:      userID.String(),
    },
  )
}

// โครงสร้างก้อน JSON ที่จะตอบกลับไปหาแอปหน้าบ้าน
type UserProfileResponse struct {
  ID           string    `json:"id"`
  PhoneNumber  string    `json:"phone_number"`
  FirstName    string    `json:"first_name"`
  LastName     string    `json:"last_name"`
  Role         string    `json:"role"`
  AvatarURL    string    `json:"avatar_url,omitempty"`
  VehicleType  string    `json:"vehicle_type,omitempty"`
  VehiclePlate string    `json:"vehicle_plate,omitempty"`
  CreatedAt    time.Time `json:"created_at"`
}

func (h *AuthHandler) GetProfileEndpoint(w http.ResponseWriter, r *http.Request) {
  // [GUARD RAIL] ดึง ID และ Role จาก Context ที่ด่านดัก Token แกะไว้ให้
  ctxUserID := r.Context().Value(UserIDKey)
  ctxRole := r.Context().Value(UserRoleKey)

  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized. Missing session identity.", "40101")
    return
  }
  userIDStr := ctxUserID.(string)
  userUUID, _ := uuid.Parse(userIDStr)

  // ตัวแปรสำหรับจัดรูปก้อนข้อมูลตอบกลับ
  var profileResponse UserProfileResponse

  // แยกสาขาตรวจสอบตามสิทธิ์ของผู้ใช้งาน
  if ctxRole == "driver" {
    var driver models.Driver
    if err := core.DB.First(&driver, "id = ?", userUUID).Error; err != nil {
      core.WriteError(w, http.StatusNotFound, "Driver profile not found", "40402")
      return
    }

    // ประกอบก้อนข้อมูลฝั่งคนขับ
    profileResponse = UserProfileResponse{
      ID:           driver.ID.String(),
      PhoneNumber:  driver.PhoneNumber,
      FirstName:    driver.FirstName,
      LastName:     driver.LastName,
      Role:         "driver",
      AvatarURL:    "",
      VehicleType:  driver.VehicleType,
      VehiclePlate: driver.VehiclePlate,
      CreatedAt:    driver.CreatedAt,
    }
  } else {
    // เคสปกติ: เป็นผู้โดยสาร (Customer)
    var customer models.Customer
    if err := core.DB.First(&customer, "id = ?", userUUID).Error; err != nil {
      core.WriteError(w, http.StatusNotFound, "Customer profile not found", "40403")
      return
    }

    // ประกอบก้อนข้อมูลฝั่งผู้โดยสาร
    profileResponse = UserProfileResponse{
      ID:          customer.ID.String(),
      PhoneNumber: customer.PhoneNumber,
      FirstName:   customer.FirstName,
      LastName:    customer.LastName,
      Role:        "customer",
      AvatarURL:   "",
      CreatedAt:   customer.CreatedAt,
    }
  }

  // ยิงข้อมูลโปรไฟล์กลับไปแบบคลีน ๆ ไวสมใจอยาก
  core.WriteSuccess(w, http.StatusOK,
    "Fetch profile successfully", "20000", profileResponse)
}

func (h *AuthHandler) LogoutEndpoint(w http.ResponseWriter, r *http.Request) {
  // ตรวจเช็กค่าตัวแปร nil
  ctxUserID := r.Context().Value(UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized,
      "Unauthorized access. Token context not found.", "40101")
    return
  }

  // ดึงไอดีที่แกะได้มาจากด่าน Middleware
  userID := r.Context().Value(UserIDKey).(string)

  // ลบ redis login session ออก
  _ = core.RDB.Del(r.Context(), SESSION_PREFIX + userID).Err()

  core.WriteSuccess(w, http.StatusOK,
    "Logged out successfully", "20000", nil)
}
