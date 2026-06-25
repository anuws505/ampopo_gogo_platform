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
  publicKey := os.Getenv("OMISE_PUBLIC_KEY")
  if publicKey == "" {
    return nil, fmt.Errorf("Not found OMISE_PUBLIC_KEY in Environment Variable")
  }

  secretKey := os.Getenv("OMISE_SECRET_KEY")
  if secretKey == "" {
    return nil, fmt.Errorf("Not found OMISE_SECRET_KEY in Environment Variable")
  }

  client, err := omise.NewClient(publicKey, secretKey)
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

func (o *OmiseClient) ReverseCharge(chargeID string) (*omise.Charge, error) {
  reverseOp := &operations.ReverseCharge{
    ChargeID: chargeID,
  }

  result := &omise.Charge{}
  if err := o.client.Do(result, reverseOp); err != nil {
    return nil, fmt.Errorf("omise reverse error: %w", err)
  }

  return result, nil
}

func (o *OmiseClient) CreatePromptPayCharge(amountTHB decimal.Decimal) (*omise.Charge, error) {
  amountSatang := amountTHB.Mul(decimal.NewFromInt(100)).IntPart()

  // 1. ระบุประเภท Payment Source ก่อน
  sourceOp := &operations.CreateSource{
    Amount:   amountSatang,
    Currency: "thb",
    Type:     "promptpay",
  }
  sourceResult := &omise.Source{}
  if err := o.client.Do(sourceResult, sourceOp); err != nil {
    return nil, fmt.Errorf("omise promptpay source creation error: %w", err)
  }

  // 2. นำ Source ID ที่ได้ ไปเปิดบิลสร้าง Charge ทันที
  chargeOp := &operations.CreateCharge{
    Amount:   amountSatang,
    Currency: "thb",
    Source:   sourceResult.ID,
  }
  chargeResult := &omise.Charge{}
  if err := o.client.Do(chargeResult, chargeOp); err != nil {
    return nil, fmt.Errorf("omise promptpay charge creation error: %w", err)
  }

  return chargeResult, nil
}

func (o *OmiseClient) RefundCharge(chargeID string, amountTHB decimal.Decimal) (*omise.Refund, error) {
  amountSatang := amountTHB.Mul(decimal.NewFromInt(100)).IntPart()

  refundOp := &operations.CreateRefund{
    ChargeID: chargeID,
    Amount:   amountSatang,
  }

  result := &omise.Refund{}
  if err := o.client.Do(result, refundOp); err != nil {
    return nil, fmt.Errorf("omise refund error: %w", err)
  }

  return result, nil
}
