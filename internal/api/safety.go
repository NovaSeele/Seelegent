package api

// Safety DTOs (milestone 12): work at risk, restore, hygiene and scope findings, file overlaps.

// Why a file is at risk.
const (
	// RiskOutsideTools: a file that had uncommitted changes before the turn lost some (or was deleted) without the
	// worker's edit tools touching it.
	RiskOutsideTools = "outside_tools"
	// RiskLargeRemoval: the worker's edit removed at least 5 lines that were uncommitted before the turn.
	RiskLargeRemoval = "large_removal"
)

// RiskFile is one file whose uncommitted work a turn may have lost. Path is relative to the repository root.
type RiskFile struct {
	Path string `json:"path"`
	Why  string `json:"why" ts:"'outside_tools' | 'large_removal'"`
	// LostLines counts the lines that were uncommitted before the turn and are gone after it.
	LostLines int  `json:"lostLines,omitempty"`
	Deleted   bool `json:"deleted,omitempty"`
	Binary    bool `json:"binary,omitempty"`
	// Restored is set once the file was put back with POST /tasks/{id}/turns/{n}/restore.
	Restored bool `json:"restored,omitempty"`
}

// Finding severities.
const (
	SeverityRed  = "red"
	SeverityWarn = "warn"
)

// Finding kinds of the hygiene check and the scope warnings.
const (
	FindingEOLChanged     = "eol_changed"
	FindingBOMChanged     = "bom_changed"
	FindingBOMNewFile     = "bom_new_file"
	FindingJunkFile       = "junk_file"
	FindingUnreported     = "unreported_file"
	FindingOutsideWrite   = "outside_repo_write"
	FindingUnfetchedURL   = "unfetched_url"
	FindingHygieneSkipped = "skipped"

	// FindingOutsideLane: a file outside the lane of a task whose gate is off (the lane is information only).
	FindingOutsideLane = "outside_lane"
)

// Finding is one result of the hygiene check or the scope warnings of a turn.
type Finding struct {
	Kind     string `json:"kind" ts:"'eol_changed' | 'bom_changed' | 'bom_new_file' | 'junk_file' | 'unreported_file' | 'outside_repo_write' | 'unfetched_url' | 'outside_lane' | 'skipped'"`
	Severity string `json:"severity" ts:"'red' | 'warn'"`
	// Path is relative to the repository root (hygiene, outside_lane) or absolute (outside_repo_write); empty for
	// URLs.
	Path string `json:"path,omitempty"`
	// Detail is a short explanation (the command, the URLs).
	Detail string `json:"detail,omitempty"`
}

// Overlap severities.
const (
	OverlapActive = "active"
	OverlapRecent = "recent"
)

// FileOverlap is a file this task and another task of the same repository changed in overlapping time.
type FileOverlap struct {
	// File is relative to the repository root.
	File   string     `json:"file"`
	TaskID string     `json:"taskId"`
	Title  string     `json:"title"`
	Status TaskStatus `json:"status"`
	// Severity is active when both tasks are still working, recent otherwise.
	Severity string `json:"severity" ts:"'active' | 'recent'"`
}

// RestoreRequest is the body of POST /tasks/{id}/turns/{n}/restore.
type RestoreRequest struct {
	// Files are relative to the repository root; empty restores every work-at-risk file of the turn.
	Files []string `json:"files,omitempty"`
	// Force also restores files that changed after the turn ended (skipped as RestoreChangedSinceTurn otherwise).
	Force bool `json:"force,omitempty"`
}

// RestoreResult is the answer of a restore.
type RestoreResult struct {
	Restored []string        `json:"restored"`
	Skipped  []RestoreFailed `json:"skipped"`
	// Backup names the snapshot that keeps what the restored files held before (t<turn>-restore-<time>, in the
	// task's snapshot folder); empty when nothing was written.
	Backup string `json:"backup,omitempty"`
}

// RestoreChangedSinceTurn is the code of a file a restore skipped because someone changed it after the turn
// ended; restoring it anyway needs force.
const RestoreChangedSinceTurn = "changed_since_turn"

// RestoreFailed is a file a restore could not put back.
type RestoreFailed struct {
	Path  string `json:"path"`
	Error string `json:"error"`
	// Code is a stable reason for the clients ("" for other failures).
	Code string `json:"code,omitempty" ts:"'changed_since_turn'"`
}

// ReportRisk is the work_at_risk field of a report: the files, the snapshot that holds their earlier content and
// the command that restores them (Restore for display, RestoreArgv as the exact argument list).
type ReportRisk struct {
	Turn        int              `json:"turn"`
	Snapshot    string           `json:"snapshot"`
	Files       []string         `json:"files"`
	Details     []ReportRiskFile `json:"details"`
	Restore     string           `json:"restore"`
	RestoreArgv []string         `json:"restore_argv"`
}

// ReportRiskFile explains one work-at-risk file of a report.
type ReportRiskFile struct {
	Path      string `json:"path"`
	Why       string `json:"why"`
	LostLines int    `json:"lost_lines,omitempty"`
	Deleted   bool   `json:"deleted,omitempty"`
}

// ReportOverlap is one entry of a report's file_overlaps.
type ReportOverlap struct {
	File     string `json:"file"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Severity string `json:"severity"`
}

// ReportFinding is one hygiene or scope finding of a report.
type ReportFinding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Detail   string `json:"detail,omitempty"`
}
