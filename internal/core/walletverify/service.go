package walletverify

// Service verifies a completed migration by comparing the archived export to the
// loaded Wallet state through the ports in Deps.
type Service struct {
	deps Deps
}

// New returns a Service backed by the given ports.
func New(d Deps) *Service {
	return &Service{deps: d}
}
