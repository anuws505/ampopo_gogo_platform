// backend/cmd/api/main.go
package main

import (
	"ampopo_gogo_platform/internal/auth"
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"ampopo_gogo_platform/internal/realtime"
	"ampopo_gogo_platform/internal/ride"
	"ampopo_gogo_platform/internal/wallet"
	"ampopo_gogo_platform/pkg/omise"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

func main() {
  core.InitDB()

  fmt.Println("Migrating database...")
  core.DB.AutoMigrate(
    &models.Customer{},
    &models.Driver{},
    &models.DriverWallet{},
    &models.Ride{},
    &models.FinancialTransaction{},
  )
  fmt.Println("Database Migrated: All tables are ready!")

  r := mux.NewRouter()

  // 1. เปิดสวิตช์ระบบจ่ายเงิน Omise
  omiseClient, err := omise.NewOmiseClient()
  if err != nil {
    panic(fmt.Sprintf("Can't connect to Omise: %v", err))
  }
  fmt.Println("Omise Payment Gateway connected -- test_mode!")

  // 1. สตาร์ทเครื่องยนต์ระบบ Real-time Hub ขึ้นมาสแตนบายไว้ในหน่วยความจำ
  realtimeHub := realtime.NewHub()

  // 2. ประกาศชิ้นส่วนคำนวณราคา
  pricingService := ride.NewPricingService()
  rideHandler := ride.NewRideHandler(pricingService, omiseClient, realtimeHub)

  // 3. ตั้งค่าเส้นทาง HTTP ROUTES
  // กลุ่มที่ 1: เส้นทางสาธารณะ (Public Routes - ไม่ต้องแนบตั๋ว JWT ก็ยิงได้)
  r.HandleFunc("/api/v1/rides/estimate", rideHandler.EstimateFareEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/payments/omise/webhook", rideHandler.OmiseWebhookEndpoint).Methods("POST")

  authHandler := auth.NewAuthHandler()
  r.HandleFunc("/api/v1/auth/request-otp", authHandler.RequestOTPEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/auth/verify-otp", authHandler.VerifyOTPEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/auth/confirm-owner", authHandler.ConfirmOwnerEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/auth/register", authHandler.RegisterEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/auth/recycle-register", authHandler.RecycleAndRegisterEndpoint).Methods("POST")

  // กลุ่มที่ 2: เส้นทางปลอดภัย (Protected Routes - ต้องตรวจตั๋ว JWT ก่อนเสมอ)
  protected := r.PathPrefix("/api/v1").Subrouter()
  protected.Use(auth.AuthMiddleware)

  protected.HandleFunc("/auth/logout", authHandler.LogoutEndpoint).Methods("POST")
  protected.HandleFunc("/rides/create", rideHandler.CreateRideEndpoint).Methods("POST")
  protected.HandleFunc("/rides/accept", rideHandler.AcceptRideEndpoint).Methods("POST")
  protected.HandleFunc("/rides/arrive", rideHandler.ArriveRideEndpoint).Methods("POST")
  protected.HandleFunc("/rides/start", rideHandler.StartRideEndpoint).Methods("POST")
  protected.HandleFunc("/rides/complete", rideHandler.CompleteRideEndpoint).Methods("POST")
  protected.HandleFunc("/rides/cancel", rideHandler.CancelRideEndpoint).Methods("POST")

  // ประกาศชิ้นส่วนฝั่ง Wallet
  walletService := wallet.NewWalletService()
  walletHandler := wallet.NewWalletHandler(walletService)

  protected.HandleFunc("/wallets/driver/summary", walletHandler.GetWalletSummaryEndpoint).Methods("GET")

  // ==========================================
  // REAL-TIME WEBSOCKET ENDPOINT
  // ==========================================
  // เส้นทางนี้ให้แอป Flutter ฝั่งไรเดอร์ใช้เกาะสายสัญญาณค้างไว้ตอนเปิดแอปเปิดระบบ Online
  r.HandleFunc("/api/v1/realtime/driver/{driver_id}", func(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    driverIDStr := vars["driver_id"]

    driverUUID, err := uuid.Parse(driverIDStr)
    if err != nil {
      http.Error(w, "รูปแบบรหัสไรเดอร์ไม่ถูกต้อง", http.StatusBadRequest)
      return
    }

    // สั่งให้ Hub จัดการเปลี่ยน Protocol และคุมท่อสื่อสารของไรเดอร์คนนี้
    realtimeHub.HandleDriverConnection(w, r, driverUUID)
  }).Methods("GET") // WebSocket จะเริ่มคุยด้วย Method "GET" เสมอ

  // Workers
  go realtimeHub.StartDispatchWorker(core.Ctx)

  // เปิด Server ที่ Port 8080
  port := ":8080"
  fmt.Printf("Ampopo Gogo App is running on http://localhost%s\n", port)

  if err := http.ListenAndServe(port, r); err != nil {
    fmt.Printf("Server failed: %s\n", err)
  }
}
