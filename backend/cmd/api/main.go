// backend/cmd/api/main.go
package main

import (
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"ampopo_gogo_platform/internal/ride"
	"ampopo_gogo_platform/internal/wallet"
	"ampopo_gogo_platform/pkg/omise"
	"fmt"
	"net/http"

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

  // 2. ประกาศชิ้นส่วนคำนวณราคา
  pricingService := ride.NewPricingService()
  rideHandler := ride.NewRideHandler(pricingService, omiseClient)

  // 3. ตั้งค่าเส้นทาง HTTP ROUTES
  r.HandleFunc("/api/v1/rides/estimate", rideHandler.EstimateFareEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/rides/create", rideHandler.CreateRideEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/rides/accept", rideHandler.AcceptRideEndpoint).Methods("POST")
  r.HandleFunc("/api/v1/rides/complete", rideHandler.CompleteRideEndpoint).Methods("POST")

  // ประกาศชิ้นส่วนฝั่ง Wallet
  walletService := wallet.NewWalletService()
  walletHandler := wallet.NewWalletHandler(walletService)

  r.HandleFunc("/api/v1/wallets/driver/{driver_id}/summary", walletHandler.GetWalletSummaryEndpoint).Methods("GET")

  // Workers
  // go workers.DoCleanupExpiredDrafts()

  // เปิด Server ที่ Port 8080
  port := ":8080"
  fmt.Printf("Ampopo Gogo App is running on http://localhost%s\n", port)

  if err := http.ListenAndServe(port, r); err != nil {
    fmt.Printf("Server failed: %s\n", err)
  }
}
