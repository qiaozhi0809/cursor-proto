package batch

import "time"

// defaultRefreshLead matches the convention documented in
// docs/phase-8c-batch-tooling.md: try to refresh 30 minutes before an
// access token's exp claim.
const defaultRefreshLead = 30 * time.Minute

// nowUTC exists so tests can override it via a build-tag file. Kept as an
// unexported variable to make replacement trivial.
var nowUTC = func() time.Time { return time.Now().UTC() }
