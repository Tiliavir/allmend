package pages

import (
	"errors"
	"slices"
	"strings"
)

// A subpage is never readable by more people than its parent
// (ADR-20261121-page-rights-inheritance). Edit rights are free within that.

// Errors of the rule.
var (
	ErrWider  = errors.New("page readable by more than its parent")
	ErrManage = errors.New("only the owner may narrow the rights of a page")
)

// LockedError refuses a change that would leave owners unable to read their
// own pages. Pages are the subpages concerned.
type LockedError struct{ Pages []Page }

func (e *LockedError) Error() string {
	ids := make([]string, len(e.Pages))
	for i, p := range e.Pages {
		ids[i] = p.ID
	}
	return "owners would lose read access to pages " + strings.Join(ids, ", ")
}

// Audience is who may read a page: everyone with access to Seiten, or the
// listed accounts (always including the owner).
type Audience struct {
	All bool
	IDs map[string]bool
}

// Has reports whether account id is in the audience.
func (x Audience) Has(id string) bool { return x.All || x.IDs[id] }

func (x Audience) within(y Audience) bool {
	if y.All {
		return true
	}
	if x.All {
		return false
	}
	for id := range x.IDs {
		if !y.IDs[id] {
			return false
		}
	}
	return true
}

// Audience returns who may read p.
func (p Page) Audience() Audience {
	if p.ReadAll || p.WriteAll {
		return Audience{All: true}
	}
	ids := map[string]bool{p.OwnerID: true}
	for _, id := range append(slices.Clone(p.Readers), p.Writers...) {
		ids[id] = true
	}
	return Audience{IDs: ids}
}

// fit narrows p's rights to limit, keeping as much as limit allows: "all"
// becomes everyone in limit. It reports whether anything changed. The caller
// checks that the owner is in limit.
func fit(p Page, limit Audience) (Page, bool) {
	if p.Audience().within(limit) {
		return p, false
	}
	everyone := make([]string, 0, len(limit.IDs))
	for id := range limit.IDs {
		if id != p.OwnerID {
			everyone = append(everyone, id)
		}
	}
	slices.Sort(everyone)
	keep := func(ids []string, all bool) []string {
		if all {
			return slices.Clone(everyone)
		}
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !limit.IDs[id] })
	}
	readAll := p.ReadAll || p.WriteAll
	p.Writers = keep(p.Writers, p.WriteAll)
	p.Readers = keep(p.Readers, readAll)
	if p.WriteAll {
		p.Readers = nil // every reader is a writer
	}
	p.ReadAll, p.WriteAll = false, false
	return p, true
}

// cascade fits the pages below root to their parents, top down, with root's
// rights already as given. It returns the pages it changed and the pages whose
// owner would no longer be allowed to read them.
func cascade(all []Page, root Page) (changed, locked []Page) {
	kids := map[string][]Page{}
	for _, p := range all {
		if p.ParentID != "" && p.ID != root.ID {
			kids[p.ParentID] = append(kids[p.ParentID], p)
		}
	}
	queue := []Page{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		limit := parent.Audience()
		for _, c := range kids[parent.ID] {
			if !limit.Has(c.OwnerID) {
				locked = append(locked, c)
			}
			c, ch := fit(c, limit)
			if ch {
				changed = append(changed, c)
			}
			queue = append(queue, c)
		}
	}
	return changed, locked
}

// ChildRights are the rights a new page below p starts with: as wide as p
// allows for a page owned by owner.
func (p Page) ChildRights(owner string) Rights {
	c, _ := fit(Page{OwnerID: owner, ReadAll: true, WriteAll: true}, p.Audience())
	return Rights{ReadAll: c.ReadAll, WriteAll: c.WriteAll, Readers: c.Readers, Writers: c.Writers}
}
