package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// confirm asks the user to approve a destructive action.
//
// assumeYes short-circuits it. When stdin is not a terminal there is nobody to
// ask, so the action is refused rather than silently performed — a script that
// wants it must pass --yes.
func confirm(g *globals, prompt string, assumeYes bool) error {
	if assumeYes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return usagef("%s\nPass --yes to proceed without confirmation.", prompt)
	}

	fmt.Fprintf(g.stderr, "%s [y/N]: ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("could not read the confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("cancelled")
	}
}
