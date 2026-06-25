// backend/internal/auth/middleware.go
package auth

import (
	"ampopo_gogo_platform/internal/core"
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type ContextKey string
const (
  UserIDKey      ContextKey = "user_id"
  UserRoleKey    ContextKey = "role"
  UserPhoneKey   ContextKey = "phone_number"
)

// AuthMiddleware ด่านตรวจตั๋ว JWT ก่อนยอมให้ยิงเรียกใช้งานระบบ Ride
func AuthMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    authHeader := r.Header.Get("Authorization")
    if authHeader == "" {
      core.WriteError(w, http.StatusUnauthorized,
        "Missing Authorization header", "40100")
      return
    }

    parts := strings.Split(authHeader, " ")
    if len(parts) != 2 || parts[0] != "Bearer" {
      core.WriteError(w, http.StatusUnauthorized,
        "Invalid Authorization format", "40105")
      return
    }

    tokenString := parts[1]

    // 1. ถอดรหัสโครงสร้างตั๋ว JWT ตามปกติ
    token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
      return jwtSecretKey, nil
    })

    if err != nil || !token.Valid {
      core.WriteError(w, http.StatusUnauthorized,
        "Token is invalid or expired", "40101")
      return
    }

    claims, ok := token.Claims.(jwt.MapClaims)
    if !ok {
      core.WriteError(w, http.StatusUnauthorized,
        "Invalid token claims", "40106")
      return
    }

    userIDStr := claims["user_id"].(string)

    // 2. วิ่งไปถาม Redis ว่าตั๋วของไอดีนี้ในระบบยังตรงกันอยู่ไหม
    ctx := r.Context()
    redisKey := SESSION_PREFIX + userIDStr

    savedToken, err := core.RDB.Get(ctx, redisKey).Result()
    if err != nil {
      // หาไม่เจอ แปลว่าโดนแอดมินสั่งลบ/สั่งระงับ หรือเขากด Logout ไปแล้ว
      core.WriteError(w, http.StatusUnauthorized,
        "Session has expired or been terminated", "40102")
      return
    }

    // ตรวจว่า Token ตรงกันเป๊ะไหม ป้องกันกรณีล็อกอินซ้ำจากเครื่องอื่นแล้ว Token เปลี่ยน (Single Device Login)
    if savedToken != tokenString {
      core.WriteError(w, http.StatusUnauthorized,
        "Logged in from another device. Terminated.", "40107")
      return
    }

    // หากผ่านด่านหมด ส่งข้อมูลลง Context ไหลเข้าเอนพอยต์ตัวจริงตามปกติ
    ctx = context.WithValue(r.Context(), UserIDKey, userIDStr)
    ctx = context.WithValue(ctx, UserRoleKey, claims["role"])
    ctx = context.WithValue(ctx, UserPhoneKey, claims["phone_number"])

    next.ServeHTTP(w, r.WithContext(ctx))
  })
}
