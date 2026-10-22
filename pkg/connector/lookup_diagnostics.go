package connector

import (
	"context"
	"strings"

	"github.com/rs/xid"

	"github.com/beeper/line/pkg/line"
)

func (lc *LineClient) lookupDiagnosticContext(ctx context.Context, source, identifier string) context.Context {
	if line.HasLookupDiagnostics(ctx) || lc.UserLogin == nil || lc.UserLogin.Bridge == nil {
		return ctx
	}
	connector, ok := lc.UserLogin.Bridge.Network.(*LineConnector)
	if !ok || !connector.Config.LookupDiagnostics {
		return ctx
	}
	category := "public_id_or_name"
	if isCanonicalUserMID(normalizeUserMID(strings.TrimSpace(identifier))) {
		category = "user_mid"
	}
	// A fresh request ID correlates stages without logging or hashing the user's
	// identifier. Info level is intentional: no global debug logging is needed.
	log := lc.UserLogin.Bridge.Log.With().Str("component", "lookup_diagnostic").
		Str("lookup_trace", xid.New().String()).Str("lookup_source", source).
		Str("identifier_category", category).Logger()
	return line.WithLookupDiagnostics(ctx, func(diagnostic line.LookupDiagnostic) {
		log.Info().Interface("lookup", diagnostic).Msg("LINE lookup diagnostic")
	})
}

func lookupDiagnostic(ctx context.Context, operation, stage, outcome string, count int) {
	line.RecordLookupDiagnostic(ctx, line.LookupDiagnostic{Operation: operation, Stage: stage, Outcome: outcome, Count: count})
}
