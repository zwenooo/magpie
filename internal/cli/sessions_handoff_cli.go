package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/yetone/magpie/internal/sessions"
)

const handoffUsage = "usage: magpie sessions handoff <agent> <session-id>"

// handoffTo: `magpie sessions handoff <agent> <id>` writes a session's
// condensed record and its whole conversation for another agent to carry it
// on, as the Sessions page's Continue in › with a prompt does, and says
// where they are.
func handoffTo(w io.Writer, args []string) error {
	if len(args) != 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return fmt.Errorf("%s", handoffUsage)
	}
	s, ok := sessions.Find(args[0], args[1])
	if !ok {
		return fmt.Errorf("no %s session %s (magpie sessions lists them)", args[0], args[1])
	}
	h, err := sessions.SaveHandoff(s, nameOf(agentNames(), s.Agent))
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Condensed record:   %s (%s)\n", h.Path, kb(h.Size))
	fmt.Fprintf(w, "Whole conversation: %s (%s)\n", h.Full, kb(h.FullSize))
	switch {
	case h.Folder == "":
		fmt.Fprintln(w, "Folder:             none recorded")
	case h.Here:
		fmt.Fprintf(w, "Folder:             %s\n", h.Folder)
	default:
		fmt.Fprintf(w, "Folder:             %s (not on this computer)\n", h.Folder)
	}
	if h.Compacted {
		fmt.Fprintln(w, "It starts at the agent's own summary of the conversation before its last compaction.")
	}
	if h.Shortened {
		fmt.Fprintln(w, "Some turns between the first and the newest are shortened or left out.")
	}
	return nil
}

func kb(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%d KB", (n+512)/1024)
}
