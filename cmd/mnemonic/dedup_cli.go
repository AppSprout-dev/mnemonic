package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/agentutil"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// dedupCommand scans active memories for near-duplicate clusters and archives duplicates.
// With --apply it modifies the DB; without it, it's a dry-run that reports what would change.
func dedupCommand(configPath string, dryRun bool) {
	cfg, db, log := initBase(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	threshold := float32(cfg.Encoding.DeduplicationThreshold)
	if threshold <= 0 {
		threshold = 0.9
	}

	if dryRun {
		fmt.Printf("Dedup dry-run (threshold: %.2f). Use --apply to execute.\n\n", threshold)
	} else {
		fmt.Printf("Dedup (threshold: %.2f). Archiving duplicates...\n\n", threshold)
	}

	// Load all active memories in pages
	var allMemories []store.Memory
	offset := 0
	pageSize := 200
	for {
		page, err := db.ListMemories(ctx, "active", pageSize, offset)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load memories: %v\n", err)
			os.Exit(1)
		}
		allMemories = append(allMemories, page...)
		if len(page) < pageSize {
			break
		}
		offset += pageSize
	}

	// Filter to memories with embeddings
	var withEmbeddings []store.Memory
	for _, m := range allMemories {
		if len(m.Embedding) > 0 {
			withEmbeddings = append(withEmbeddings, m)
		}
	}

	fmt.Printf("Active memories: %d (%d with embeddings)\n", len(allMemories), len(withEmbeddings))

	// Union-find clustering: for each pair above threshold, merge clusters
	clusterOf := make(map[string]string) // memory ID → cluster representative ID
	for i := range withEmbeddings {
		clusterOf[withEmbeddings[i].ID] = withEmbeddings[i].ID
	}

	// Find root of cluster (with path compression)
	var find func(string) string
	find = func(id string) string {
		if clusterOf[id] != id {
			clusterOf[id] = find(clusterOf[id])
		}
		return clusterOf[id]
	}

	// Union two IDs into the same cluster
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			clusterOf[ra] = rb
		}
	}

	// O(n^2) pairwise comparison — fine for <1000 memories
	comparisons := 0
	for i := 0; i < len(withEmbeddings); i++ {
		for j := i + 1; j < len(withEmbeddings); j++ {
			sim := agentutil.CosineSimilarity(withEmbeddings[i].Embedding, withEmbeddings[j].Embedding)
			comparisons++
			if sim >= threshold {
				union(withEmbeddings[i].ID, withEmbeddings[j].ID)
			}
		}
	}

	// Build clusters
	clusters := make(map[string][]store.Memory) // representative ID → members
	for _, m := range withEmbeddings {
		root := find(m.ID)
		clusters[root] = append(clusters[root], m)
	}

	// Filter to clusters with more than 1 member (actual duplicates)
	dupClusters := 0
	totalDups := 0
	totalArchived := 0
	totalAssocTransferred := 0

	for _, members := range clusters {
		if len(members) <= 1 {
			continue
		}
		dupClusters++
		totalDups += len(members)

		// Pick survivor: highest salience, then most recently accessed, then newest
		survivor := members[0]
		for _, m := range members[1:] {
			if m.Salience > survivor.Salience {
				survivor = m
			} else if m.Salience == survivor.Salience && m.LastAccessed.After(survivor.LastAccessed) {
				survivor = m
			} else if m.Salience == survivor.Salience && m.LastAccessed.Equal(survivor.LastAccessed) && m.CreatedAt.After(survivor.CreatedAt) {
				survivor = m
			}
		}

		fmt.Printf("Cluster (%d members):\n", len(members))
		fmt.Printf("  Survivor: %s (salience=%.2f) %s\n", survivor.ID[:8], survivor.Salience, truncate(survivor.Summary, 60))
		for _, m := range members {
			if m.ID == survivor.ID {
				continue
			}
			fmt.Printf("  Archive:  %s (salience=%.2f) %s\n", m.ID[:8], m.Salience, truncate(m.Summary, 60))

			if !dryRun {
				// Transfer associations from archived memory to survivor
				assocs, err := db.GetAssociations(ctx, m.ID)
				if err != nil {
					log.Warn("failed to get associations", "memory_id", m.ID, "error", err)
				} else {
					for _, a := range assocs {
						targetID := a.TargetID
						if targetID == m.ID {
							targetID = a.SourceID
						}
						if targetID == survivor.ID {
							continue // skip self-association
						}
						newAssoc := store.Association{
							SourceID:      survivor.ID,
							TargetID:      targetID,
							Strength:      a.Strength,
							RelationType:  a.RelationType,
							CreatedAt:     a.CreatedAt,
							LastActivated: a.LastActivated,
						}
						if err := db.CreateAssociation(ctx, newAssoc); err != nil {
							// Likely duplicate — ignore
							log.Debug("association transfer skipped (likely exists)", "source", survivor.ID[:8], "target", targetID[:8])
						} else {
							totalAssocTransferred++
						}
					}
				}

				// Archive the duplicate
				if err := db.UpdateState(ctx, m.ID, "archived"); err != nil {
					log.Warn("failed to archive duplicate", "memory_id", m.ID, "error", err)
				} else {
					totalArchived++
				}
			}
		}
		fmt.Println()
	}

	fmt.Printf("Summary:\n")
	fmt.Printf("  Comparisons:  %d\n", comparisons)
	fmt.Printf("  Dup clusters: %d (%d memories)\n", dupClusters, totalDups)
	if dryRun {
		fmt.Printf("  Would archive: %d memories\n", totalDups-dupClusters)
		fmt.Printf("\nRun with --apply to execute.\n")
	} else {
		fmt.Printf("  Archived:     %d memories\n", totalArchived)
		fmt.Printf("  Associations: %d transferred\n", totalAssocTransferred)

		// Clean up dangling associations pointing to archived memories
		pruned, err := db.PruneOrphanedAssociations(ctx)
		if err != nil {
			log.Warn("failed to prune orphaned associations", "error", err)
		} else {
			fmt.Printf("  Orphaned assocs pruned: %d\n", pruned)
		}
	}
}

