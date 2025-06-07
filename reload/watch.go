package reload

import (
	"errors"
	"io/fs"
	"markdown-server/fsnotify"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func NewDebouncer(after time.Duration) func(f func()) {
	d := &debouncer{after: after}

	return func(f func()) {
		d.add(f)
	}
}

type debouncer struct {
	mu    sync.Mutex
	after time.Duration
	timer *time.Timer
}

func (d *debouncer) add(f func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.after, f)
}

func (reload *Reloader) WatchDirectories() {
	if len(reload.directories) == 0 {
		reload.logError("no directories provided (reload.Directories is empty)\n")
		return
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		reload.logError("error initializing fsnotify watcher: %s\n", err)
	}

	defer w.Close()

	for _, pathString := range reload.directories {
		directories, err := recursiveWalk(pathString)
		if err != nil {
			var pathErr *fs.PathError
			if errors.As(err, &pathErr) {
				reload.logError("directory doesn't exist: %s\n", pathErr.Path)
			} else {
				reload.logError("error walking directories: %s\n", err)
			}
			return
		}
		for _, dir := range directories {
			// Path is converted to absolute path, so that fsnotify.Event also contains
			// absolute paths
			absPath, err := filepath.Abs(dir)
			if err != nil {
				reload.logError("Failed to convert path to absolute path: %s\n", err)
				continue
			}
			_ = w.Add(absPath)
		}
	}

	reload.logDebug("watching %s for changes\n", strings.Join(reload.directories, ","))

	debounce := NewDebouncer(100 * time.Millisecond)

	callback := func(path string) func() {
		return func() {
			reload.logDebug("Edit %s\n", path)
			if reload.OnReload != nil {
				reload.OnReload()
			}
			reload.cond.Broadcast()
		}
	}

	for {
		select {
		case err := <-w.Errors:
			reload.logError("error watching: %s \n", err)
		case e := <-w.Events:
			switch {
			case e.Has(fsnotify.Create):
				dir := filepath.Dir(e.Name)
				// Watch any created directory
				if err := w.Add(dir); err != nil {
					reload.logError("error watching %s: %s\n", e.Name, err)
					continue
				}
				debounce(callback(path.Base(e.Name)))

			case e.Has(fsnotify.Write):
				debounce(callback(path.Base(e.Name)))

			case e.Has(fsnotify.Rename), e.Has(fsnotify.Remove):
				// a renamed file might be outside the specified paths
				directories, _ := recursiveWalk(e.Name)
				for _, v := range directories {
					_ = w.Remove(v)
				}
				_ = w.Remove(e.Name)
			}
		}
	}
}

func recursiveWalk(path string) ([]string, error) {
	var res []string
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			res = append(res, path)
		}
		return nil
	})

	return res, err
}
