package buildinfo

// These values are injected by the container build. Development builds keep
// readable defaults instead of returning empty strings to the UI.
var (
	Version   = "dev"
	Revision  = "unknown"
	BuildTime = "unknown"
)
