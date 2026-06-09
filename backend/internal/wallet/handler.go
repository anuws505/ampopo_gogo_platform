// backend/internal/wallet/handler.go
package wallet

import (
	"ampopo_gogo_platform/internal/core"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type WalletHandler struct {
  walletService *WalletService
}

func NewWalletHandler(ws *WalletService) *WalletHandler {
  return &WalletHandler{
    walletService: ws,
  }
}

// GetWalletSummaryEndpoint รวมยอดเงินคงเหลือและประวัติธุรกรรมล่าสุดส่งกลับใน API เดียว
func (h *WalletHandler) GetWalletSummaryEndpoint(w http.ResponseWriter, r *http.Request) {
  // ดึง driver_id จาก URL Path เช่น /api/v1/wallets/driver/{driver_id}/summary
  vars := mux.Vars(r)
  driverIDStr := vars["driver_id"]

  driverUUID, err := uuid.Parse(driverIDStr)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest, "รูปแบบรหัสคนขับ (driver_id) ไม่ถูกต้อง", "40021")
    return
  }

  // 1. ดึงยอดเงินคงเหลือ
  wallet, err := h.walletService.GetWalletBalance(driverUUID)
  if err != nil {
    core.WriteError(w, http.StatusNotFound, err.Error(), "40421")
    return
  }

  // 2. ดึงประวัติธุรกรรม 20 รายการล่าสุด (เผื่อ Query limit ส่งมาทาง URL เช่น ?limit=10)
  limit := 20
  if limitQuery := r.URL.Query().Get("limit"); limitQuery != "" {
    if l, err := strconv.Atoi(limitQuery); err == nil && l > 0 {
      limit = l
    }
  }

  transactions, err := h.walletService.GetTransactionHistory(driverUUID, limit)
  if err != nil {
    core.WriteError(w, http.StatusInternalServerError, "ไม่สามารถเรียกดูประวัติธุรกรรมได้", "50021")
    return
  }

  // 3. ประกอบร่างส่งผลลัพธ์กลับ
  response := map[string]interface{}{
    "driver_id":    wallet.DriverID.String(),
    "balance":      wallet.Balance,
    "updated_at":   wallet.UpdatedAt,
    "transactions": transactions,
  }

  core.WriteSuccess(w, http.StatusOK, "ดึงข้อมูลกระเป๋าเงินสำเร็จ", "20000", response)
}
