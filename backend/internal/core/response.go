// backend/internal/core/response.go
package core

import (
	"encoding/json"
	"net/http"
)

type JSONResponse struct {
  Success bool        `json:"success"`
  Message string      `json:"message"`
  Code    string      `json:"code"`
  Data    interface{} `json:"data,omitempty"`
}

// WriteSuccess: สำหรับส่ง Response ที่สำเร็จพร้อมข้อมูล (Data)
func WriteSuccess(
  w http.ResponseWriter, statusCode int, message string, errorCode string,
  data interface{}) {
  w.Header().Set("Content-Type", "application/json")
  w.WriteHeader(statusCode)

  json.NewEncoder(w).Encode(JSONResponse{
    Success: true,
    Message: message,
    Code:    errorCode,
    Data:    data,
  })
}

// WriteError: สำหรับส่ง Response เมื่อเกิดข้อผิดพลาด
func WriteError(
  w http.ResponseWriter, statusCode int, message string, errorCode string) {
  w.Header().Set("Content-Type", "application/json")
  w.WriteHeader(statusCode)

  json.NewEncoder(w).Encode(JSONResponse{
    Success: false,
    Message: message,
    Code:    errorCode,
    Data:    nil,
  })
}
