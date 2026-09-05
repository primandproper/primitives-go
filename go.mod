module github.com/primandproper/primitives-go

go 1.27

// The toolchain, not the library's dependencies: gci, goimports, betteralign
// and tagalign are what `make format` runs, moq is what `make generate` runs,
// and protoc-gen-go is the plugin `make proto` builds. Every requirement below
// is one of these six pulling its own graph in — this module requires nothing
// of its own yet.
tool (
	github.com/4meepo/tagalign/cmd/tagalign
	github.com/daixiang0/gci
	github.com/dkorunic/betteralign/cmd/betteralign
	github.com/matryer/moq
	golang.org/x/tools/cmd/goimports
	google.golang.org/protobuf/cmd/protoc-gen-go
)

require (
	github.com/4meepo/tagalign v1.4.3 // indirect
	github.com/KimMachineGun/automemlimit v0.7.5 // indirect
	github.com/alfatraining/structtag v1.0.0 // indirect
	github.com/daixiang0/gci v0.14.0 // indirect
	github.com/dkorunic/betteralign v0.14.3 // indirect
	github.com/google/renameio/v2 v2.0.2 // indirect
	github.com/hexops/gotextdiff v1.0.3 // indirect
	github.com/inconshreveable/mousetrap v1.0.1 // indirect
	github.com/matryer/moq v0.7.1 // indirect
	github.com/pbnjay/memory v0.0.0-20210728143218-7b4eea64cf58 // indirect
	github.com/spf13/cobra v1.6.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	go.uber.org/zap v1.24.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260708182218-49f421fb7959 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
