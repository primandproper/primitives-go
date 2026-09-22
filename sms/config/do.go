package smscfg

import (
	"context"
	"net/http"

	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/sms"

	"github.com/samber/do/v2"
)

// RegisterSender registers an sms.Sender with the injector.
func RegisterSender(i do.Injector) {
	do.Provide(i, func(i do.Injector) (sms.Sender, error) {
		pillars, err := observability.InvokePillars(i)
		if err != nil {
			return nil, err
		}

		return NewSender(
			do.MustInvoke[context.Context](i),
			do.MustInvoke[*Config](i),
			do.MustInvoke[*http.Client](i),
			WithPillars(pillars),
		)
	})
}
