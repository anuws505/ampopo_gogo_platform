// backend/internal/models/models.go
package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// 1. Customer Model (ตารางลูกค้า)
type Customer struct {
  ID                uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
  PhoneNumber       string    `gorm:"type:varchar(50);unique;not null"`
  FirstName         string    `gorm:"type:varchar(100);not null"`
  LastName          string    `gorm:"type:varchar(100);not null"`
  IsProfileComplete bool      `gorm:"type:boolean;default:false"`
  CreatedAt         time.Time `gorm:"autoCreateTime"`
}

// 2. Driver Model (ตารางคนขับ/ไรเดอร์)
type Driver struct {
  ID                uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
  PhoneNumber       string    `gorm:"type:varchar(50);unique;not null"`
  FirstName         string    `gorm:"type:varchar(100);not null"`
  LastName          string    `gorm:"type:varchar(100);not null"`
  VehicleType       string    `gorm:"type:varchar(20);not null"` // 'bike' หรือ 'car'
  VehiclePlate      string    `gorm:"type:varchar(50);not null"` // ทะเบียนรถ
  Status            string    `gorm:"type:varchar(20);default:'offline'"` // 'offline', 'online', 'busy'
  IsProfileComplete bool      `gorm:"type:boolean;default:false"`
  CreatedAt         time.Time `gorm:"autoCreateTime"`
}

// 3. DriverWallet Model (ตารางกระเป๋าเงินคนขับ)
type DriverWallet struct {
  DriverID  uuid.UUID       `gorm:"type:uuid;primaryKey"`
  Balance   decimal.Decimal `gorm:"type:numeric(10,2);not null;default:0.00"` // ทศนิยม 2 ตำแหน่ง
  CreatedAt time.Time       `gorm:"autoCreateTime"`
  UpdatedAt time.Time       `gorm:"autoUpdateTime"`
}

// 4. Ride Model (ตารางประวัติการวิ่งทริปเรียกรถ)
type Ride struct {
  ID               uuid.UUID       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
  CustomerID       uuid.UUID       `gorm:"type:uuid;not null"`
  DriverID         *uuid.UUID      `gorm:"type:uuid"` // ใช้ Pointer เผื่อกรณีเริ่มแรกยังไม่มีคนขับรับงาน (Null)
  VehicleType      string          `gorm:"type:varchar(20);not null"`

  // พิกัดปักหมุดแผนที่
  PickupLatitude   decimal.Decimal `gorm:"type:numeric(9,6);not null"`
  PickupLongitude  decimal.Decimal `gorm:"type:numeric(9,6);not null"`
  DropoffLatitude  decimal.Decimal `gorm:"type:numeric(9,6);not null"`
  DropoffLongitude decimal.Decimal `gorm:"type:numeric(9,6);not null"`

  // รายละเอียดระยะทางและราคา
  DistanceKM       decimal.Decimal `gorm:"type:numeric(5,2);not null"` // เช่น 2.00 กม.
  DurationMinutes  decimal.Decimal `gorm:"type:numeric(5,2);not null"` // สำหรับรถยนต์
  SurgeMultiplier  decimal.Decimal `gorm:"type:numeric(3,2);default:1.00"` // เช่น 1.30

  // ข้อมูลการเงินสุทธิของทริป (จากสูตร 35 บาท ขั้นต่ำ / คอม 14%)
  TotalFare        decimal.Decimal `gorm:"type:numeric(10,2);not null"` // ยอดเต็มจากลูกค้า (เช่น 35.00)
  DriverShare      decimal.Decimal `gorm:"type:numeric(10,2);not null"` // โอนให้คนขับ 86% (เช่น 30.10)
  PlatformShare    decimal.Decimal `gorm:"type:numeric(10,2);not null"` // เข้าแอป 14% (เช่น 4.90)

  // สเตตัสและข้อมูล Gateway
  PaymentMethod    string          `gorm:"type:varchar(20);not null"` // 'promptpay', 'credit_card', 'cash'
  PaymentStatus    string          `gorm:"type:varchar(20);default:'pending'"` // 'pending', 'authorized', 'captured', 'voided'
  OmiseChargeID    *string         `gorm:"type:varchar(100)"` // ใช้คุยกับ Omise API (Hold/Capture)

  // 'requested', 'searching', 'pending_payment, 'accepted'', 'completed', 'cancelled'
  // 'arrived', 'on_trip'
  Status           string          `gorm:"type:varchar(30);default:'requested'"`
  OriginName       string          `gorm:"type:varchar(100)"`
  DestinationName  string          `gorm:"type:varchar(100)"`

  CreatedAt        time.Time       `gorm:"autoCreateTime"`
  UpdatedAt        time.Time       `gorm:"autoUpdateTime"`

  // Relationships สำหรับให้ GORM ดึงข้อมูลเชื่อมโยงง่ายๆ
  Customer         Customer        `gorm:"foreignKey:CustomerID"`
  Driver           *Driver         `gorm:"foreignKey:DriverID"`
}

// 5. FinancialTransaction Model (ตารางบัญชีแยกประเภทควบคุมเงิน)
type FinancialTransaction struct {
  ID          uuid.UUID       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`

  // ส่วนเชื่อมโยงความสัมพันธ์ (Foreign Keys & Relationships)
  RideID      *uuid.UUID      `gorm:"type:uuid;index" json:"ride_id"` // ผูกกับทริป (เป็น NULL ได้ถ้าเป็นการถอนเงิน/เติมเงิน)
  DriverID    *uuid.UUID      `gorm:"type:uuid;index" json:"driver_id"` // ผูกกับคนขับโดยตรง เพื่อคิวรี่ประวัติรายได้ฝั่งคนขับได้รวดเร็ว

  // ประเภทธุรกรรมและทิศทางเงิน
  TxType      string          `gorm:"type:varchar(20);not null" json:"tx_type"` // 'earning' (รายได้ทริป), 'withdrawal' (ถอนเงิน), 'topup' (เติมเงิน), 'fee' (ค่าธรรมเนียมแอป)
  Amount      decimal.Decimal `gorm:"type:numeric(10,2);not null" json:"amount"` // ยอดเงินขยับ (ใช้เลขบวกเสมอ แล้วดูทิศทางจาก TxType หรือจะใช้ +/- ก็ได้ครับ)

  // ข้อมูลบันทึกเพิ่มเติม
  Description string          `gorm:"type:varchar(255);not null" json:"description"` // รายละเอียด เช่น "รายได้จากทริป"
  CreatedAt   time.Time       `gorm:"autoCreateTime" json:"created_at"`
}
