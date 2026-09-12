package ui

import "context"

// ChangesJobsProvider is the read-only boundary used by the operational UI.
// Implementations must expose already persisted orchestration state and must
// not approve, queue, claim, cancel, retry, execute or otherwise mutate work as
// a side effect of serving a snapshot.
type ChangesJobsProvider interface {
	ChangesJobs(context.Context) (ChangesJobsView, error)
}

type ChangesJobsProviderFunc func(context.Context) (ChangesJobsView, error)

func (fn ChangesJobsProviderFunc) ChangesJobs(ctx context.Context) (ChangesJobsView, error) {
	return fn(ctx)
}
