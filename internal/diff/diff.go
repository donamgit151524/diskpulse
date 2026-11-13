package diff

import (
	"sort"
	"strings"

	"diskpulse/internal/model"
	"diskpulse/internal/tagging"
)

const minNotableDeltaBytes = 10 * 1024 * 1024 // 10 MB noise floor for ranked lists

func normKey(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, "/", `\`))
}

// CompareSnapshots computes directory deltas, root-cause attribution, large file diffs, and tag breakdown.
func CompareSnapshots(
	snapA, snapB model.Snapshot,
	nodesA, nodesB []model.DirNode,
	filesA, filesB []model.LargeFile,
) model.DiffResult {
	mapA := make(map[string]model.DirNode, len(nodesA))
	for _, n := range nodesA {
		mapA[normKey(n.Path)] = n
	}
	mapB := make(map[string]model.DirNode, len(nodesB))
	for _, n := range nodesB {
		mapB[normKey(n.Path)] = n
	}

	// Collect union of all keys and map parent -> immediate children.
	allKeys := make(map[string]struct{}, len(mapA)+len(mapB))
	childrenByParent := make(map[string][]string)

	for k := range mapA {
		allKeys[k] = struct{}{}
	}
	for k := range mapB {
		allKeys[k] = struct{}{}
	}

	entriesByKey := make(map[string]*model.DiffEntry, len(allKeys))
	for k := range allKeys {
		a, okA := mapA[k]
		b, okB := mapB[k]

		var path, parent, name, tag string
		var depth int
		var sizeBefore, sizeAfter int64
		var status string

		switch {
		case okA && okB:
			path = b.Path
			parent = b.ParentPath
			name = b.Name
			depth = b.Depth
			tag = b.Tag
			sizeBefore = a.SizeBytes
			sizeAfter = b.SizeBytes
			switch {
			case sizeAfter > sizeBefore:
				status = "grew"
			case sizeAfter < sizeBefore:
				status = "shrank"
			default:
				status = "unchanged"
			}
		case okB:
			path = b.Path
			parent = b.ParentPath
			name = b.Name
			depth = b.Depth
			tag = b.Tag
			sizeBefore = 0
			sizeAfter = b.SizeBytes
			status = "new"
		default:
			path = a.Path
			parent = a.ParentPath
			name = a.Name
			depth = a.Depth
			tag = a.Tag
			sizeBefore = a.SizeBytes
			sizeAfter = 0
			status = "deleted"
		}

		if tag == "" {
			tag = tagging.ClassifyPath(path)
		}

		entry := &model.DiffEntry{
			Path:       path,
			ParentPath: parent,
			Name:       name,
			Depth:      depth,
			SizeBefore: sizeBefore,
			SizeAfter:  sizeAfter,
			DeltaBytes: sizeAfter - sizeBefore,
			Status:     status,
			Tag:        tag,
			Hint:       tagging.CleanupHint(tag),
		}
		entriesByKey[k] = entry

		if parent != "" {
			pk := normKey(parent)
			childrenByParent[pk] = append(childrenByParent[pk], k)
		}
	}

	// Root-Cause Attribution:
	// AttributedDelta(P) = Delta(P) - sum(Delta(immediate tracked children of P)).
	// This isolates growth to the deepest tracked directory responsible rather than inflating ancestors.
	var rootCauses []model.DiffEntry
	var freedSpaces []model.DiffEntry
	var allChanges []model.DiffEntry
	tagTotals := make(map[string]*model.TagSummary)

	for k, entry := range entriesByKey {
		var childDeltaSum int64
		for _, ck := range childrenByParent[k] {
			if child, ok := entriesByKey[ck]; ok {
				childDeltaSum += child.DeltaBytes
			}
		}
		entry.AttributedDeltaBytes = entry.DeltaBytes - childDeltaSum

		if entry.DeltaBytes != 0 {
			allChanges = append(allChanges, *entry)
		}
		if entry.AttributedDeltaBytes >= minNotableDeltaBytes {
			rootCauses = append(rootCauses, *entry)
			ts, ok := tagTotals[entry.Tag]
			if !ok {
				ts = &model.TagSummary{
					Tag:   entry.Tag,
					Label: tagging.HumanLabel(entry.Tag),
				}
				tagTotals[entry.Tag] = ts
			}
			ts.DeltaBytes += entry.AttributedDeltaBytes
			ts.NodeCount++
		} else if entry.AttributedDeltaBytes <= -minNotableDeltaBytes {
			freedSpaces = append(freedSpaces, *entry)
		}
	}

	sort.Slice(rootCauses, func(i, j int) bool {
		if rootCauses[i].AttributedDeltaBytes != rootCauses[j].AttributedDeltaBytes {
			return rootCauses[i].AttributedDeltaBytes > rootCauses[j].AttributedDeltaBytes
		}
		return rootCauses[i].Depth > rootCauses[j].Depth
	})

	sort.Slice(freedSpaces, func(i, j int) bool {
		if freedSpaces[i].AttributedDeltaBytes != freedSpaces[j].AttributedDeltaBytes {
			return freedSpaces[i].AttributedDeltaBytes < freedSpaces[j].AttributedDeltaBytes
		}
		return freedSpaces[i].Depth > freedSpaces[j].Depth
	})

	sort.Slice(allChanges, func(i, j int) bool {
		absI := allChanges[i].DeltaBytes
		if absI < 0 {
			absI = -absI
		}
		absJ := allChanges[j].DeltaBytes
		if absJ < 0 {
			absJ = -absJ
		}
		if absI != absJ {
			return absI > absJ
		}
		return allChanges[i].Depth < allChanges[j].Depth
	})

	tagBreakdown := make([]model.TagSummary, 0, len(tagTotals))
	for _, ts := range tagTotals {
		tagBreakdown = append(tagBreakdown, *ts)
	}
	sort.Slice(tagBreakdown, func(i, j int) bool {
		return tagBreakdown[i].DeltaBytes > tagBreakdown[j].DeltaBytes
	})

	largeDiffs := compareLargeFiles(filesA, filesB)

	if len(rootCauses) > 100 {
		rootCauses = rootCauses[:100]
	}
	if len(freedSpaces) > 100 {
		freedSpaces = freedSpaces[:100]
	}
	if len(allChanges) > 300 {
		allChanges = allChanges[:300]
	}

	return model.DiffResult{
		DriveRoot:      snapB.DriveRoot,
		SnapshotA:      snapA,
		SnapshotB:      snapB,
		DriveUsedDelta: snapB.DriveUsedBytes - snapA.DriveUsedBytes,
		ScannedDelta:   snapB.ScannedBytes - snapA.ScannedBytes,
		RootCauses:     rootCauses,
		FreedSpaces:    freedSpaces,
		AllChanges:     allChanges,
		LargeFileDiffs: largeDiffs,
		TagBreakdown:   tagBreakdown,
	}
}

func compareLargeFiles(filesA, filesB []model.LargeFile) []model.LargeFileDiff {
	mapA := make(map[string]model.LargeFile, len(filesA))
	for _, f := range filesA {
		mapA[normKey(f.Path)] = f
	}
	mapB := make(map[string]model.LargeFile, len(filesB))
	for _, f := range filesB {
		mapB[normKey(f.Path)] = f
	}

	keys := make(map[string]struct{}, len(mapA)+len(mapB))
	for k := range mapA {
		keys[k] = struct{}{}
	}
	for k := range mapB {
		keys[k] = struct{}{}
	}

	var diffs []model.LargeFileDiff
	for k := range keys {
		fa, okA := mapA[k]
		fb, okB := mapB[k]

		var path, tag, status string
		var before, after int64

		switch {
		case okA && okB:
			path = fb.Path
			tag = fb.Tag
			before = fa.SizeBytes
			after = fb.SizeBytes
			if after == before {
				continue
			}
			if after > before {
				status = "grew"
			} else {
				status = "shrank"
			}
		case okB:
			path = fb.Path
			tag = fb.Tag
			after = fb.SizeBytes
			status = "new"
		default:
			path = fa.Path
			tag = fa.Tag
			before = fa.SizeBytes
			status = "deleted"
		}

		if tag == "" {
			tag = tagging.ClassifyPath(path)
		}

		diffs = append(diffs, model.LargeFileDiff{
			Path:       path,
			SizeBefore: before,
			SizeAfter:  after,
			DeltaBytes: after - before,
			Status:     status,
			Tag:        tag,
		})
	}

	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].DeltaBytes != diffs[j].DeltaBytes {
			return diffs[i].DeltaBytes > diffs[j].DeltaBytes
		}
		return diffs[i].SizeAfter > diffs[j].SizeAfter
	})
	if len(diffs) > 50 {
		diffs = diffs[:50]
	}
	return diffs
}
