package mcpserver

import (
	"context"
	"net/http"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
)

// tokenVerifier returns a TokenVerifier closing over the sextant CA. The
// verifier is wired into auth.RequireBearerToken middleware so the SDK
// rejects unauthenticated/expired/invalid-signature requests at the
// HTTP layer — before the JSON-RPC body is parsed.
//
// The verifier embeds the parsed sextant Claims inside TokenInfo.Extra
// under the key "sextant_claims" so the tool dispatcher (server.go) can
// build a Caller without re-verifying the token.
func tokenVerifier(ca *authjwt.CA) mcpauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		claims, err := ca.Verify(token)
		if err != nil {
			// Wrap so the SDK middleware emits 401 Unauthorized rather
			// than 500.
			return nil, mcpauth.ErrInvalidToken
		}
		return &mcpauth.TokenInfo{
			Scopes:     append([]string(nil), claims.Capabilities...),
			Expiration: claims.ExpiresAt,
			// UserID prevents session hijacking inside the SDK: once the
			// session is created with this ID, subsequent requests on the
			// same session must present a token resolving to the same
			// UserID. We use the incarnation ID (per-incarnation token)
			// so a token re-issue after a restart cannot inherit an old
			// session.
			UserID: claims.IncarnationID.String(),
			Extra: map[string]any{
				claimsExtraKey: claims,
			},
		}, nil
	}
}

// claimsExtraKey is the TokenInfo.Extra map key under which we stash the
// parsed Claims so the tool dispatcher avoids a second verify pass.
const claimsExtraKey = "sextant_claims"

// callerFromTokenInfo materializes a Caller from the SDK-attached
// TokenInfo. Returns the operator caller when ti is nil (the stdio
// transport path leaves TokenInfo unset, which is the operator-trusted
// surface).
func callerFromTokenInfo(ti *mcpauth.TokenInfo) Caller {
	if ti == nil {
		return Caller{Kind: CallerOperator}
	}
	claims, ok := ti.Extra[claimsExtraKey].(authjwt.Claims)
	if !ok {
		// Defensive: if some middleware bypass produced a TokenInfo we
		// didn't mint, treat it as agent-with-no-caps rather than
		// silently elevating to operator.
		return Caller{Kind: CallerAgent, Capabilities: append([]string(nil), ti.Scopes...)}
	}
	return Caller{
		Kind:          CallerAgent,
		AgentUUID:     claims.AgentUUID,
		IncarnationID: claims.IncarnationID,
		Capabilities:  append([]string(nil), claims.Capabilities...),
	}
}
