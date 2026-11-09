// Package pages is the family's mini wiki: Markdown pages with per-page read
// and edit rights, revisions and tickable checklists.
package pages

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/tiliavir/allmend/internal/account"
	"github.com/tiliavir/allmend/internal/id"
)

// Page is one wiki page.
type Page struct {
	ID        string
	Slug      string
	Title     string
	Body      string // Markdown
	Version   int    // increases with every save
	OwnerID   string
	ReadAll   bool     // everyone with module access may read
	WriteAll  bool     // everyone with module access may edit
	Readers   []string // account ids, when !ReadAll
	Writers   []string // account ids, when !WriteAll
	CreatedAt time.Time
	UpdatedAt time.Time
	UpdatedBy string
}

// Errors.
var (
	ErrNotFound  = errors.New("page not found")
	ErrForbidden = errors.New("no access to page")
	ErrConflict  = errors.New("page changed meanwhile")
	ErrInvalid   = errors.New("page invalid")
	ErrNoTask    = errors.New("checklist item not found")
	ErrAmbiguous = errors.New("checklist item ambiguous")
)

// Limits.
const (
	MaxTitle = 120
	MaxBody  = 200_000
)

// CanRead reports whether a may read p.
func (p Page) CanRead(a account.Account) bool {
	return p.CanWrite(a) || p.ReadAll || slices.Contains(p.Readers, a.ID)
}

// CanWrite reports whether a may edit p.
func (p Page) CanWrite(a account.Account) bool {
	return p.OwnerID == a.ID || p.WriteAll || slices.Contains(p.Writers, a.ID)
}

// CanManage reports whether a may change rights or delete p.
func (p Page) CanManage(a account.Account) bool { return p.OwnerID == a.ID || a.IsAdmin }

// Revision is a saved state of a page.
type Revision struct {
	Version   int
	Title     string
	Body      string
	AuthorID  string
	CreatedAt time.Time
}

// Repository stores pages. Update must fail with ErrConflict when the stored
// version is not want; every successful Insert/Update also stores a revision.
type Repository interface {
	List(ctx context.Context) ([]Page, error) // without deleted pages, newest first
	BySlug(ctx context.Context, slug string) (Page, error)
	SlugExists(ctx context.Context, slug string) (bool, error)
	Insert(ctx context.Context, p Page) error
	Update(ctx context.Context, p Page, want int) error
	SetRights(ctx context.Context, p Page) error
	Delete(ctx context.Context, pageID, by string, at time.Time) error // soft delete
	Revisions(ctx context.Context, pageID string) ([]Revision, error)
}

