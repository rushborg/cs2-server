// Package perf bundles the host-performance bootstrap script into the
// agent binary so it can apply CS2 server tuning (CPU governor, THP,
// swap, sudoers) on its own without requiring the operator to copy
// shell scripts to each host.
package perf

import _ "embed"

// HostPerfScript is the bootstrap-host-perf.sh shipped with this build.
// Written to disk and executed via sudo at agent startup (when missing
// the .host-perf-applied marker) and on demand via apply_host_perf.
//
//go:embed host-perf.sh
var HostPerfScript string
