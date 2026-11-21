package library

import (
	"context"
	"errors"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

var errRepoNeedsAuth = errors.New("GitHub refused anonymous access")
var repoAuthenticated = ghRepoRead

func ghRepoRead(endpoint, accept string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := proc.CommandContext(ctx, "gh", "api", "--hostname", "github.com", "--method", "GET", "-H", "Accept: "+accept, endpoint)
	cmd.Env = append(cmd.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_PAGER=cat")
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.New("cannot start GitHub CLI (gh); install it and sign in to github.com for private repositories")
	}
	data, readErr := repoRead(output, limit)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if waitErr != nil {
		return nil, errors.New("GitHub CLI could not read this repository or ref; check gh auth status for github.com and the account's repository access")
	}
	return data, nil
}
