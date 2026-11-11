package convo

// group folds each stretch of work, approvals and text, between the items
// that part a turn (my messages, turn starts, errors, stops, change cards),
// into one work group up to its last step; the text after that step stays
// as it is. running says the last turn still runs: its last group runs
// unless text followed it.
func group(items []Item, running bool) []Item {
	items = codeItems(items)
	out := make([]Item, 0, len(items))
	for i := 0; i < len(items); {
		if !folds(items[i].Kind) {
			out = append(out, items[i])
			i++
			continue
		}
		j := i
		for j < len(items) && folds(items[j].Kind) {
			j++
		}
		out = append(out, fold(items[i:j])...)
		i = j
	}
	if !running {
		return out
	}
	for i := len(out) - 1; i >= 0; i-- {
		switch out[i].Kind {
		case KindWork:
			out[i].Work.Running = true
			return out
		case KindText:
			return out
		case KindMine, KindTurn:
			if !out[i].Steer {
				return out
			}
		}
	}
	return out
}

// codeItems leaves out the rows only a companion's chat draws; a memory
// saved shows in both.
func codeItems(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		switch it.Kind {
		case KindEvent:
			if it.Event.Memory == "" {
				continue
			}
		case KindReport:
			it.Kind = KindMine
		}
		out = append(out, it)
	}
	return out
}

func folds(k Kind) bool {
	return k == KindWork || k == KindApproval || k == KindText
}

// fold makes one stretch into a work group and the reply after it.
func fold(run []Item) []Item {
	last := -1
	for i, it := range run {
		if it.Kind != KindText {
			last = i
		}
	}
	if last < 0 {
		return run
	}
	g := Item{Kind: KindWork, Work: &Work{}}
	w := g.Work
	for _, it := range run[:last+1] {
		if g.Key == "" && it.Kind != KindText {
			// The first step's key, which a text before it does not change.
			g.Key, g.Time, g.Turn = it.Key, it.Time, it.Turn
		}
		switch it.Kind {
		case KindWork:
			w.Entries = append(w.Entries, it.Work.Entries...)
			w.Failed += it.Work.Failed
			if it.Work.End.After(w.End) {
				w.End = it.Work.End
			}
		case KindApproval:
			at := it.Approval.At
			if at.IsZero() {
				at = it.Time
			}
			w.Entries = append(w.Entries, Entry{Key: it.Key, Approval: it.Approval, Start: it.Time, End: at})
			if at.After(w.End) {
				w.End = at
			}
		case KindText:
			w.Entries = append(w.Entries, Entry{Key: it.Key, Note: it.Text, Start: it.Time, End: it.Time})
		}
	}
	w.Start = run[0].Time
	if w.End.Before(w.Start) {
		w.End = w.Start
	}
	return append([]Item{g}, run[last+1:]...)
}
