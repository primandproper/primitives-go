/*
Package mcp puts a Model Context Protocol server behind oauth2server's Verifier:
the resource-server glue an MCP endpoint needs between that package and the MCP
Go SDK.

	verifier, err := oauth2server.NewVerifier(metadata, srv)
	if err != nil {
		return err
	}

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "recipes"}, nil)
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)

	protected, err := oauth2mcp.Protect(verifier, handler, "recipes:read")
	if err != nil {
		return err
	}

	mux.Handle("/mcp", protected)
	mux.Handle("/mcp/", protected)

That is the whole of mounting one. Protect verifies every request with
Verifier.Verify — the token is live, its RFC 8707 audience names this resource,
it carries the scopes asked for — answers a refusal with the challenge whose
resource_metadata parameter a client follows to discover where to get a token,
and serves that document. A tool reads the verified token with
AccessTokenFrom(req.Extra.TokenInfo).

The package is named for the protocol, as the SDK's own is, so a file that
imports both aliases one; the examples here call the SDK's sdkmcp and this one
oauth2mcp.

# Where the dependency lives

The MCP SDK joins go.mod beside the AWS, GCP and other provider SDKs, and like
theirs it sits behind a subpackage: a consumer that never imports this package
never builds it. This is one module, and the SDK goes where every other
provider's does rather than into a module of its own.

# What is easy to get wrong, and is done here

The refusals keep their split. Verify's four refusals are three 401s and a 403;
the SDK's verifier has one error, auth.ErrInvalidToken, which it answers 401.
Protect does not route a scope refusal through it, so a client holding a good
token without a scope is told which scope, rather than to go and get a new
token. TokenVerifier, for a deployment that owns the SDK's middleware itself,
takes no scopes for the same reason: they go in BearerTokenOptions, where the
SDK makes its own 403.

A token with no audience is refused. Verify refuses it and neither adapter
reintroduces a lenient policy: a token minted without a resource indicator is
the token RFC 8707 exists to stop being replayed across resource servers.

The metadata URL is the Verifier's. Both the challenge and the document come
from Verifier.Metadata, so the document a client is pointed at names the same
resource the audience check compares against, and there is no second
configured string for them to disagree through.

# What this is not

Not the tools, not transport selection, and not stdio: a stdio server has no
bearer and no resource, so there is nothing here for it to use. Mounting the
authorization server beside the endpoint is oauth2server.Server.Mount.
*/
package mcp

//platform:transport middleware: Verifier in front of an MCP SDK handler, and the SDK's TokenVerifier over it
