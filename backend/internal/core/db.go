// backend/internal/core/db.go
package core

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
  DB  *gorm.DB
  RDB *redis.Client
  Ctx = context.Background()
)

func InitDB() {
  host := GetEnv("DB_HOST", "localhost")
  user := GetEnv("DB_USER", "user")
  pass := GetEnv("DB_PASS", "password")
  name := GetEnv("DB_NAME", "ampopo_gogo")
  port := GetEnv("DB_PORT", "5432")
  redis_addr := GetEnv("REDIS_ADDR", "localhost:6379")

  dsn := fmt.Sprintf(
    "host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
    host, user, pass, name, port)

  // dsn := "host=localhost user=user password=password dbname=ampopo_gogo port=5432 sslmode=disable"
  // redis_addr := "localhost:6379"

  // เชื่อมต่อ Postgres
  var err error
  DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
  if err != nil {
    log.Fatal("Failed to connect to Postgres: ", err)
  }

  sqlDB, err := DB.DB()
  if err != nil {
    log.Fatal("Failed to get generic database object:", err)
  }

  sqlDB.SetMaxOpenConns(10)
  sqlDB.SetMaxIdleConns(5)
  sqlDB.SetConnMaxLifetime(time.Hour)

  fmt.Println("Postgres connected!")

  // เชื่อมต่อ Redis
  RDB = redis.NewClient(&redis.Options{
    Addr: redis_addr,
    Password: "",
    DB: 0,
    MaxRetries: 3,
    MinRetryBackoff: 500 * time.Millisecond,
    PoolSize: 10,
  })

  if err := RDB.Ping(Ctx).Err(); err != nil {
    log.Fatal("Failed to connect to Redis:", err)
  }

  fmt.Println("Redis connected!")
}
