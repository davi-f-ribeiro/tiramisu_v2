package bazarrvfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"tiramisu/internal/metadb"
)

const VirtualLanguage = "pt-BR"

var DummySRT = []byte("1\n00:00:00,000 --> 00:00:01,000\nLegenda sendo preparada pelo Tiramisu.\n\n")
var virtualSRTCache sync.Map // full virtual path -> *Subtitle

type Options struct {
	Provider     SubtitleProvider
	InodeForPath func(string) uint64
	Go           func(func())
	Logf         func(string, ...any)
}

type Subtitle struct {
	Name      string
	Dir       string
	VideoPath string
	Candidate SubtitleCandidate
	Provider  SubtitleProvider
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
	if opts.Provider == nil || !opts.Provider.IsEnabled() {
		return nil
	}
	var out []fuse.DirEntry
	virtualSRTCache.Range(func(key, value any) bool {
		virtPath, ok := key.(string)
		if !ok || filepath.Dir(virtPath) != dir {
			return true
		}
		entry, ok := value.(*Subtitle)
		if !ok || entry == nil || entry.Candidate.ID == "" || entry.Candidate.Score < 80 {
			return true
		}
		ino := uint64(0)
		if opts.InodeForPath != nil {
			ino = opts.InodeForPath(virtPath)
		}
		out = append(out, fuse.DirEntry{Name: filepath.Base(virtPath), Mode: syscall.S_IFREG, Ino: ino})
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for i := range out {
		out[i].Off = uint64(startOffset + i + 1)
	}
	return out
}

func Lookup(dir, name string, opts Options) (*Subtitle, bool) {
	if opts.Provider == nil || !opts.Provider.IsEnabled() {
		return nil, false
	}
	val, ok := virtualSRTCache.Load(filepath.Join(dir, name))
	if !ok {
		return nil, false
	}
	entry, ok := val.(*Subtitle)
	if !ok || entry == nil || entry.Candidate.ID == "" || entry.Candidate.Score < 80 {
		return nil, false
	}
	return entry, true
}

func DiscoverVirtualSubtitles(ctx context.Context, store *metadb.DB, provider SubtitleProvider, opts Options) {
	if store == nil || provider == nil || !provider.IsEnabled() {
		return
	}
	if opts.Provider == nil {
		opts.Provider = provider
	}
	if existing, err := store.ListVirtualSubtitles(ctx, VirtualLanguage); err == nil {
		for _, rec := range existing {
			cachePersistedSubtitle(rec, provider, opts)
		}
	} else {
		logf(opts, "[BAZARR] list persisted virtual subtitles failed: %v", err)
	}
	media, err := store.ListARRMediaMissingVirtualSubtitles(ctx, VirtualLanguage)
	if err != nil {
		logf(opts, "[BAZARR] list media missing virtual subtitles failed: %v", err)
		return
	}
	for _, item := range media {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if item.ID <= 0 || item.Path == "" {
			continue
		}
		searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var subs []SubtitleCandidate
		var searchErr error
		switch item.MediaType {
		case "movie":
			subs, searchErr = provider.SearchMovie(searchCtx, int(item.ID), VirtualLanguage)
		case "episode":
			subs, searchErr = provider.SearchEpisode(searchCtx, int(item.ID), VirtualLanguage)
		}
		cancel()
		if searchErr != nil {
			logf(opts, "[BAZARR] discovery search failed for %s id=%d path=%s: %v", item.MediaType, item.ID, filepath.Base(item.Path), searchErr)
			continue
		}
		best, ok := bestSubtitle(subs)
		if !ok {
			continue
		}
		rec := metadb.VirtualSubtitle{MediaPath: item.Path, Language: VirtualLanguage, SubtitleID: best.ID, Provider: best.ReleaseGroup, Score: float64(best.Score)}
		if rec.Provider == "" {
			rec.Provider = "Bazarr"
		}
		if err := store.UpsertVirtualSubtitle(ctx, rec); err != nil {
			logf(opts, "[BAZARR] persist virtual subtitle failed for %s: %v", filepath.Base(item.Path), err)
			continue
		}
		cacheSubtitleForMedia(item.Path, best, provider, opts)
	}
}

func bestSubtitle(subs []SubtitleCandidate) (SubtitleCandidate, bool) {
	var best SubtitleCandidate
	for _, sub := range subs {
		if sub.ID == "" || sub.Score < 80 {
			continue
		}
		if best.ID == "" || sub.Score > best.Score {
			best = sub
		}
	}
	return best, best.ID != ""
}

func cachePersistedSubtitle(rec metadb.VirtualSubtitle, provider SubtitleProvider, opts Options) {
	if rec.MediaPath == "" || rec.SubtitleID == "" || rec.Score < 80 {
		return
	}
	cacheSubtitleForMedia(rec.MediaPath, SubtitleCandidate{ID: rec.SubtitleID, Title: strings.TrimSuffix(filepath.Base(rec.MediaPath), filepath.Ext(rec.MediaPath)), ReleaseGroup: rec.Provider, Score: int(rec.Score), Language: rec.Language}, provider, opts)
}

func cacheSubtitleForMedia(videoPath string, sub SubtitleCandidate, provider SubtitleProvider, opts Options) {
	name := VirtualSRTName(filepath.Base(videoPath), sub)
	if name == "" {
		return
	}
	dir := filepath.Dir(videoPath)
	virtPath := filepath.Join(dir, name)
	virtualSRTCache.Store(virtPath, &Subtitle{Name: name, Dir: dir, VideoPath: videoPath, Candidate: sub, Provider: provider, DestPath: DestPath(virtPath), done: make(chan struct{}), goFunc: opts.Go})
}

func VirtualSRTName(videoName string, sub SubtitleCandidate) string {
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
