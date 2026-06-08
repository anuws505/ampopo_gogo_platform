// backend/pkg/omise/omise.go
package omise

import (
	"fmt"
	"os"

	"github.com/omise/omise-go"
	"github.com/omise/omise-go/operations"
	"github.com/shopspring/decimal"
)

type OmiseClient struct {
  client *omise.Client
}

func NewOmiseClient() (*OmiseClient, error) {
  secretKey := os.Getenv("OMISE_SECRET_KEY")
  if secretKey == "" {
    return nil, fmt.Errorf("Not found OMISE_SECRET_KEY in Environment Variable")
  }

  client, err := omise.NewClient("", secretKey)
  if err != nil {
    return nil, err
  }

  return &OmiseClient{client: client}, nil
}

func (o *OmiseClient) CreateHoldCharge(token string,
  amountTHB decimal.Decimal) (*omise.Charge, error) {
  // แปลงเงินบาทให้เป็นหน่วย "สตางค์" เป็นเลขจำนวนเต็ม int64
  amountSatang := amountTHB.Mul(decimal.NewFromInt(100)).IntPart()
  
  chargeOp := &operations.CreateCharge{
    Amount:      amountSatang,
    Currency:    "thb",
    Card:        token,
    DontCapture: true, 
  }

  result := &omise.Charge{}
  if err := o.client.Do(result, chargeOp); err != nil {
    return nil, fmt.Errorf("omise error: %w", err)
  }

  return result, nil
}

func (o *OmiseClient) CaptureCharge(chargeID string) (*omise.Charge, error) {
  captureOp := &operations.CaptureCharge{
    ChargeID: chargeID,
  }

  result := &omise.Charge{}
  if err := o.client.Do(result, captureOp); err != nil {
    return nil, fmt.Errorf("omise capture error: %w", err)
  }

  return result, nil
}
