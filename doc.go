// Package primitives is the root of the module and holds no code. It exists so
// that the module documents itself at its own import path, and so that
// `go build ./...` and `go test ./...` have one thing to be pointed at.
//
// The packages live one directory down, each named for the concern it covers.
// Two sit one level deeper than that — notifications/mobile and
// webhooks/inbound — because the package they are nested under owns a table and
// stayed in platform-go. They kept the import paths they arrived with, so this
// module has a notifications directory and a webhooks directory that are not
// themselves packages.
//
// # What belongs here
//
// This module ships what every service is built from and no service is. Four
// kinds of thing qualify:
//
//   - a provider behind an interface — cache, email, messagequeue, secrets;
//   - a transport whose shape is decided by something other than the consumer's
//     domain — a probe, a protocol, a middleware contract, a third party's
//     payload;
//   - the database and schema tooling stores are built with — database and its
//     subpackages, filtering;
//   - the cross-cutting values both tiers have to agree on — tenancy.Scope, the
//     errors sentinels, clock.
//
// Nothing in it owns a table. A package that owns a noun with a table — its
// lifecycle, its transport, its permissions and its privacy obligations — is
// [github.com/primandproper/platform-go]'s. The test for a new package is
// whether an application with no users would still need it. If yes, it is a
// primitive and it belongs here.
//
// The dependency runs one way: platform-go imports this module, and this module
// imports platform-go from nowhere, ever.
package primitives
