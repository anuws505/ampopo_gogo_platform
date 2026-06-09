- init
go mod init ampopo_gogo_platform

- remove go.sum
rm go.sum

- clear module cache
go clean -cache -modcache

- จัดระเบียบ Module ใหม่ และโหลด Library ที่ขาด
go mod tidy

- ติดตั้ง GORM (Core Package)
go get gorm.io/gorm

- ติดตั้ง Driver Postgres สำหรับ GORM
go get gorm.io/driver/postgres

- options
go get github.com/shopspring/decimal
go get github.com/google/uuid



- on local environment
docker compose up -d
docker compose down -v

DB_HOST=localhost \
DB_USER=user \
DB_PASS=password \
DB_NAME=ampopo_gogo \
DB_PORT=5432 \
REDIS_ADDR=localhost:6379 \
AUTH_SECRET_KEY=a-string-secret-at-least-256-bits-long \
OMISE_SECRET_KEY=skey_test_5gyi3jzp0b5dhnux2m3 \
go run cmd/api/main.go

- docker build command
docker build -t ampopo_gogo_platform:v1 .

- docker option
docker image prune -f

- docker run command
docker run --network backend_default \
  -p 8080:8080 \
  -e DB_HOST=db \
  -e DB_USER=user \
  -e DB_PASS=password \
  -e DB_NAME=ampopo_gogo \
  -e DB_PORT=5432 \
  -e REDIS_ADDR=cache:6379 \
  -e UTH_SECRET_KEY=a-string-secret-at-least-256-bits-long \
  -e OMISE_SECRET_KEY=skey_test_5gyi3jzp0b5dhnux2m3 \
  ampopo_gogo_platform:v1

- or command with ".env" files
docker run --network backend_default \
  -p 8080:8080 \
  -e DB_HOST=db \
  -e REDIS_ADDR=cache:6379 \
  --env-file .env \
  ampopo_gogo_platform:v1



curl -X POST http://localhost:8080/api/v1/rides/estimate \
-H "Content-Type: application/json" \
-d '{
  "vehicle_type": "car",
  "distance_km": "4",
  "duration_minutes": "12",
  "surge_multiplier": "1.5"
}'

curl -X POST http://localhost:8080/api/v1/rides/create \
-H "Content-Type: application/json" \
-d '{
  "customer_id": "00000000-0000-0000-0000-000000000001",
  "vehicle_type": "car",
  "distance_km": "5",
  "duration_minutes": "10",
  "surge_multiplier": "1.5",
  "card_token": "tokn_test_67yk13yzh33c7mcu0ks",
  "origin_name": "เซ็นทรัลลาดพร้าว",
  "destination_name": "สยามพารากอน"
}'

curl https://vault.omise.co/tokens \
  -X POST \
  -u "pkey_test_5gyi3jzoz07f9o26x5t" \
  -d "card[name]=Somchai Prasert" \
  -d "card[city]=Bangkok" \
  -d "card[postal_code]=10320" \
  -d "card[number]=4242424242424242" \
  -d "card[security_code]=123" \
  -d "card[expiration_month]=12" \
  -d "card[expiration_year]=2027"

curl -X POST http://localhost:8080/api/v1/rides/accept \
-H "Content-Type: application/json" \
-d '{
  "ride_id": "cd580814-b115-48d6-a28d-eb56efc2d7dd",
  "driver_id": "00000000-0000-0000-0000-000000000002"
}'

curl -X POST http://localhost:8080/api/v1/rides/complete \
-H "Content-Type: application/json" \
-d '{
  "ride_id": "505197bf-81a4-487b-ac55-4086b6a7d2cd"
}'

curl -X GET http://localhost:8080/api/v1/wallets/driver/00000000-0000-0000-0000-000000000002/summary

curl -X POST http://localhost:8080/api/v1/rides/cancel \
-H "Content-Type: application/json" \
-d '{
  "ride_id": "cd580814-b115-48d6-a28d-eb56efc2d7dd"
}'
