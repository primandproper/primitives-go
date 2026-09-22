package noop_test

import (
	"context"
	"fmt"

	"github.com/primandproper/primitives-go/v2/sms"
	"github.com/primandproper/primitives-go/v2/sms/noop"
)

func ExampleNewSender() {
	sender := noop.NewSender()

	receipt, err := sender.SendSMS(context.Background(), &sms.OutboundSMS{
		To:   "+15558675309",
		From: "+15551112222",
		Body: "Su código de verificación es 123456",
	})

	fmt.Println(err, receipt.ProviderMessageID == "")
	// Output: <nil> true
}
