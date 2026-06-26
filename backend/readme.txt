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
OMISE_PUBLIC_KEY=pkey_test_5gyi3jzoz07f9o26x5t \
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
  -e AUTH_SECRET_KEY=a-string-secret-at-least-256-bits-long \
  -e OMISE_PUBLIC_KEY=pkey_test_5gyi3jzoz07f9o26x5t \
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
-H "Authorization: Bearer xxxxx" \
-d '{
  "vehicle_type": "bike",
  "pickup_latitude": "13.816200",
  "pickup_longitude": "100.560300",
  "distance_km": "7",
  "duration_minutes": "10",
  "surge_multiplier": "1.3",
  "card_token": "tokn_test_684yuh82n3q64jk6z6n",
  "payment_method": "promptpay",
  "origin_name": "เซ็นทรัลลาดพร้าว",
  "destination_name": "สยามพารากอน"
}'
// เซ็นทรัลลาดพร้าว
{
  "event": "update_location",
  "latitude": 13.8162,
  "longitude": 100.5603
}
// สยามพารากอน
{
  "event": "update_location",
  "latitude": 13.7469,
  "longitude": 100.5393
}

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

curl -X POST http://localhost:8080/api/v1/payments/omise/webhook \
-H "Content-Type: application/json" \
-d '{
  "object": "event",
  "type": "charge.complete",
  "data": {
    "id": "chrg_test_6853ltfsycj7nq3jhyl",
    "status": "successful"
  }
}'


curl -X POST http://localhost:8080/api/v1/rides/accept \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx" \
-d '{
  "ride_id": "f30b7906-28e1-4eff-8eb7-41968d39efb8"
}'

curl -X POST http://localhost:8080/api/v1/rides/arrive \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx" \
-d '{
  "ride_id": "f30b7906-28e1-4eff-8eb7-41968d39efb8"
}'

curl -X POST http://localhost:8080/api/v1/rides/start \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx" \
-d '{
  "ride_id": "f30b7906-28e1-4eff-8eb7-41968d39efb8"
}'

curl -X POST http://localhost:8080/api/v1/rides/complete \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx" \
-d '{
  "ride_id": "f30b7906-28e1-4eff-8eb7-41968d39efb8"
}'

curl -X POST http://localhost:8080/api/v1/rides/cancel \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx" \
-d '{
  "ride_id": "f30b7906-28e1-4eff-8eb7-41968d39efb8"
}'

curl -X GET http://localhost:8080/api/v1/wallets/driver/summary \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx"



curl -X POST http://localhost:8080/api/v1/auth/request-otp \
-H "Content-Type: application/json" \
-d '{
  "phone_number": "0812345888"
}'

curl -X POST http://localhost:8080/api/v1/auth/verify-otp \
-H "Content-Type: application/json" \
-d '{
  "phone_number": "0812345888",
  "otp_code": "744370",
  "role": "driver"
}'

curl -X POST http://localhost:8080/api/v1/auth/confirm-owner \
-H "Content-Type: application/json" \
-d '{
  "phone_number": "0812345888",
  "confirmed_name": "amdriver",
  "role": "driver"
}'

curl -X POST http://localhost:8080/api/v1/auth/register \
-H "Content-Type: application/json" \
-d '{
  "phone_number": "0812345888",
  "role": "driver",
  "first_name": "amdriver",
  "last_name": "gogogo",
  "vehicle_type": "car",
  "vehicle_plate": "2AB4888"
}'

curl -X POST http://localhost:8080/api/v1/auth/recycle-register \
-H "Content-Type: application/json" \
-d '{
  "phone_number": "0812345888",
  "role": "driver",
  "first_name": "amdriver",
  "last_name": "gogogo",
  "vehicle_type": "car",
  "vehicle_plate": "2AB4888"
}'

curl -X POST http://localhost:8080/api/v1/auth/logout \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx"

curl -X POST http://localhost:8080/api/v1/auth/me \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx"

curl -X POST http://localhost:8080/api/v1/rides/history \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx"

curl -X POST http://localhost:8080/api/v1/rides/current \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx"


- websocket connect
ws://localhost:8080/api/v1/realtime/driver \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx"

curl -X POST http://localhost:8080/api/v1/realtime/driver/toggle-status \
-H "Content-Type: application/json" \
-H "Authorization: Bearer xxxxx" \
-d '{
  "is_online": false
}'




rides/estimate
payments/omise/webhook

auth
- request-otp
- verify-otp
- confirm-owner
- register
- recycle-register
- logout

ride
- create
- accept
- arrive
- start
- complete
- cancel

wallets/driver/summary
realtime/driver
realtime/driver/toggle-status
