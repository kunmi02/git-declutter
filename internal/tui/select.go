package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kunmi02/git-declutter/internal/safety"
	"golang.org/x/term"
)

func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func Confirm(in io.Reader, out io.Writer, prompt string, defaultYes bool) (bool, error) {
	suffix := "y/N"
	if defaultYes {
		suffix = "Y/n"
	}
	fmt.Fprintf(out, "%s [%s] ", prompt, suffix)
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return false, err
		}
		return defaultYes, nil
	}
	s := strings.TrimSpace(strings.ToLower(sc.Text()))
	if s == "" {
		return defaultYes, nil
	}
	return s == "y" || s == "yes", nil
}

func SelectSafeBranches(in io.Reader, out io.Writer, analyses []safety.BranchAnalysis, retention string) ([]safety.BranchAnalysis, error) {
	var selected []safety.BranchAnalysis
	for _, a := range analyses {
		if a.Status == safety.StatusSafe {
			selected = append(selected, a)
		}
	}
	if len(selected) == 0 {
		return []safety.BranchAnalysis{}, nil
	}

	fmt.Fprintln(out, "Selected branches to delete")
	fmt.Fprintln(out)
	for _, a := range selected {
		fmt.Fprintf(out, "[x] %-32s %s\n", a.Branch, a.Summary)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%d branches selected\n\n", len(selected))
	if retention != "" {
		fmt.Fprintf(out, "Recovery: %s\n\n", retention)
	}
	ok, err := Confirm(in, out, "Clean selected branches?", false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return selected, nil
}
