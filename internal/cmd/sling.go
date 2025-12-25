package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/wisp"
)

var slingCmd = &cobra.Command{
	Use:   "sling <bead-id> [target]",
	Short: "Hook work and start immediately (no restart)",
	Long: `Sling work onto an agent's hook and start working immediately.

Unlike 'gt handoff', sling does NOT restart the session. It:
  1. Attaches the bead to the hook (durability)
  2. Injects a prompt to start working NOW

This preserves current context while kicking off work. Use when:
  - You've been chatting with an agent and want to kick off a workflow
  - You want to assign work to another agent that has useful context
  - You (Overseer) want to start work then attend to another window

The hook provides durability - the agent can restart, compact, or hand off,
but until the hook is changed or closed, that agent owns the work.

Examples:
  gt sling gt-abc                       # Hook and start on it now
  gt sling gt-abc -s "Fix the bug"      # With context subject
  gt sling gt-abc crew                  # Sling to crew worker
  gt sling gt-abc gastown/crew/max      # Sling to specific agent

Compare:
  gt hook <bead>      # Just attach (no action)
  gt sling <bead>     # Attach + start now (keep context)
  gt handoff <bead>   # Attach + restart (fresh context)

The propulsion principle: if it's on your hook, YOU RUN IT.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSling,
}

var (
	slingSubject string
	slingMessage string
	slingDryRun  bool
)

func init() {
	slingCmd.Flags().StringVarP(&slingSubject, "subject", "s", "", "Context subject for the work")
	slingCmd.Flags().StringVarP(&slingMessage, "message", "m", "", "Context message for the work")
	slingCmd.Flags().BoolVarP(&slingDryRun, "dry-run", "n", false, "Show what would be done")
	rootCmd.AddCommand(slingCmd)
}

func runSling(cmd *cobra.Command, args []string) error {
	beadID := args[0]

	// Polecats cannot sling - check early before writing anything
	if polecatName := os.Getenv("GT_POLECAT"); polecatName != "" {
		return fmt.Errorf("polecats cannot sling (use gt done for handoff)")
	}

	// Verify the bead exists
	if err := verifyBeadExists(beadID); err != nil {
		return err
	}

	// Determine target agent (self or specified)
	var targetAgent string
	var targetPane string
	var hookRoot string // Where to store the hook (role's home)
	var err error

	if len(args) > 1 {
		// Slinging to another agent
		targetAgent, targetPane, err = resolveTargetAgent(args[1])
		if err != nil {
			return fmt.Errorf("resolving target: %w", err)
		}
		// For remote targets, use their home directory
		// TODO: resolve target's home properly
		hookRoot, err = detectCloneRoot()
		if err != nil {
			return fmt.Errorf("detecting clone root: %w", err)
		}
	} else {
		// Slinging to self - use env-aware role detection
		roleInfo, err := GetRole()
		if err != nil {
			return fmt.Errorf("detecting role: %w", err)
		}
		// Build agent identity from role
		switch roleInfo.Role {
		case RoleMayor:
			targetAgent = "mayor"
		case RoleDeacon:
			targetAgent = "deacon"
		case RoleWitness:
			targetAgent = fmt.Sprintf("%s/witness", roleInfo.Rig)
		case RoleRefinery:
			targetAgent = fmt.Sprintf("%s/refinery", roleInfo.Rig)
		case RolePolecat:
			targetAgent = fmt.Sprintf("%s/polecats/%s", roleInfo.Rig, roleInfo.Polecat)
		case RoleCrew:
			targetAgent = fmt.Sprintf("%s/crew/%s", roleInfo.Rig, roleInfo.Polecat)
		default:
			return fmt.Errorf("cannot determine agent identity (role: %s)", roleInfo.Role)
		}
		targetPane = os.Getenv("TMUX_PANE")
		// Use role's home for hook storage
		hookRoot = roleInfo.Home
		if hookRoot == "" {
			// Fallback to git root if home not determined
			hookRoot, err = detectCloneRoot()
			if err != nil {
				return fmt.Errorf("detecting clone root: %w", err)
			}
		}
	}

	// Create the slung work wisp
	sw := wisp.NewSlungWork(beadID, targetAgent)
	sw.Subject = slingSubject
	sw.Context = slingMessage

	fmt.Printf("%s Slinging %s to %s...\n", style.Bold.Render("🎯"), beadID, targetAgent)

	if slingDryRun {
		fmt.Printf("Would create wisp: %s\n", wisp.HookPath(hookRoot, targetAgent))
		fmt.Printf("  bead_id: %s\n", beadID)
		fmt.Printf("  agent: %s\n", targetAgent)
		fmt.Printf("  hook_root: %s\n", hookRoot)
		if slingSubject != "" {
			fmt.Printf("  subject: %s\n", slingSubject)
		}
		if slingMessage != "" {
			fmt.Printf("  context: %s\n", slingMessage)
		}
		fmt.Printf("Would inject start prompt to pane: %s\n", targetPane)
		return nil
	}

	// Write the wisp to the hook
	if err := wisp.WriteSlungWork(hookRoot, targetAgent, sw); err != nil {
		return fmt.Errorf("writing wisp: %w", err)
	}

	fmt.Printf("%s Work attached to hook\n", style.Bold.Render("✓"))

	// Inject the "start now" prompt
	if err := injectStartPrompt(targetPane, beadID, slingSubject); err != nil {
		return fmt.Errorf("injecting start prompt: %w", err)
	}

	fmt.Printf("%s Start prompt sent\n", style.Bold.Render("▶"))
	return nil
}

// injectStartPrompt sends a prompt to the target pane to start working.
// Uses the reliable nudge pattern: literal mode + 500ms debounce + separate Enter.
func injectStartPrompt(pane, beadID, subject string) error {
	if pane == "" {
		return fmt.Errorf("no target pane")
	}

	// Build the prompt to inject
	var prompt string
	if subject != "" {
		prompt = fmt.Sprintf("Work slung: %s (%s). Start working on it now - no questions, just begin.", beadID, subject)
	} else {
		prompt = fmt.Sprintf("Work slung: %s. Start working on it now - run `gt mol status` to see the hook, then begin.", beadID)
	}

	// Use the reliable nudge pattern (same as gt nudge / tmux.NudgeSession)
	t := tmux.NewTmux()
	return t.NudgePane(pane, prompt)
}

// resolveTargetAgent converts a target spec to agent ID and pane.
func resolveTargetAgent(target string) (agentID string, pane string, err error) {
	// First resolve to session name
	sessionName, err := resolveRoleToSession(target)
	if err != nil {
		return "", "", err
	}

	// Get the pane for that session
	pane, err = getSessionPane(sessionName)
	if err != nil {
		return "", "", fmt.Errorf("getting pane for %s: %w", sessionName, err)
	}

	// Convert session name back to agent ID format
	agentID = sessionToAgentID(sessionName)
	return agentID, pane, nil
}

// sessionToAgentID converts a session name to agent ID format.
func sessionToAgentID(session string) string {
	switch {
	case session == "gt-mayor":
		return "mayor"
	case session == "gt-deacon":
		return "deacon"
	case strings.Contains(session, "-crew-"):
		// gt-gastown-crew-max -> gastown/crew/max
		parts := strings.Split(session, "-")
		for i, p := range parts {
			if p == "crew" && i > 1 && i < len(parts)-1 {
				rig := strings.Join(parts[1:i], "-")
				name := strings.Join(parts[i+1:], "-")
				return fmt.Sprintf("%s/crew/%s", rig, name)
			}
		}
	case strings.HasSuffix(session, "-witness"):
		rig := strings.TrimPrefix(session, "gt-")
		rig = strings.TrimSuffix(rig, "-witness")
		return fmt.Sprintf("%s/witness", rig)
	case strings.HasSuffix(session, "-refinery"):
		rig := strings.TrimPrefix(session, "gt-")
		rig = strings.TrimSuffix(rig, "-refinery")
		return fmt.Sprintf("%s/refinery", rig)
	}
	return session
}

// verifyBeadExists checks that the bead exists using bd show.
func verifyBeadExists(beadID string) error {
	cmd := exec.Command("bd", "show", beadID, "--json")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bead '%s' not found (bd show failed)", beadID)
	}
	return nil
}

// detectAgentIdentity figures out who we are (crew/joe, witness, etc).
func detectAgentIdentity() (string, error) {
	// Check environment first
	if crew := os.Getenv("GT_CREW"); crew != "" {
		if rig := os.Getenv("GT_RIG"); rig != "" {
			return fmt.Sprintf("%s/crew/%s", rig, crew), nil
		}
	}

	// Check if we're a polecat
	if polecat := os.Getenv("GT_POLECAT"); polecat != "" {
		if rig := os.Getenv("GT_RIG"); rig != "" {
			return fmt.Sprintf("%s/polecats/%s", rig, polecat), nil
		}
	}

	// Try to detect from cwd
	detected, err := detectCrewFromCwd()
	if err == nil {
		return fmt.Sprintf("%s/crew/%s", detected.rigName, detected.crewName), nil
	}

	// Check for other role markers in session name
	if session := os.Getenv("TMUX"); session != "" {
		sessionName, err := getCurrentTmuxSession()
		if err == nil {
			if sessionName == "gt-mayor" {
				return "mayor", nil
			}
			if sessionName == "gt-deacon" {
				return "deacon", nil
			}
			if strings.HasSuffix(sessionName, "-witness") {
				rig := strings.TrimSuffix(strings.TrimPrefix(sessionName, "gt-"), "-witness")
				return fmt.Sprintf("%s/witness", rig), nil
			}
			if strings.HasSuffix(sessionName, "-refinery") {
				rig := strings.TrimSuffix(strings.TrimPrefix(sessionName, "gt-"), "-refinery")
				return fmt.Sprintf("%s/refinery", rig), nil
			}
		}
	}

	return "", fmt.Errorf("cannot determine agent identity - set GT_RIG/GT_CREW or run from clone directory")
}

// detectCloneRoot finds the root of the current git clone.
func detectCloneRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}
