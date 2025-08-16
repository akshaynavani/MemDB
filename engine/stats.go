package engine

// TableStats is a point-in-time snapshot of a table's shape and activity.
// Operation counters are cumulative since the table was created.
type TableStats struct {
	Name    string
	Keys    int
	Height  int
	Order   int
	Puts    uint64 // includes entries written through BatchPut
	Gets    uint64
	Hits    uint64 // Gets that found their key
	Deletes uint64
	Scans   uint64
	Batches uint64
}

// Stats returns a snapshot of the table's statistics.
func (t *Table) Stats() TableStats {
	t.mu.RLock()
	keys, height, order := t.tree.Len(), t.tree.Height(), t.tree.Order()
	t.mu.RUnlock()
	return TableStats{
		Name:    t.name,
		Keys:    keys,
		Height:  height,
		Order:   order,
		Puts:    t.puts.Load(),
		Gets:    t.gets.Load(),
		Hits:    t.hits.Load(),
		Deletes: t.deletes.Load(),
		Scans:   t.scans.Load(),
		Batches: t.batches.Load(),
	}
}

// Stats returns snapshots for every table, sorted by name.
func (e *Engine) Stats() []TableStats {
	names := e.ListTables()
	out := make([]TableStats, 0, len(names))
	for _, name := range names {
		t, err := e.Table(name)
		if err != nil {
			continue // dropped between listing and lookup
		}
		out = append(out, t.Stats())
	}
	return out
}
