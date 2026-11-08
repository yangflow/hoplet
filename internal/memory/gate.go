package memory

// takesEffect reports whether a non-conflicting write becomes active at once
// rather than waiting for the user. old is the target's scope; nil on create.
func takesEffect(w Write, category Category, old *Scope, scope Scope) bool {
	if w.Actor == ActorUser {
		return true
	}
	if w.Actor == ActorExtraction && w.Action == ActionWithdraw {
		return false
	}
	if old != nil && scope != *old && old.Kind != ScopeGlobal {
		return false
	}
	var stated, verified bool
	for _, ev := range w.Evidence {
		stated = stated || ev.Kind == EvidenceUserStated
		verified = verified || ev.Kind == EvidenceVerified
	}
	return stated || (category == CategoryExperience && verified && scope.Kind != ScopeGlobal)
}
