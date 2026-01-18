package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// OrphanSessionCheck detects orphaned tmux sessions that don't match
// the expected Gas Town session naming patterns.
type OrphanSessionCheck struct {
	FixableCheck
	sessionLister  SessionLister
	orphanSessions []string // Cached during Run for use in Fix
}

// SessionLister abstracts tmux session listing for testing.
type SessionLister interface {
	ListSessions() ([]string, error)
}

type realSessionLister struct {
	t *tmux.Tmux
}

func (r *realSessionLister) ListSessions() ([]string, error) {
	return r.t.ListSessions()
}

// NewOrphanSessionCheck creates a new orphan session check.
func NewOrphanSessionCheck() *OrphanSessionCheck {
	return &OrphanSessionCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "orphan-sessions",
				CheckDescription: "Detect orphaned tmux sessions",
				CheckCategory:    CategoryCleanup,
			},
		},
	}
}

// NewOrphanSessionCheckWithSessionLister creates a check with a custom session lister (for testing).
func NewOrphanSessionCheckWithSessionLister(lister SessionLister) *OrphanSessionCheck {
	check := NewOrphanSessionCheck()
	check.sessionLister = lister
	return check
}

// Run checks for orphaned Gas Town tmux sessions.
func (c *OrphanSessionCheck) Run(ctx *CheckContext) *CheckResult {
	lister := c.sessionLister
	if lister == nil {
		lister = &realSessionLister{t: tmux.NewTmux()}
	}

	sessions, err := lister.ListSessions()
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not list tmux sessions",
			Details: []string{err.Error()},
		}
	}

	if len(sessions) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No tmux sessions found",
		}
	}

	// Get list of valid rigs
	validRigs := c.getValidRigs(ctx.TownRoot)

	// Get session names for mayor/deacon
	mayorSession := session.MayorSessionName()
	deaconSession := session.DeaconSessionName()

	// Check each session
	var orphans []string
	var validCount int

	for _, sess := range sessions {
		if sess == "" {
			continue
		}

		// Only check gt-* sessions (Gas Town sessions)
		if !strings.HasPrefix(sess, "gt-") {
			continue
		}

		if c.isValidSession(sess, validRigs, mayorSession, deaconSession) {
			validCount++
		} else {
			orphans = append(orphans, sess)
		}
	}

	// Cache orphans for Fix
	c.orphanSessions = orphans

	if len(orphans) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("All %d Gas Town sessions are valid", validCount),
		}
	}

	details := make([]string, len(orphans))
	for i, session := range orphans {
		details[i] = fmt.Sprintf("Orphan: %s", session)
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("Found %d orphaned session(s)", len(orphans)),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to kill orphaned sessions",
	}
}

// Fix kills all orphaned sessions, except crew sessions which are protected.
func (c *OrphanSessionCheck) Fix(ctx *CheckContext) error {
	if len(c.orphanSessions) == 0 {
		return nil
	}

	t := tmux.NewTmux()
	var lastErr error

	for _, sess := range c.orphanSessions {
		// SAFEGUARD: Never auto-kill crew sessions.
		// Crew workers are human-managed and require explicit action.
		if isCrewSession(sess) {
			continue
		}
		// Log pre-death event for crash investigation (before killing)
		_ = events.LogFeed(events.TypeSessionDeath, sess,
			events.SessionDeathPayload(sess, "unknown", "orphan cleanup", "gt doctor"))
		if err := t.KillSession(sess); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// isCrewSession returns true if the session name matches the crew pattern.
// Crew sessions are gt-<rig>-crew-<name> and are protected from auto-cleanup.
func isCrewSession(session string) bool {
	// Pattern: gt-<rig>-crew-<name>
	// Example: gt-gastown-crew-joe
	parts := strings.Split(session, "-")
	if len(parts) >= 4 && parts[0] == "gt" && parts[2] == "crew" {
		return true
	}
	return false
}

// getValidRigs returns a list of valid rig names from the workspace.
func (c *OrphanSessionCheck) getValidRigs(townRoot string) []string {
	var rigs []string

	// Read rigs.json if it exists
	rigsPath := filepath.Join(townRoot, "mayor", "rigs.json")
	if _, err := os.Stat(rigsPath); err == nil {
		// For simplicity, just scan directories at town root that look like rigs
		entries, err := os.ReadDir(townRoot)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != "mayor" && entry.Name() != ".beads" && !strings.HasPrefix(entry.Name(), ".") {
					// Check if it looks like a rig (has polecats/ or crew/ directory)
					polecatsDir := filepath.Join(townRoot, entry.Name(), "polecats")
					crewDir := filepath.Join(townRoot, entry.Name(), "crew")
					if _, err := os.Stat(polecatsDir); err == nil {
						rigs = append(rigs, entry.Name())
					} else if _, err := os.Stat(crewDir); err == nil {
						rigs = append(rigs, entry.Name())
					}
				}
			}
		}
	}

	return rigs
}