// resetPatternsCommand recalculates pattern strengths using logarithmic scaling
// and merges near-duplicate patterns. Dry-run by default; use --apply to execute.
func resetPatternsCommand(configPath string, dryRun bool) {
	_, db, log := initBase(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Load all patterns (no project filter, high limit)
	patterns, err := db.ListPatterns(ctx, "", 1000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load patterns: %v\n", err)
		os.Exit(1)
	}

	if dryRun {
		fmt.Printf("Pattern reset dry-run. Use --apply to execute.\n\n")
	} else {
		fmt.Printf("Pattern reset. Recalculating strengths and merging duplicates...\n\n")
	}

	fmt.Printf("Total patterns: %d\n\n", len(patterns))

	// Phase 1: Recalculate strengths using logarithmic formula
	strengthCeiling := float32(0.95)
	strongCeiling := float32(1.0)
	strongMinCount := 50

	fmt.Printf("=== Strength Recalculation ===\n")
	fmt.Printf("Formula: 0.5 + 0.03 * log2(1 + evidenceCount)\n")
	fmt.Printf("Ceiling: %.2f (%.2f with %d+ evidence)\n\n", strengthCeiling, strongCeiling, strongMinCount)

	recalculated := 0
	for i := range patterns {
		p := &patterns[i]
		if p.State != "active" {
			continue
		}
		evidenceCount := len(p.EvidenceIDs)
		newStrength := float32(0.5) + 0.03*float32(math.Log2(1+float64(evidenceCount)))
		ceiling := strengthCeiling
		if evidenceCount > strongMinCount {
			ceiling = strongCeiling
		}
		if newStrength > ceiling {
			newStrength = ceiling
		}
		if newStrength != p.Strength {
			fmt.Printf("  %-50s  evidence=%3d  %.2f -> %.2f\n",
				truncate(p.Title, 50), evidenceCount, p.Strength, newStrength)
			if !dryRun {
				p.Strength = newStrength
				p.UpdatedAt = time.Now()
				if err := db.UpdatePattern(ctx, *p); err != nil {
					log.Warn("failed to update pattern strength", "pattern_id", p.ID, "error", err)
				}
			}
			recalculated++
		}
	}
	fmt.Printf("\nRecalculated: %d patterns\n\n", recalculated)

	// Phase 2: Merge near-duplicate patterns (>0.80 cosine similarity)
	const mergeThreshold = float32(0.80)
	fmt.Printf("=== Duplicate Pattern Merge (threshold: %.2f) ===\n\n", mergeThreshold)

	// Filter to active patterns with embeddings
	var active []int
	for i, p := range patterns {
		if p.State == "active" && len(p.Embedding) > 0 {
			active = append(active, i)
		}
	}

	// Union-find for pattern clustering
	parent := make(map[int]int)
	for _, i := range active {
		parent[i] = i
	}
	var findRoot func(int) int
	findRoot = func(i int) int {
		if parent[i] != i {
			parent[i] = findRoot(parent[i])
		}
		return parent[i]
	}

	for ai := 0; ai < len(active); ai++ {
		for bi := ai + 1; bi < len(active); bi++ {
			i, j := active[ai], active[bi]
			sim := agentutil.CosineSimilarity(patterns[i].Embedding, patterns[j].Embedding)
			if sim >= mergeThreshold {
				ri, rj := findRoot(i), findRoot(j)
				if ri != rj {
					parent[ri] = rj
				}
			}
		}
	}

	// Build clusters
	patternClusters := make(map[int][]int)
	for _, i := range active {
		root := findRoot(i)
		patternClusters[root] = append(patternClusters[root], i)
	}

	merged := 0
	for _, members := range patternClusters {
		if len(members) <= 1 {
			continue
		}

		// Pick survivor: most evidence, then highest strength
		survivorIdx := members[0]
		for _, idx := range members[1:] {
			if len(patterns[idx].EvidenceIDs) > len(patterns[survivorIdx].EvidenceIDs) {
				survivorIdx = idx
			} else if len(patterns[idx].EvidenceIDs) == len(patterns[survivorIdx].EvidenceIDs) &&
				patterns[idx].Strength > patterns[survivorIdx].Strength {
				survivorIdx = idx
			}
		}

		survivor := &patterns[survivorIdx]
		fmt.Printf("Cluster (%d patterns):\n", len(members))
		fmt.Printf("  Survivor: %s (evidence=%d)\n", truncate(survivor.Title, 60), len(survivor.EvidenceIDs))

		for _, idx := range members {
			if idx == survivorIdx {
				continue
			}
			dup := &patterns[idx]
			fmt.Printf("  Archive:  %s (evidence=%d)\n", truncate(dup.Title, 60), len(dup.EvidenceIDs))

			if !dryRun {
				// Merge evidence IDs into survivor
				existingEvidence := make(map[string]bool)
				for _, eid := range survivor.EvidenceIDs {
					existingEvidence[eid] = true
				}
				for _, eid := range dup.EvidenceIDs {
					if !existingEvidence[eid] {
						survivor.EvidenceIDs = append(survivor.EvidenceIDs, eid)
					}
				}
				survivor.UpdatedAt = time.Now()
				if err := db.UpdatePattern(ctx, *survivor); err != nil {
					log.Warn("failed to update survivor pattern", "id", survivor.ID, "error", err)
				}

				// Archive the duplicate
				dup.State = "archived"
				dup.UpdatedAt = time.Now()
				if err := db.UpdatePattern(ctx, *dup); err != nil {
					log.Warn("failed to archive duplicate pattern", "id", dup.ID, "error", err)
				}
			}
			merged++
		}
		fmt.Println()
	}

	fmt.Printf("Summary:\n")
	fmt.Printf("  Strengths recalculated: %d\n", recalculated)
	if dryRun {
		fmt.Printf("  Would merge: %d duplicate patterns\n", merged)
		fmt.Printf("\nRun with --apply to execute.\n")
	} else {
		fmt.Printf("  Patterns merged: %d\n", merged)
	}
}
