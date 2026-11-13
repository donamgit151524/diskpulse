package scanner

import (
	"container/heap"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"diskpulse/internal/config"
	"diskpulse/internal/model"
	"diskpulse/internal/tagging"
)

// ScanOutput contains the completed snapshot header, pruned directory nodes, and top large files.
type ScanOutput struct {
	Snapshot   model.Snapshot
	Nodes      []model.DirNode
	LargeFiles []model.LargeFile
}

type walkNode struct {
	path       string
	parentPath string
	name       string
	depth      int
	sizeBytes  int64
	fileCount  int64
	dirCount   int64
	children   []*walkNode
}

type largeFileHeap []model.LargeFile

func (h largeFileHeap) Len() int           { return len(h) }
func (h largeFileHeap) Less(i, j int) bool { return h[i].SizeBytes < h[j].SizeBytes }
func (h largeFileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *largeFileHeap) Push(x any)        { *h = append(*h, x.(model.LargeFile)) }
func (h *largeFileHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// ScanDrive traverses root safely, builds exact subtree totals, prunes nodes, and collects top large files.
func ScanDrive(
	ctx context.Context,
	root string,
	cfg config.Config,
	progressFn func(model.ScanProgress),
) (ScanOutput, error) {
	started := time.Now()
	cleanRoot := normalizeRoot(root)

	driveStats, err := GetDriveStats(cleanRoot)
	if err != nil {
		// Fall back cleanly when scanning a custom folder root in tests.
		driveStats = model.DriveInfo{Root: cleanRoot}
	}

	ignores := compileIgnores(cfg.IgnorePaths)
	sem := make(chan struct{}, maxInt(1, cfg.ScannerWorkers))

	var dirsScanned atomic.Int64
	var filesScanned atomic.Int64
	var bytesScanned atomic.Int64
	var errorCount atomic.Int64

	var heapMu sync.Mutex
	lfHeap := &largeFileHeap{}
	heap.Init(lfHeap)

	recordLargeFile := func(path string, size int64, modTime time.Time) {
		if size < cfg.LargeFileThresholdBytes || cfg.TopLargeFiles <= 0 {
			return
		}
		heapMu.Lock()
		defer heapMu.Unlock()
		if lfHeap.Len() < cfg.TopLargeFiles {
			heap.Push(lfHeap, model.LargeFile{
				Path:       path,
				SizeBytes:  size,
				ModifiedAt: modTime,
				Tag:        tagging.ClassifyPath(path),
			})
			return
		}
		if (*lfHeap)[0].SizeBytes < size {
			(*lfHeap)[0] = model.LargeFile{
				Path:       path,
				SizeBytes:  size,
				ModifiedAt: modTime,
				Tag:        tagging.ClassifyPath(path),
			}
			heap.Fix(lfHeap, 0)
		}
	}

	var reportStop chan struct{}
	var currentPathMu sync.Mutex
	currentPath := cleanRoot
	if progressFn != nil {
		reportStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(400 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-reportStop:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					currentPathMu.Lock()
					cp := currentPath
					currentPathMu.Unlock()
					progressFn(model.ScanProgress{
						Active:       true,
						DriveRoot:    cleanRoot,
						StartedAt:    started,
						DirsScanned:  dirsScanned.Load(),
						FilesScanned: filesScanned.Load(),
						BytesScanned: bytesScanned.Load(),
						CurrentPath:  cp,
					})
				}
			}
		}()
		defer close(reportStop)
	}

	var walk func(dirPath, parentPath, name string, depth int) *walkNode
	walk = func(dirPath, parentPath, name string, depth int) *walkNode {
		node := &walkNode{
			path:       dirPath,
			parentPath: parentPath,
			name:       name,
			depth:      depth,
		}
		if ctx.Err() != nil {
			return node
		}

		dirsScanned.Add(1)
		if progressFn != nil && dirsScanned.Load()%150 == 0 {
			currentPathMu.Lock()
			currentPath = dirPath
			currentPathMu.Unlock()
		}

		entries, err := os.ReadDir(dirPath)
		if err != nil {
			errorCount.Add(1)
			return node
		}

		var subdirs []os.DirEntry
		for _, entry := range entries {
			if ctx.Err() != nil {
				break
			}
			entryName := entry.Name()
			fullPath := joinChildPath(dirPath, entryName)

			info, infoErr := entry.Info()
			if infoErr != nil {
				errorCount.Add(1)
				continue
			}

			if entry.IsDir() {
				if isReparseDir(info.Sys()) || shouldIgnore(fullPath, entryName, ignores) {
					continue
				}
				subdirs = append(subdirs, entry)
				continue
			}

			skip, size, ok := inspectFileAttributes(fullPath, info.Sys())
			if !ok {
				size = info.Size()
			}
			if skip {
				continue
			}
			node.sizeBytes += size
			node.fileCount++
			filesScanned.Add(1)
			bytesScanned.Add(size)
			if size >= cfg.LargeFileThresholdBytes {
				recordLargeFile(fullPath, size, info.ModTime())
			}
		}

		if len(subdirs) > 0 {
			children := make([]*walkNode, len(subdirs))
			var wg sync.WaitGroup
			for i, sd := range subdirs {
				if ctx.Err() != nil {
					break
				}
				childName := sd.Name()
				childPath := joinChildPath(dirPath, childName)
				idx := i

				// Non-blocking worker spawn avoids semaphore deadlock across recursion levels.
				select {
				case sem <- struct{}{}:
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer func() { <-sem }()
						children[idx] = walk(childPath, dirPath, childName, depth+1)
					}()
				default:
					children[idx] = walk(childPath, dirPath, childName, depth+1)
				}
			}
			wg.Wait()

			for _, child := range children {
				if child == nil {
					continue
				}
				node.sizeBytes += child.sizeBytes
				node.fileCount += child.fileCount
				node.dirCount += 1 + child.dirCount
				node.children = append(node.children, child)
			}
		}

		return node
	}

	rootName := cleanRoot
	rootTree := walk(cleanRoot, "", rootName, 0)
	if ctx.Err() != nil {
		return ScanOutput{}, ctx.Err()
	}

	persisted := make([]model.DirNode, 0, 1024)
	flattenPruned(rootTree, cfg.MaxDepth, cfg.MinPersistBytes, &persisted)

	// Drain heap descending by size.
	largeFiles := make([]model.LargeFile, lfHeap.Len())
	for i := len(largeFiles) - 1; i >= 0; i-- {
		largeFiles[i] = heap.Pop(lfHeap).(model.LargeFile)
	}

	snap := model.Snapshot{
		DriveRoot:       cleanRoot,
		CreatedAt:       time.Now(),
		DurationMs:      time.Since(started).Milliseconds(),
		DriveTotalBytes: driveStats.TotalBytes,
		DriveFreeBytes:  driveStats.FreeBytes,
		DriveUsedBytes:  driveStats.UsedBytes,
		ScannedBytes:    rootTree.sizeBytes,
		FileCount:       rootTree.fileCount,
		DirCount:        rootTree.dirCount,
		ErrorCount:      errorCount.Load(),
		Status:          "completed",
	}

	return ScanOutput{
		Snapshot:   snap,
		Nodes:      persisted,
		LargeFiles: largeFiles,
	}, nil
}