// isValidSession checks if a session name matches expected Gas Town patterns.
// Valid patterns:
//   - gt-{town}-mayor (dynamic based on town name)
//   - gt-{town}-deacon (dynamic based on town name)
//   - gt-<rig>-witness
//   - gt-<rig>-refinery
//   - gt-<rig>-<polecat> (where polecat is any name)
//
// Note: We can't verify polecat names without reading state, so we're permissive.
func (c *OrphanSessionCheck) isValidSession(sess string, validRigs []string, mayorSession, deaconSession string) bool {
	// Mayor session is always valid (dynamic name based on town)
	if mayorSession != "" && sess == mayorSession {
		return true
	}

	// Deacon session is always valid (dynamic name based on town)
	if deaconSession != "" && sess == deaconSession {
		return true
	}

	// For rig-specific sessions, extract rig name
	// Pattern: gt-<rig>-<role>
	parts := strings.SplitN(sess, "-", 3)
	if len(parts) < 3 {
		// Invalid format - must be gt-<rig>-<something>
		return false
	}

	rigName := parts[1]

	// Check if this rig exists
	rigFound := false
	for _, r := range validRigs {
		if r == rigName {
			rigFound = true
			break
		}
	}

	if !rigFound {
		// Unknown rig - this is an orphan
		return false
	}

	role := parts[2]

	// witness and refinery are valid roles
	if role == "witness" || role == "refinery" {
		return true
	}

	// Any other name is assumed to be a polecat or crew member
	// We can't easily verify without reading state, so accept it
	return true
}

// OrphanProcessCheck detects runtime processes that are not
// running inside a tmux session. It distinguishes Gas Town processes
// (which have --dangerously-skip-permissions flag) from personal sessions.
// Only orphaned Gas Town processes are auto-fixed; personal sessions are protected.
type OrphanProcessCheck struct {
	FixableCheck
	orphanProcesses []processInfo // Cached during Run for use in Fix
}

// NewOrphanProcessCheck creates a new orphan process check.
func NewOrphanProcessCheck() *OrphanProcessCheck {
	return &OrphanProcessCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "orphan-processes",
				CheckDescription: "Detect Gas Town processes outside tmux",
				CheckCategory:    CategoryCleanup,
			},
		},
	}
}

// Run checks for runtime processes running outside tmux.
func (c *OrphanProcessCheck) Run(ctx *CheckContext) *CheckResult {
	// Get list of tmux session PIDs
	tmuxPIDs, err := c.getTmuxSessionPIDs()
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not get tmux session info",
			Details: []string{err.Error()},
		}
	}

	// Find runtime processes
	runtimeProcs, err := c.findRuntimeProcesses()
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not list runtime processes",
			Details: []string{err.Error()},
		}
	}

	if len(runtimeProcs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No runtime processes found",
		}
	}

	// Categorize processes:
	// - insideTmux: processes with tmux ancestor (safe)
	// - orphanedGasTown: Gas Town processes without tmux ancestor (can fix)
	// - orphanedPersonal: personal sessions without tmux (informational only)
	var insideTmux int
	var orphanedGasTown []processInfo
	var orphanedPersonal []processInfo

	for _, proc := range runtimeProcs {
		if c.isOrphanProcess(proc, tmuxPIDs) {
			if proc.isGasTown {
				orphanedGasTown = append(orphanedGasTown, proc)
			} else {
				orphanedPersonal = append(orphanedPersonal, proc)
			}
		} else {
			insideTmux++
		}
	}

	// Cache orphaned Gas Town processes for Fix
	c.orphanProcesses = orphanedGasTown

	// No orphans at all
	if len(orphanedGasTown) == 0 && len(orphanedPersonal) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("All %d runtime processes are inside tmux", insideTmux),
		}
	}

	// Build details
	details := make([]string, 0)

	if len(orphanedGasTown) > 0 {
		details = append(details, fmt.Sprintf("Orphaned Gas Town processes (%d):", len(orphanedGasTown)))
		for _, proc := range orphanedGasTown {
			details = append(details, fmt.Sprintf("  PID %d: %s (will be killed with --fix)", proc.pid, proc.cmd))
		}
	}

	if len(orphanedPersonal) > 0 {
		if len(orphanedGasTown) > 0 {
			details = append(details, "")
		}
		details = append(details, fmt.Sprintf("Personal Claude sessions (%d) - PROTECTED:", len(orphanedPersonal)))
		for _, proc := range orphanedPersonal {
			details = append(details, fmt.Sprintf("  PID %d: %s (personal session, not auto-fixed)", proc.pid, proc.cmd))
		}
	}

	// Only fail if there are Gas Town orphans to fix
	status := StatusOK
	if len(orphanedGasTown) > 0 {
		status = StatusWarning
	}

	msg := fmt.Sprintf("Found %d orphaned Gas Town process(es)", len(orphanedGasTown))
	if len(orphanedPersonal) > 0 {
		msg += fmt.Sprintf(", %d personal session(s) protected", len(orphanedPersonal))
	}

	result := &CheckResult{
		Name:    c.Name(),
		Status:  status,
		Message: msg,
		Details: details,
	}

	if len(orphanedGasTown) > 0 {
		result.FixHint = "Run 'gt doctor --fix' to kill orphaned Gas Town processes"
	}

	return result
}

