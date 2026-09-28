/*
Package grpc guards gRPC services with oauth2server's Verifier: the resource
server's half of that package, over metadata instead of an HTTP header.

	verifier, err := oauth2server.NewVerifier(metadata, srv)
	if err != nil {
		return err
	}

	unary, err := oauth2grpc.NewUnaryServerInterceptor(verifier, "recipes:read")
	if err != nil {
		return err
	}

	stream, err := oauth2grpc.NewStreamServerInterceptor(verifier, "recipes:read")
	if err != nil {
		return err
	}

The interceptors read the bearer credential from the authorization metadata
entry, hand it to Verifier.Verify, and put the token that comes back on the
context, where a handler reads it with oauth2server.TokenFromContext. That is the
whole of Verifier.Middleware, and it is the same three checks: the token is live,
its RFC 8707 audience names this resource, and it carries the scopes asked for.
The audience check is the reason to use this rather than calling
Server.Authenticate by hand — see oauth2server.Verifier for why that one gets
left out.

# What a refused call gets back

Unauthenticated for a call with no credential, a credential that is not live,
or a live one minted for a different resource: each is answered by presenting a
better credential. PermissionDenied for a live token, for this resource, that
lacks a scope — the one refusal a better credential for the same grant will not
fix. Internal for a store that broke, which is not the caller's doing.

Those codes come from oauth2server.GRPCMapper, consulted directly, so they are
right whether or not a composition root has registered it. Registering it is
still worth doing: an error-encoding interceptor re-maps the chain it is handed
through the registered mappers, and a handler that verifies for itself and
returns what it got should map the same way.

There is no WWW-Authenticate. RFC 9728's discovery pointer is an HTTP header,
gRPC has no counterpart a client library knows to read, and a client of a gRPC
resource server has its credentials configured rather than discovered.
*/
package grpc

//platform:transport middleware: the same, as interceptors
