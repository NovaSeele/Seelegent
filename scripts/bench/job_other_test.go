//go:build bench && !windows

package bench

import "errors"

// treeJob accounts the whole tree on Windows only; elsewhere the pid scan's numbers stand alone.
type treeJob struct{}

func newTreeJob(int) (*treeJob, error) {
	return nil, errors.New("job accounting is implemented on Windows only")
}

func (*treeJob) stats() (jobStat, error) { return jobStat{}, errors.New("no job") }

func (*treeJob) close() {}
