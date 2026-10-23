//go:build unix

package agentattachment

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

// Check every ancestor, not just the leaf. Private control directories must be
// owned by this user; root-owned sticky /tmp is allowed as an ancestor. This is
// filesystem isolation from other UIDs, not protection from root/same-UID writers.
func privateParent(path string, create bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrSocket
	}
	parent := filepath.Dir(path)
	parts := strings.Split(strings.TrimPrefix(parent, string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	for i, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) && create && i == len(parts)-1 {
			err = os.Mkdir(current, 0700)
			if err == nil {
				info, err = os.Lstat(current)
			}
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrSocket
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		isStickyRoot := (stat.Uid == 0 || stat.Uid == 65534) && info.Mode()&os.ModeSticky != 0
		if !ok || (!isStickyRoot && stat.Uid != uint32(os.Geteuid())) {
			return ErrSocket
		}
		if current == parent {
			if stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
				return ErrSocket
			}
		} else if info.Mode().Perm()&0022 != 0 && !isStickyRoot {
			return ErrSocket
		}
	}
	if parent == string(filepath.Separator) {
		return ErrSocket
	}
	return nil
}

func privateLeaf(path string, kind os.FileMode) (os.FileInfo, error) {
	i, err := os.Lstat(path)
	if err != nil || i.Mode()&os.ModeSymlink != 0 || i.Mode()&os.ModeType != kind || i.Mode().Perm() != 0600 {
		return nil, ErrSocket
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) {
		return nil, ErrSocket
	}
	return i, nil
}

func listenLocal(path string) (net.Listener, func() error, error) {
	if err := privateParent(path, true); err != nil {
		return nil, nil, err
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return nil, nil, ErrSocket
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, nil, ErrSocket
	}
	l.SetUnlinkOnClose(false)
	created, err := os.Lstat(path)
	cleanup := func() error {
		current, e := os.Lstat(path)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil || created == nil || !os.SameFile(created, current) || current.Mode()&os.ModeSocket == 0 {
			return ErrSocket
		}
		return os.Remove(path)
	}
	if err != nil {
		_ = l.Close()
		return nil, nil, ErrSocket
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = l.Close()
		_ = cleanup()
		return nil, nil, ErrSocket
	}
	if _, err := privateLeaf(path, os.ModeSocket); err != nil {
		_ = l.Close()
		_ = cleanup()
		return nil, nil, ErrSocket
	}
	return l, cleanup, nil
}

func dialLocal(ctx context.Context, path string) (io.ReadWriteCloser, error) {
	if err := privateParent(path, false); err != nil {
		return nil, err
	}
	before, err := privateLeaf(path, os.ModeSocket)
	if err != nil {
		return nil, err
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrSocket
	}
	after, err := privateLeaf(path, os.ModeSocket)
	if err != nil || !os.SameFile(before, after) {
		_ = c.Close()
		return nil, ErrSocket
	}
	return c, nil
}

// WriteCredential deliberately delivers a new invitation into a private file.
// It never overwrites an existing path and never prints credential material.
func WriteCredential(path string, p agentprotocol.InvitationProof) error {
	if p.Validate() != nil {
		return ErrAdmission
	}
	if err := privateParent(path, false); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrSocket
	}
	defer f.Close()
	b, _ := json.Marshal(p)
	b = append(b, '\n')
	if _, err := f.Write(b); err != nil {
		return ErrSocket
	}
	if f.Sync() != nil {
		return ErrSocket
	}
	if f.Close() != nil {
		return ErrSocket
	}
	return nil
}

func ReadCredential(path string) (agentprotocol.InvitationProof, error) {
	if err := privateParent(path, false); err != nil {
		return agentprotocol.InvitationProof{}, err
	}
	before, err := privateLeaf(path, 0)
	if err != nil {
		return agentprotocol.InvitationProof{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return agentprotocol.InvitationProof{}, ErrSocket
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return agentprotocol.InvitationProof{}, ErrSocket
	}
	b, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(b) > 1024 {
		return agentprotocol.InvitationProof{}, ErrAdmission
	}
	var p agentprotocol.InvitationProof
	if strictjson.Decode(b, &p) != nil || p.Validate() != nil {
		return agentprotocol.InvitationProof{}, ErrAdmission
	}
	return p, nil
}
