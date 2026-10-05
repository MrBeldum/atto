package app

import (
	"context"
	"time"

	"github.com/sebastianrcnt/atto/update"
)

// checkUpdate tells the user about a new release, once per start.
func (a *App) checkUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if tag := update.Available(ctx); tag != "" {
		a.ui.Do(func() {
			a.notice("atto %s is out (you have %s). Update with: atto update", tag, Version)
		})
	}
}
