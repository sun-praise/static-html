package live

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceInterval = 300 * time.Millisecond

func WatchDir(ctx context.Context, dir string, notify func()) (context.CancelFunc, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := addWatchRecursive(w, dir); err != nil {
		w.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		defer w.Close()

		var timer *time.Timer
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if shouldIgnorePath(event.Name) {
					continue
				}
				if event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						_ = addWatchRecursive(w, event.Name)
					}
				}
				if timer != nil {
					timer.Reset(debounceInterval)
				} else {
					timer = time.AfterFunc(debounceInterval, func() {
						notify()
					})
				}
			case <-w.Errors:
				return
			}
		}
	}()

	return cancel, nil
}

func addWatchRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if shouldIgnorePath(path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return w.Add(path)
		}
		return nil
	})
}

func shouldIgnorePath(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") && base != "." {
		return true
	}
	if strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".tmp") {
		return true
	}
	return false
}