func flattenPruned(n *walkNode, maxDepth int, minPersistBytes int64, out *[]model.DirNode) {
	if n == nil {
		return
	}
	var persistedChildrenBytes int64
	var keptChildren []*walkNode
	for _, c := range n.children {
		if c.depth <= maxDepth || c.sizeBytes >= minPersistBytes {
			persistedChildrenBytes += c.sizeBytes
			keptChildren = append(keptChildren, c)
		}
	}
	otherBytes := n.sizeBytes - persistedChildrenBytes
	if otherBytes < 0 {
		otherBytes = 0
	}

	*out = append(*out, model.DirNode{
		Path:       n.path,
		ParentPath: n.parentPath,
		Name:       n.name,
		Depth:      n.depth,
		SizeBytes:  n.sizeBytes,
		OtherBytes: otherBytes,
		FileCount:  n.fileCount,
		DirCount:   n.dirCount,
		Tag:        tagging.ClassifyPath(n.path),
	})

	for _, c := range keptChildren {
		flattenPruned(c, maxDepth, minPersistBytes, out)
	}
}

type ignoreRule struct {
	raw     string
	isExact bool
}

func compileIgnores(paths []string) []ignoreRule {
	rules := make([]ignoreRule, 0, len(paths))
	for _, p := range paths {
		norm := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(p, "/", `\`)))
		norm = strings.TrimSuffix(norm, `\`)
		if norm == "" {
			continue
		}
		isExact := strings.Contains(norm, `:`) || strings.HasPrefix(norm, `\`)
		rules = append(rules, ignoreRule{raw: norm, isExact: isExact})
	}
	return rules
}

func shouldIgnore(fullPath, entryName string, rules []ignoreRule) bool {
	lowerFull := strings.ToLower(strings.TrimSuffix(fullPath, `\`))
	lowerName := strings.ToLower(entryName)
	for _, r := range rules {
		if r.isExact {
			if lowerFull == r.raw || strings.HasPrefix(lowerFull, r.raw+`\`) {
				return true
			}
		} else if lowerName == r.raw {
			return true
		}
	}
	return false
}

func normalizeRoot(root string) string {
	root = strings.TrimSpace(root)
	if len(root) == 2 && root[1] == ':' {
		return strings.ToUpper(root[:1]) + `:\`
	}
	cleaned := filepath.Clean(root)
	if len(cleaned) == 2 && cleaned[1] == ':' {
		return strings.ToUpper(cleaned[:1]) + `:\`
	}
	if len(cleaned) >= 3 && cleaned[1] == ':' && cleaned[2] == '\\' {
		cleaned = strings.ToUpper(cleaned[:1]) + cleaned[1:]
	}
	return cleaned
}

func joinChildPath(parent, name string) string {
	if strings.HasSuffix(parent, `\`) {
		return parent + name
	}
	return parent + `\` + name
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
