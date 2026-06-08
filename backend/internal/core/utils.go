// backend/internal/core/utils.go
package core

import (
	"os"
)

func GetEnv(key, fallback string) string {
  if value, ok := os.LookupEnv(key); ok {
    return value
  }
  return fallback
}
