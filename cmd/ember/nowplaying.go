package main

import (
	"context"
	"sync"
)

func (a *App) StartNowPlaying(ctx context.Context) {
	np := a.nowPlaying
	var plex sync.WaitGroup
	defer plex.Wait()
	if np.Plex != nil {
		plex.Go(func() { np.Plex.Run(ctx, np, a.logger) })
	}
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-np.Jobs:
			np.LookupArtist(ctx, j, a.logger)
		}
	}
}
