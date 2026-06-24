// backend/internal/wallet/service.go
package wallet

import (
	"ampopo_gogo_platform/internal/core"
	"ampopo_gogo_platform/internal/models"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WalletService struct{}

func NewWalletService() *WalletService {
  return &WalletService{}
}

// GetWalletBalance ดึงข้อมูลกระเป๋าเงินเพื่อดูยอดคงเหลือปัจจุบัน
func (s *WalletService) GetWalletBalance(driverID uuid.UUID) (*models.DriverWallet, error) {
  var wallet models.DriverWallet
  err := core.DB.First(&wallet, "driver_id = ?", driverID).Error
  if err != nil {
    if errors.Is(err, gorm.ErrRecordNotFound) {
      return nil, errors.New("Driver wallet not found")
    }
    return nil, err
  }
  return &wallet, nil
}

// GetTransactionHistory ดึงรายการเดินบัญชีย้อนหลังของคนขับ
func (s *WalletService) GetTransactionHistory(driverID uuid.UUID,
  limit int) ([]models.FinancialTransaction, error) {
  var txs []models.FinancialTransaction

  // คิวรี่เรียงจากใหม่สุดไปเก่าสุด (Latest First)
  err := core.DB.Where("driver_id = ?", driverID).
    Order("created_at desc").
    Limit(limit).
    Find(&txs).Error
    
  if err != nil {
    return nil, err
  }
  return txs, nil
}
