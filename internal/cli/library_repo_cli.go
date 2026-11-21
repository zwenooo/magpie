package cli

import (
	"fmt"

	"github.com/yetone/magpie/internal/library"
)

func libraryRepoCmd(args []string) error {
	base := 2
	apply := args[0] == "repo-update"
	if apply {
		base++
	}
	if len(args) < base || len(args) > base+3 {
		return fmt.Errorf("usage:\n  %s", libraryUsage)
	}
	var source *library.MCPRepoSource
	if len(args) > base {
		source = &library.MCPRepoSource{Repo: args[base]}
		if len(args) > base+1 {
			source.Ref = args[base+1]
		}
		if len(args) > base+2 {
			source.Path = args[base+2]
		}
	}
	name := args[1]
	if apply {
		result, err := library.ApplyMCPRepo(name, source, args[2])
		if err != nil {
			return err
		}
		printLibraryResult(result)
		fmt.Printf("%s: installed commit %s\n", name, args[2])
		return nil
	}
	check, err := library.CheckMCPRepo(name, source)
	if err != nil {
		return err
	}
	current := check.Current
	if current == "" {
		current = "(not installed from a repository)"
	}
	fmt.Printf("%s: %s -> %s\n", name, current, check.Latest)
	return nil
}
