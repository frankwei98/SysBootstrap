package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const secureRemoveOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW

// RemoveAllBeneath recursively removes a directory while resolving every
// component relative to a pinned base directory descriptor. It never follows
// an ancestor or descendant symlink, so a concurrent pathname replacement
// cannot redirect a privileged uninstall outside baseDir.
func RemoveAllBeneath(baseDir, target string) error {
	return RemoveAllBeneathContext(context.Background(), baseDir, target)
}

// RemoveAllBeneathContext is the context-aware form of RemoveAllBeneath. It
// checks cancellation before and after every destructive operation while
// retaining the same descriptor-relative, no-follow safety guarantees.
func RemoveAllBeneathContext(ctx context.Context, baseDir, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absBase, rel, err := removeAllRelativePath(baseDir, target)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return fmt.Errorf("resolve removal base %s: %w", absBase, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	base, baseStat, err := openPinnedRemovalDir(realBase)
	if err != nil {
		return fmt.Errorf("open removal base %s: %w", realBase, err)
	}
	defer base.Close()

	components := strings.Split(rel, string(filepath.Separator))
	parent := base
	for _, component := range components[:len(components)-1] {
		if err := ctx.Err(); err != nil {
			if parent != base {
				_ = parent.Close()
			}
			return err
		}
		child, _, openErr := openPinnedRemovalDirAt(parent, component, uint64(baseStat.Dev))
		if openErr != nil {
			if parent != base {
				_ = parent.Close()
			}
			if errors.Is(openErr, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("open removal path component %s: %w", component, openErr)
		}
		if err := ctx.Err(); err != nil {
			_ = child.Close()
			if parent != base {
				_ = parent.Close()
			}
			return err
		}
		if parent != base {
			_ = parent.Close()
		}
		parent = child
	}
	if parent != base {
		defer parent.Close()
	}

	name := components[len(components)-1]
	if err := ctx.Err(); err != nil {
		return err
	}
	targetDir, targetStat, err := openPinnedRemovalDirAt(parent, name, uint64(baseStat.Dev))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("refusing unsafe removal target %s: %w", target, err)
	}
	defer targetDir.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := removePinnedDirectoryContents(ctx, targetDir, uint64(baseStat.Dev)); err != nil {
		return fmt.Errorf("remove contents of %s: %w", target, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return unlinkPinnedDirectory(ctx, parent, name, targetStat)
}

func removeAllRelativePath(baseDir, target string) (string, string, error) {
	if baseDir == "" || target == "" {
		return "", "", fmt.Errorf("removal base and target must not be empty")
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve removal base %s: %w", baseDir, err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", "", fmt.Errorf("resolve removal target %s: %w", target, err)
	}
	rel, err := filepath.Rel(filepath.Clean(absBase), filepath.Clean(absTarget))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("removal target %s is not strictly beneath %s", target, baseDir)
	}
	return filepath.Clean(absBase), filepath.Clean(rel), nil
}

func openPinnedRemovalDir(path string) (*os.File, *unix.Stat_t, error) {
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("pinned removal directory must be absolute: %s", path)
	}
	fd, err := unix.Open(string(filepath.Separator), secureRemoveOpenFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	current := os.NewFile(uintptr(fd), string(filepath.Separator))
	for _, component := range splitPathComponents(path) {
		childFD, openErr := unix.Openat(int(current.Fd()), component, secureRemoveOpenFlags, 0)
		if openErr != nil {
			_ = current.Close()
			return nil, nil, openErr
		}
		child := os.NewFile(uintptr(childFD), component)
		if closeErr := current.Close(); closeErr != nil {
			_ = child.Close()
			return nil, nil, closeErr
		}
		current = child
	}
	stat := &unix.Stat_t{}
	if err := unix.Fstat(int(current.Fd()), stat); err != nil {
		_ = current.Close()
		return nil, nil, err
	}
	return current, stat, nil
}

func openPinnedRemovalDirAt(parent *os.File, name string, baseDevice uint64) (*os.File, *unix.Stat_t, error) {
	fd, err := openRemovalDirAt(int(parent.Fd()), name)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	stat := &unix.Stat_t{}
	if err := unix.Fstat(fd, stat); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if uint64(stat.Dev) != baseDevice {
		_ = f.Close()
		return nil, nil, fmt.Errorf("refusing to cross filesystem boundary at %s", name)
	}
	return f, stat, nil
}

func removePinnedDirectoryContents(ctx context.Context, dir *os.File, baseDevice uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) {
			return fmt.Errorf("invalid directory entry %q", name)
		}
		if err := removePinnedEntry(ctx, dir, name, baseDevice); err != nil {
			return err
		}
	}
	return nil
}

func removePinnedEntry(ctx context.Context, parent *os.File, name string, baseDevice uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := unix.Unlinkat(int(parent.Fd()), name, 0)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return ctx.Err()
	}
	if !errors.Is(err, unix.EISDIR) && !errors.Is(err, unix.EPERM) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(ctxErr, fmt.Errorf("unlink %s: %w", name, err))
		}
		return fmt.Errorf("unlink %s: %w", name, err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	child, childStat, err := openPinnedRemovalDirAt(parent, name, baseDevice)
	if err != nil {
		return fmt.Errorf("open directory %s without following links: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		_ = child.Close()
		return err
	}
	if err := removePinnedDirectoryContents(ctx, child, baseDevice); err != nil {
		_ = child.Close()
		return err
	}
	if err := child.Close(); err != nil {
		return fmt.Errorf("close directory %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return unlinkPinnedDirectory(ctx, parent, name, childStat)
}

func unlinkPinnedDirectory(ctx context.Context, parent *os.File, name string, opened *unix.Stat_t) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current := &unix.Stat_t{}
	if err := unix.Fstatat(int(parent.Fd()), name, current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return fmt.Errorf("inspect directory %s before removal: %w", name, err)
	}
	if uint64(current.Dev) != uint64(opened.Dev) || current.Ino != opened.Ino {
		return fmt.Errorf("refusing to remove directory %s: path changed during deletion", name)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(ctxErr, fmt.Errorf("remove directory %s: %w", name, err))
		}
		return fmt.Errorf("remove directory %s: %w", name, err)
	}
	return ctx.Err()
}
