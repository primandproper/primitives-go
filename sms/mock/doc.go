// Package smsmock provides moq-generated mock implementations of the sms
// package's interfaces.
package smsmock

// Regenerate the moq mocks via `go generate ./sms/mock/`.

//go:generate go tool github.com/matryer/moq -out sender_mock.go -pkg smsmock -rm -fmt goimports .. Sender:SenderMock
