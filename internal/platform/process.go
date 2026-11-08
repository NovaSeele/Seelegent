package platform

import "context"

// Process is a running process: its id, its executable's file name (such as node.exe) and its command line.
// CommandLine is "" and Args nil when the command line could not be read (a process of another user, or an
// elevated one).
type Process struct {
	PID         int
	Name        string
	CommandLine string
	// Args is CommandLine split the way the C runtime splits it (the program first).
	Args []string
}

// ListProcesses lists the running processes whose executable file name match accepts (nil accepts every one),
// with their command lines. It only reads. Only Windows is supported: elsewhere it returns an error matching
// errors.ErrUnsupported.
func ListProcesses(ctx context.Context, match func(name string) bool) ([]Process, error) {
	return listProcesses(ctx, match)
}
