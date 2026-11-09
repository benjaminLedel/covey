package httpapi

import "covey/internal/netguard"

// The provider doubles in this package's tests listen on loopback; allowed the
// way an operator with a local demo double allows it (#692).
func init() { _ = netguard.SetAllow("127.0.0.0/8,::1") }
