// Package noop is the sms.Sender that sends nothing. SendSMS accepts every
// message and returns an empty receipt, so verification codes, appointment
// reminders, and every reply are addressed, rendered, and dropped — with no
// carrier-side log, no provider message ID, and no error for anyone to notice.
//
// The absence of a signal is what makes it a test and local-development choice
// and a hazard anywhere else. A deployment that reaches this by accident reports
// itself healthy while every person waiting on a code is stuck, and nothing in
// the metrics distinguishes that from a quiet week. sms/config builds it only
// for the "noop" provider name; an unrecognized name is
// errors.ErrUnknownProvider.
//
// The receipt it returns carries an empty ProviderMessageID, which is the honest
// answer: no provider accepted the message, so no provider named it. A consumer
// that keys a status callback or a metering row on that ID gets an empty string
// rather than a plausible-looking one, which is the difference between a test
// that notices and a test that passes.
package noop

import (
	"context"

	"github.com/primandproper/primitives-go/v2/sms"
)

var _ sms.Sender = (*Sender)(nil)

// Sender is a no-op sms.Sender. It is exported, and returned by NewSender, so a
// caller who has chosen to send nothing depends on that choice rather than on
// the interface every provider shares.
type Sender struct{}

// NewSender returns a Sender that accepts every message and delivers none.
//
// It cannot fail, and so returns no error: there is no credential to check, no
// client to build, and nothing for a caller to branch on.
func NewSender() *Sender {
	return &Sender{}
}

// SendSMS is a no-op.
func (*Sender) SendSMS(context.Context, *sms.OutboundSMS) (*sms.Receipt, error) {
	return &sms.Receipt{}, nil
}
