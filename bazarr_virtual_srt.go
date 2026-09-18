package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"tiramisu/internal/subprovider"
	"tiramisu/internal/vfs"
)

const bazarrVirtualLanguage = "pt-BR"

var bazarrDummySRT = []byte("1\n00:00:00,000 --> 00:00:01,000\nLegenda sendo preparada pelo Tiramisu.\n\n")
var bazarrVirtualSRTCache sync.Map // full virtual path -> *bazarrVirtualSubtitle

type bazarrVirtualSubtitle struct {
	Name      string
	Dir       string
	VideoPath string
	Candidate subprovider.SubtitleCandidate
	Provider  subprovider.SubtitleProvider
	DestPath  string

	once    sync.Once
	done    chan struct{}
	mu      sync.RWMutex
	content []byte
	err     error
}

type VirtualSRTNode struct {
	fs.Inode
	entry *bazarrVirtualSubtitle
}

var _ fs.NodeGetattrer = (*VirtualSRTNode)(nil)
var _ fs.NodeOpener = (*VirtualSRTNode)(nil)

func (n *VirtualSRTNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFREG | 0644
	out.Size = uint64(len(bazarrDummySRT))
	if n.entry != nil {
		out.Ino = getFileInodeFromMap(filepath.Join(n.entry.Dir, n.entry.Name))
	}
	return 0
}

func (n *VirtualSRTNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.entry == nil {
		return nil, 0, syscall.ENOENT
	}
	n.entry.startDownload()
	return &VirtualSRTHandle{entry: n.entry}, 0, 0
}

type VirtualSRTHandle struct {
	entry *bazarrVirtualSubtitle
}

var _ fs.FileReader = (*VirtualSRTHandle)(nil)

func (h *VirtualSRTHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if h.entry == nil {
		return nil, syscall.ENOENT
	}
	h.entry.startDownload()

	if off == 0 {
		select {
		case <-h.entry.done:
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return nil, syscall.EINTR
		}
	}

	data := h.entry.bytesOrDummy()
	if off >= int64(len(data)) {
		return fuse.ReadResultData(nil), 0
	}
	end := int(off) + len(dest)
	if end > len(data) {
		end = len(data)
	}
	return fuse.ReadResultData(data[off:end]), 0
}

func (v *bazarrVirtualSubtitle) startDownload() {
	v.once.Do(func() {
		if v.done == nil {
			v.done = make(chan struct{})
		}
		safeGo(func() {
			defer close(v.done)
			if v.Provider == nil || !v.Provider.IsEnabled() {
				return
			}
			if err := os.MkdirAll(filepath.Dir(v.DestPath), 0755); err != nil {
				v.mu.Lock()
				v.err = err
				v.mu.Unlock()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := v.Provider.Download(ctx, v.Candidate.ID, v.DestPath); err != nil {
				v.mu.Lock()
				v.err = err
				v.mu.Unlock()
				return
			}
			content, err := os.ReadFile(v.DestPath)
			v.mu.Lock()
			defer v.mu.Unlock()
			if err != nil || len(content) == 0 {
				v.err = err
				return
			}
			v.content = content
		})
	})
}

func (v *bazarrVirtualSubtitle) bytesOrDummy() []byte {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.content) > 0 {
		return v.content
	}
	return bazarrDummySRT
}

func clearBazarrVirtualSRTCache() {
	bazarrVirtualSRTCache.Range(func(key, value any) bool {
		bazarrVirtualSRTCache.Delete(key)
		return true
	})
}

func bazarrVirtualSRTEntries(dir string, startOffset int, provider subprovider.SubtitleProvider) []fuse.DirEntry {
	if provider == nil || !provider.IsEnabled() {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Printf("[BAZARR] readdir source failed for %s: %v", dir, err)
		return nil
	}

	var out []fuse.DirEntry
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".mkv") {
			continue
		}
		videoPath := filepath.Join(dir, e.Name())
		meta, err := vfs.ReadMetadataFromFile(videoPath)
		if err != nil || (meta.ImdbID == "" && meta.RadarrID == 0 && meta.SonarrEpisodeID == 0) {
			continue
		}
		subs, err := searchSubtitleProviderForMetadata(provider, meta)
		if err != nil {
			logger.Printf("[BAZARR] search failed for %s imdb=%s: %v", e.Name(), meta.ImdbID, err)
			continue
		}
		for _, sub := range subs {
			name := bazarrVirtualSRTName(e.Name(), sub)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			virtPath := filepath.Join(dir, name)
			entry := &bazarrVirtualSubtitle{
				Name:      name,
				Dir:       dir,
				VideoPath: videoPath,
				Candidate: sub,
				Provider:  provider,
				DestPath:  bazarrVirtualSRTDestPath(virtPath),
				done:      make(chan struct{}),
			}
			bazarrVirtualSRTCache.Store(virtPath, entry)
			out = append(out, fuse.DirEntry{
				Name: name,
				Mode: syscall.S_IFREG,
				Ino:  getFileInodeFromMap(virtPath),
				Off:  uint64(startOffset + len(out) + 1),
			})
		}
	}
	return out
}

func searchSubtitleProviderForMetadata(provider subprovider.SubtitleProvider, meta *vfs.FileMetadata) ([]subprovider.SubtitleCandidate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	mediaID := meta.RadarrID
	if mediaID == 0 {
		mediaID = meta.SonarrEpisodeID
	}
	title := meta.ImdbID
	if title == "" {
		title = filepath.Base(meta.Path)
	}
	return provider.Search(ctx, mediaID, title, bazarrVirtualLanguage)
}

func lookupBazarrVirtualSRT(dir, name string, provider subprovider.SubtitleProvider) (*bazarrVirtualSubtitle, bool) {
	if val, ok := bazarrVirtualSRTCache.Load(filepath.Join(dir, name)); ok {
		entry, ok := val.(*bazarrVirtualSubtitle)
		return entry, ok
	}
	for _, e := range bazarrVirtualSRTEntries(dir, 0, provider) {
		if e.Name == name {
			if val, ok := bazarrVirtualSRTCache.Load(filepath.Join(dir, name)); ok {
				entry, ok := val.(*bazarrVirtualSubtitle)
				return entry, ok
			}
		}
	}
	return nil, false
}

func bazarrVirtualSRTName(videoName string, sub subprovider.SubtitleCandidate) string {
	base := strings.TrimSuffix(videoName, filepath.Ext(videoName))
	if sub.Title != "" {
		base = sub.Title
	}
	release := sub.ReleaseGroup
	if release == "" {
		release = "Bazarr"
	}
	score := sub.Score
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return fmt.Sprintf("%s.%s-%d%%.%s.srt", safeSRTName(base), safeSRTName(release), score, bazarrVirtualLanguage)
}

func bazarrVirtualSRTDestPath(virtPath string) string {
	return filepath.Join(os.TempDir(), "tiramisu-bazarr-srt", fmt.Sprintf("%x.srt", xxhashString(virtPath)))
}

func xxhashString(s string) uint64 { return hashFilenameToInode(s) }

var unsafeSRTNameChars = regexp.MustCompile(`[\\/:*?"<>|]+`)

func safeSRTName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Bazarr"
	}
	s = unsafeSRTNameChars.ReplaceAllString(s, "_")
	s = strings.ReplaceAll(s, " ", ".")
	return s
}
