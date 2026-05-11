// service_runs.go: run lifecycle (start/poll/cancel/list runs for canvas + apps).
//
// Reserved slot in the service_*.go split. The current pkg/project Store has
// no run-lifecycle methods to cut over from service.go — runs are handled
// outside this package today. This file exists so future run methods land in
// the obvious place without re-splitting service.go again.
package project
