// backend/internal/ride/service.go
package ride

import (
	"github.com/shopspring/decimal"
)

type PricingConfig struct {
  BaseFare          decimal.Decimal // ราคาเริ่มต้น
  BaseDistanceKM    decimal.Decimal // ระยะทางเริ่มต้นที่ใช้คิดราคา
  PerKMFare         decimal.Decimal // ค่าบริการกิโลเมตรถัดไป
  PerMinuteFare     decimal.Decimal // ค่าเวลาเดินทางต่อนาที
  DriverShareRate   decimal.Decimal // อัตราส่วนแบ่งคนขับ
  PlatformShareRate decimal.Decimal // อัตราส่วนแบ่งแอป
}

type PricingService struct {
  bikeConfig PricingConfig
  carConfig  PricingConfig
}

func NewPricingService() *PricingService {
  return &PricingService{
    // คอนฟิกฝั่ง มอเตอร์ไซค์: หักคอมมิชชัน 14% (คนขับได้ 86%)
    bikeConfig: PricingConfig{
      BaseFare:          decimal.NewFromFloat(35.00),
      BaseDistanceKM:    decimal.NewFromFloat(2.00),
      PerKMFare:         decimal.NewFromFloat(6.00),
      PerMinuteFare:     decimal.NewFromFloat(0.00),
      DriverShareRate:   decimal.NewFromFloat(0.86),
      PlatformShareRate: decimal.NewFromFloat(0.14),
    },
    // คอนฟิกฝั่ง รถยนต์: หักคอมมิชชัน 14% (คนขับได้ 86%) เท่ากัน
    carConfig: PricingConfig{
      BaseFare:          decimal.NewFromFloat(45.00),
      BaseDistanceKM:    decimal.NewFromFloat(2.00),
      PerKMFare:         decimal.NewFromFloat(10.00),
      PerMinuteFare:     decimal.NewFromFloat(2.00),
      DriverShareRate:   decimal.NewFromFloat(0.86),
      PlatformShareRate: decimal.NewFromFloat(0.14),
    },
  }
}

type FareCalculationResult struct {
  TotalFare     decimal.Decimal `json:"total_fare"`
  DriverShare   decimal.Decimal `json:"driver_share"`
  PlatformShare decimal.Decimal `json:"platform_share"`
}

// CalculateFare คำนวณราคาแบบทศนิยม พร้อมตัวคูณ Surge ทั้งก้อนตามโมเดลล่าสุด
func (s *PricingService) CalculateFare(vehicleType string, distanceKM decimal.Decimal,
  durationMinutes decimal.Decimal, surgeMultiplier decimal.Decimal) FareCalculationResult {
  
  // เลือก Config ให้ถูกตามประเภทรถที่เรียก
  var cfg PricingConfig
  if vehicleType == "car" {
    cfg = s.carConfig
  } else {
    cfg = s.bikeConfig
  }

  // Short Trip Optimization: ถ้าระยะทางสั้นมาก (< 500 เมตร) ปัดเป็นราคาขั้นต่ำ
  minDistanceThreshold := decimal.NewFromFloat(0.50)
  if distanceKM.LessThan(minDistanceThreshold) {
    distanceKM = cfg.BaseDistanceKM
  }

  totalFare := cfg.BaseFare
  extraDistanceFare := decimal.NewFromFloat(0.00)

  // 1. คำนวณส่วนต่างระยะทางกิโลเมตรถัดไป
  if distanceKM.GreaterThan(cfg.BaseDistanceKM) {
    extraDistance := distanceKM.Sub(cfg.BaseDistanceKM)
    extraDistanceFare = extraDistance.Mul(cfg.PerKMFare)
  }

  // 2. คำนวณค่าเวลาเดินทาง/รถติด (Duration Fare)
  durationFare := durationMinutes.Mul(cfg.PerMinuteFare)

  // 3. รวมราคาฐานและค่าบริการตามระยะทาง/เวลาทั้งหมดเข้าด้วยกันก่อนคูณ Surge
  totalFare = totalFare.Add(extraDistanceFare).Add(durationFare)

  // 4. คูณตัวแปร Surge ครอบคลุม "ทั้งก้อน" ตามสเปกใหม่ที่คุณปรับปรุง
  totalFare = totalFare.Mul(surgeMultiplier)

  // 5. ปัดเศษทศนิยม 2 ตำแหน่งตามมาตรฐานการเงินสากล
  totalFare = totalFare.Round(2)

  // 6. คิดส่วนแบ่งสุทธิจากยอดรวมที่แท้จริง
  driverShare := totalFare.Mul(cfg.DriverShareRate).Round(2)
  platformShare := totalFare.Sub(driverShare) // ใช้วิธีลบออกเพื่อป้องกันเศษสตางค์สูญหายจากการปัดเศษ

  return FareCalculationResult{
    TotalFare:     totalFare,
    DriverShare:   driverShare,
    PlatformShare: platformShare,
  }
}
