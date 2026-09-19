package bazarrvfs

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

	"github.com/cespare/xxhash/v2"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"tiramisu/internal/subprovider"
	"tiramisu/internal/vfs"
)

const VirtualLanguage = "pt-BR"

var DummySRT = []byte("1\n00:00:00,000 --> 00:00:01,000\nLegenda sendo preparada pelo Tiramisu.\n\n")
var virtualSRTCache sync.Map // full virtual path -> *Subtitle

type Options struct {
	Provider     subprovider.SubtitleProvider
	InodeForPath func(string) uint64
	Go           func(func())
	Logf         func(string, ...any)
}

type Subtitle struct {
	Name      string
	Dir       string
	VideoPath string
	Candidate subprovider.SubtitleCandidate
	Provider  subprovider.SubtitleProvider
	DestPath  string
	goFunc    func(func())

	once    sync.Once
	done    chan struct{}
	mu      sync.RWMutex
	content []byte
	err     error
}

type Node struct {
	fs.Inode
	entry        *Subtitle
	inodeForPath func(string) uint64
}

var _ fs.NodeGetattrer = (*Node)(nil)
var _ fs.NodeOpener = (*Node)(nil)

func NewNode(entry *Subtitle, opts Options) *Node {
	return &Node{entry: entry, inodeForPath: opts.InodeForPath}
}

func (n *Node) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFREG | 0644
	out.Size = uint64(len(DummySRT))
	if n.entry != nil && n.inodeForPath != nil {
		out.Ino = n.inodeForPath(filepath.Join(n.entry.Dir, n.entry.Name))
	}
	return 0
}

func (n *Node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.entry == nil {
		return nil, 0, syscall.ENOENT
	}
	n.entry.startDownload()
	return &Handle{entry: n.entry}, 0, 0
}

type Handle struct {
	entry *Subtitle
}

var _ fs.FileReader = (*Handle)(nil)

func (h *Handle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
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

func (v *Subtitle) startDownload() {
	v.once.Do(func() {
		if v.done == nil {
			v.done = make(chan struct{})
		}
		run := v.goFunc
		if run == nil {
			run = func(f func()) { go f() }
		}
		run(func() {
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

func (v *Subtitle) bytesOrDummy() []byte {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.content) > 0 {
		return v.content
	}
	return DummySRT
}

func ClearCache() {
	virtualSRTCache.Range(func(key, value any) bool {
		virtualSRTCache.Delete(key)
		return true
	})
}

func Entries(dir string, startOffset int, opts Options) []fuse.DirEntry {
	provider := opts.Provider
	if provider == nil || !provider.IsEnabled() {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		logf(opts, "[BAZARR] readdir source failed for %s: %v", dir, err)
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
		subs, err := searchProviderForMetadata(provider, meta)
		if err != nil {
			logf(opts, "[BAZARR] search failed for %s imdb=%s: %v", e.Name(), meta.ImdbID, err)
			continue
		}
		for _, sub := range subs {
			name := VirtualSRTName(e.Name(), sub)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			virtPath := filepath.Join(dir, name)
			entry := &Subtitle{
				Name:      name,
				Dir:       dir,
				VideoPath: videoPath,
				Candidate: sub,
				Provider:  provider,
				DestPath:  DestPath(virtPath),
				done:      make(chan struct{}),
				goFunc:    opts.Go,
			}
			virtualSRTCache.Store(virtPath, entry)
			ino := uint64(0)
			if opts.InodeForPath != nil {
				ino = opts.InodeForPath(virtPath)
			}
			out = append(out, fuse.DirEntry{
				Name: name,
				Mode: syscall.S_IFREG,
				Ino:  ino,
				Off:  uint64(startOffset + len(out) + 1),
			})
		}
	}
	return out
}

func searchProviderForMetadata(provider subprovider.SubtitleProvider, meta *vfs.FileMetadata) ([]subprovider.SubtitleCandidate, error) {
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
	return provider.Search(ctx, mediaID, title, VirtualLanguage)
}

func Lookup(dir, name string, opts Options) (*Subtitle, bool) {
	if val, ok := virtualSRTCache.Load(filepath.Join(dir, name)); ok {
		entry, ok := val.(*Subtitle)
		return entry, ok
	}
	for _, e := range Entries(dir, 0, opts) {
		if e.Name == name {
			if val, ok := virtualSRTCache.Load(filepath.Join(dir, name)); ok {
				entry, ok := val.(*Subtitle)
				return entry, ok
			}
		}
	}
	return nil, false
}

func VirtualSRTName(videoName string, sub subprovider.SubtitleCandidate) string {
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
	return fmt.Sprintf("%s.%s-%d%%.%s.srt", safeSRTName(base), safeSRTName(release), score, VirtualLanguage)
}

func DestPath(virtPath string) string {
	return filepath.Join(os.TempDir(), "tiramisu-bazarr-srt", fmt.Sprintf("%x.srt", xxhash.Sum64String(virtPath)))
}

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

func logf(opts Options, format string, args ...any) {
	if opts.Logf != nil {
		opts.Logf(format, args...)
	}
}
