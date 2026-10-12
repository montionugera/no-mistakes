package git

import (
	"context"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/safeurl"
)

// ResolveUpstreamURL prefers the configured origin when its registration is
// unverified or its redacted value matches the persisted upstream. This keeps
// credentials in the local Git configuration instead of requiring them in the
// repository record. A freshly verified registration wins when it differs.
func ResolveUpstreamURL(ctx context.Context, dir, registeredURL string, verified bool) string {
	if originURL, err := GetRemoteURL(ctx, dir, "origin"); err == nil && strings.TrimSpace(originURL) != "" {
		if !verified || safeurl.Redact(originURL) == registeredURL {
			return originURL
		}
	}
	return registeredURL
}

// ResolvePushURL uses the configured fork when one exists. Otherwise it uses
// the same credential-aware upstream selection as ResolveUpstreamURL.
func ResolvePushURL(ctx context.Context, dir, upstreamURL, forkURL string, verified bool) string {
	if strings.TrimSpace(forkURL) != "" {
		return forkURL
	}
	return ResolveUpstreamURL(ctx, dir, upstreamURL, verified)
}
