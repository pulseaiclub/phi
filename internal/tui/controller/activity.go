package controller

// Activity mirrors session status for the composer status slot (driven by the stream pipeline).
type Activity int

// Activity values map to status-slot messages shown while the pipeline runs.
const (
	ActivityIdle Activity = iota
	ActivitySubmitting
	ActivityWaiting
	ActivityStreaming
	ActivityTools
	ActivityRetrying
	ActivityCancelled
	ActivityCompacting
	ActivityAwaitingApproval
)