// Fix kills orphaned Gas Town processes.
// Only processes with --dangerously-skip-permissions are killed.
// Personal Claude sessions (without the flag) are protected.
func (c *OrphanProcessCheck) Fix(ctx *CheckContext) error {
	if len(c.orphanProcesses) == 0 {
		return nil
	}

	var lastErr error

	for _, proc := range c.orphanProcesses {
		// SAFEGUARD: Double-check this is a Gas Town process
		if !proc.isGasTown {
			continue
		}

		// Log pre-death event for crash investigation
		_ = events.LogFeed(events.TypeSessionDeath, fmt.Sprintf("pid-%d", proc.pid),
			events.SessionDeathPayload(fmt.Sprintf("pid-%d", proc.pid), "unknown", "orphan process cleanup", "gt doctor"))

		// Kill the process
		if err := exec.Command("kill", fmt.Sprintf("%d", proc.pid)).Run(); err != nil { //nolint:gosec // G204: PID is from internal process scan
			lastErr = err
		}
	}

	return lastErr
}

type processInfo struct {
	pid       int
	ppid      int
	cmd       string
	args      string // Full command line arguments
	isGasTown bool   // Has --dangerously-skip-permissions flag
}

// getTmuxSessionPIDs returns PIDs of all tmux server processes and pane shell PIDs.
func (c *OrphanProcessCheck) getTmuxSessionPIDs() (map[int]bool, error) { //nolint:unparam // error return kept for future use
	// Get tmux server PID and all pane PIDs
	pids := make(map[int]bool)

	// Find tmux server processes using ps instead of pgrep.
	// pgrep -x tmux is unreliable on macOS - it often misses the actual server.
	// We use ps with awk to find processes where comm is exactly "tmux".
	out, err := exec.Command("sh", "-c", `ps ax -o pid,comm | awk '$2 == "tmux" || $2 ~ /\/tmux$/ { print $1 }'`).Output()
	if err != nil {
		// No tmux server running
		return pids, nil
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
			pids[pid] = true
		}
	}

	// Also get shell PIDs inside tmux panes
	t := tmux.NewTmux()
	sessions, _ := t.ListSessions()
	for _, session := range sessions {
		// Get pane PIDs for this session
		out, err := exec.Command("tmux", "list-panes", "-t", session, "-F", "#{pane_pid}").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var pid int
			if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
				pids[pid] = true
			}
		}
	}

	return pids, nil
}

// findRuntimeProcesses finds all running runtime CLI processes.
// Identifies Gas Town processes by the --dangerously-skip-permissions flag.
// Excludes Claude.app desktop application and its helpers.
func (c *OrphanProcessCheck) findRuntimeProcesses() ([]processInfo, error) {
	var procs []processInfo

	// Use ps to find runtime processes with full command line
	// -e: all processes, -o: output format with pid, ppid, comm, and args
	out, err := exec.Command("ps", "-eo", "pid,ppid,comm,args").Output()
	if err != nil {
		return nil, err
	}

	// Pattern to exclude Claude.app and related desktop processes
	excludePattern := regexp.MustCompile(`(?i)(Claude\.app|claude-native|chrome-native)`)

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		var pid, ppid int
		if _, err := fmt.Sscanf(fields[0], "%d", &pid); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(fields[1], "%d", &ppid); err != nil {
			continue
		}

		// comm is just the executable name (no path, no args)
		comm := fields[2]

		// Only match actual claude/codex executables, not tmux launchers
		// This prevents matching "tmux new-session ... && claude ..."
		if comm != "claude" && comm != "claude-code" && comm != "codex" {
			continue
		}

		// args is the full command line
		args := strings.Join(fields[3:], " ")

		// Skip desktop app processes
		if excludePattern.MatchString(args) {
			continue
		}

		// Gas Town processes have --dangerously-skip-permissions flag
		isGasTown := strings.Contains(args, "--dangerously-skip-permissions")

		procs = append(procs, processInfo{
			pid:       pid,
			ppid:      ppid,
			cmd:       comm,
			args:      args,
			isGasTown: isGasTown,
		})
	}

	return procs, nil
}

// isOrphanProcess checks if a runtime process is orphaned.
// A process is orphaned if its parent (or ancestor) is not a tmux session.
func (c *OrphanProcessCheck) isOrphanProcess(proc processInfo, tmuxPIDs map[int]bool) bool {
	// Walk up the process tree looking for a tmux parent
	currentPPID := proc.ppid
	visited := make(map[int]bool)

	for currentPPID > 1 && !visited[currentPPID] {
		visited[currentPPID] = true

		// Check if this is a tmux process
		if tmuxPIDs[currentPPID] {
			return false // Has tmux ancestor, not orphaned
		}

		// Get parent's parent
		out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", currentPPID), "-o", "ppid=").Output() //nolint:gosec // G204: PID is numeric from internal state
		if err != nil {
			break
		}

		var nextPPID int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &nextPPID); err != nil {
			break
		}
		currentPPID = nextPPID
	}

	return true // No tmux ancestor found
}