// Service implements page use cases. Module access is checked by the caller.
type Service struct {
	Repo Repository
	Now  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Readable lists the pages a may read.
func (s *Service) Readable(ctx context.Context, a account.Account) ([]Page, error) {
	all, err := s.Repo.List(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(p Page) bool { return !p.CanRead(a) }), nil
}

// Get returns the page if a may read it.
func (s *Service) Get(ctx context.Context, a account.Account, slug string) (Page, error) {
	p, err := s.Repo.BySlug(ctx, slug)
	if err != nil {
		return p, err
	}
	if !p.CanRead(a) {
		return Page{}, ErrNotFound // do not reveal that the page exists
	}
	return p, nil
}

func validate(title, body string) (string, string, error) {
	title = strings.TrimSpace(title)
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if title == "" || len([]rune(title)) > MaxTitle || len(body) > MaxBody {
		return title, body, ErrInvalid
	}
	return title, body, nil
}

// Create adds a page owned by a, readable and editable by everyone with access.
func (s *Service) Create(ctx context.Context, a account.Account, title, body string) (Page, error) {
	title, body, err := validate(title, body)
	if err != nil {
		return Page{Title: title, Body: body}, err
	}
	slug, err := s.uniqueSlug(ctx, title)
	if err != nil {
		return Page{}, err
	}
	now := s.now()
	p := Page{ID: id.New(), Slug: slug, Title: title, Body: body, Version: 1, OwnerID: a.ID,
		ReadAll: true, WriteAll: true, CreatedAt: now, UpdatedAt: now, UpdatedBy: a.ID}
	return p, s.Repo.Insert(ctx, p)
}

// Save writes new title and body if the page is still at version base.
func (s *Service) Save(ctx context.Context, a account.Account, slug string, base int, title, body string) (Page, error) {
	p, err := s.Get(ctx, a, slug)
	if err != nil {
		return p, err
	}
	if !p.CanWrite(a) {
		return p, ErrForbidden
	}
	title, body, err = validate(title, body)
	if err != nil {
		return p, err
	}
	if p.Version != base {
		return p, ErrConflict
	}
	if title == p.Title && body == p.Body {
		return p, nil
	}
	p.Title, p.Body, p.Version, p.UpdatedAt, p.UpdatedBy = title, body, base+1, s.now(), a.ID
	return p, s.Repo.Update(ctx, p, base)
}

// Rights are who may read and edit a page.
type Rights struct {
	ReadAll, WriteAll bool
	Readers, Writers  []string
}

// SetRights changes who may read and edit; owner and admins only.
func (s *Service) SetRights(ctx context.Context, a account.Account, slug string, r Rights) (Page, error) {
	p, err := s.Get(ctx, a, slug)
	if err != nil {
		return p, err
	}
	if !p.CanManage(a) {
		return p, ErrForbidden
	}
	p.ReadAll, p.WriteAll = r.ReadAll || r.WriteAll, r.WriteAll
	p.Readers, p.Writers = nil, nil
	if !p.ReadAll {
		p.Readers = uniq(r.Readers)
	}
	if !p.WriteAll {
		p.Writers = uniq(r.Writers)
	}
	return p, s.Repo.SetRights(ctx, p)
}

// Delete hides a page; its revisions stay in the database.
func (s *Service) Delete(ctx context.Context, a account.Account, slug string) error {
	p, err := s.Get(ctx, a, slug)
	if err != nil {
		return err
	}
	if !p.CanManage(a) {
		return ErrForbidden
	}
	return s.Repo.Delete(ctx, p.ID, a.ID, s.now())
}

// Revisions lists a page's saved states, newest first.
func (s *Service) Revisions(ctx context.Context, a account.Account, slug string) (Page, []Revision, error) {
	p, err := s.Get(ctx, a, slug)
	if err != nil {
		return p, nil, err
	}
	rs, err := s.Repo.Revisions(ctx, p.ID)
	return p, rs, err
}

// Toggle ticks or unticks the checklist item on line (0-based) whose text is
// text. If the page changed since version, the item is found by its text.
func (s *Service) Toggle(ctx context.Context, a account.Account, slug string, version, line int, text string) (Page, error) {
	p, err := s.Get(ctx, a, slug)
	if err != nil {
		return p, err
	}
	if !p.CanWrite(a) {
		return p, ErrForbidden
	}
	lines := strings.Split(p.Body, "\n")
	target := -1
	if p.Version == version && line >= 0 && line < len(lines) {
		if t, ok := TaskText(lines[line]); ok && t == text {
			target = line
		}
	}
	if target < 0 {
		for i, l := range lines {
			if t, ok := TaskText(l); ok && t == text {
				if target >= 0 {
					return p, ErrAmbiguous
				}
				target = i
			}
		}
	}
	if target < 0 {
		return p, ErrNoTask
	}
	lines[target] = toggleLine(lines[target])
	base := p.Version
	p.Body, p.Version, p.UpdatedAt, p.UpdatedBy = strings.Join(lines, "\n"), base+1, s.now(), a.ID
	return p, s.Repo.Update(ctx, p, base)
}

// TaskText returns the text of a Markdown task list item ("- [ ] text").
func TaskText(line string) (string, bool) {
	i := taskBox(line)
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(line[i+3:]), true
}

// taskBox returns the index of "[" of the checkbox, or -1.
func taskBox(line string) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t' || line[i] == '>') {
		i++
	}
	switch {
	case i < len(line) && strings.ContainsRune("-*+", rune(line[i])):
		i++
	default:
		j := i
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j == i || j >= len(line) || (line[j] != '.' && line[j] != ')') {
			return -1
		}
		i = j + 1
	}
	if i >= len(line) || (line[i] != ' ' && line[i] != '\t') {
		return -1
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i+2 >= len(line) || line[i] != '[' || line[i+2] != ']' || !strings.ContainsRune(" xX", rune(line[i+1])) {
		return -1
	}
	if i+3 < len(line) && line[i+3] != ' ' && line[i+3] != '\t' {
		return -1
	}
	return i
}

func toggleLine(line string) string {
	i := taskBox(line)
	mark := byte('x')
	if line[i+1] != ' ' {
		mark = ' '
	}
	return line[:i+1] + string(mark) + line[i+2:]
}

func (s *Service) uniqueSlug(ctx context.Context, title string) (string, error) {
	base := Slugify(title)
	slug := base
	for n := 2; ; n++ {
		exists, err := s.Repo.SlugExists(ctx, slug)
		if err != nil {
			return "", err
		}
		if !exists && !reserved[slug] {
			return slug, nil
		}
		slug = base + "-" + itoa(n)
	}
}

var reserved = map[string]bool{"neu": true}

// Slugify makes a URL-safe lowercase slug ("Netzwerk & NAS" → "netzwerk-nas").
func Slugify(s string) string {
	r := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "ae", "Ö", "oe", "Ü", "ue", "ß", "ss")
	s = strings.ToLower(r.Replace(s))
	var b strings.Builder
	dash := false
	for _, c := range s {
		switch {
		case c < unicode.MaxASCII && (unicode.IsLetter(c) || unicode.IsDigit(c)):
			b.WriteRune(c)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if len(out) > 60 {
		out = strings.TrimSuffix(out[:60], "-")
	}
	if out == "" {
		return "seite"
	}
	return out
}

func itoa(n int) string {
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func uniq(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}
