package noop_test

import (
	"fmt"

	"github.com/primandproper/primitives-go/v2/circuitbreaking/noop"
)

func ExampleNewCircuitBreaker() {
	cb := noop.NewCircuitBreaker()

	fmt.Println(cb.CanProceed())

	cb.Failed()
	fmt.Println(cb.CanProceed())
	// Output:
	// true
	// true
}
