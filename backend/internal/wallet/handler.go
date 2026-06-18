// backend/internal/wallet/handler.go
package wallet

import (
	"ampopo_gogo_platform/internal/auth"
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

func (h *WalletHandler) GetWalletSummaryEndpoint(w http.ResponseWriter, r *http.Request) {
  ctxUserID := r.Context().Value(auth.UserIDKey)
  if ctxUserID == nil {
    core.WriteError(w, http.StatusUnauthorized, 
      "Unauthorized access. Missing session identity.", "40101")
    return
  }
  tokenUserIDStr := ctxUserID.(string)

  vars := mux.Vars(r)
  driverIDStr := vars["driver_id"]

  if tokenUserIDStr != driverIDStr {
    core.WriteError(w, http.StatusForbidden, 
      "Access denied. You do not have permission to view this wallet profile.", "40305")
    return
  }

  driverUUID, err := uuid.Parse(driverIDStr)
  if err != nil {
    core.WriteError(w, http.StatusBadRequest,
      "Invalid driver identity key format.", "40021")
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
    core.WriteError(w, http.StatusInternalServerError,
      "Failed to retrieve wallet statement and transaction history.", "50021")
    return
  }

  // 3. ประกอบร่างส่งผลลัพธ์กลับ
  response := map[string]interface{}{
    "driver_id":    wallet.DriverID.String(),
    "balance":      wallet.Balance,
    "updated_at":   wallet.UpdatedAt,
    "transactions": transactions,
  }

  core.WriteSuccess(w, http.StatusOK,
    "Wallet snapshot and statements retrieved successfully.", "20000", response)
}
