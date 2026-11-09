package migrations

// v9 arranges pages in a tree: a page may have a parent page and has a
// position among its siblings. Existing pages become top-level pages in
// alphabetical order.
const v9 = `
ALTER TABLE pages ADD COLUMN parent_id TEXT REFERENCES pages(id) CHECK (parent_id != id);
ALTER TABLE pages ADD COLUMN position INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0);
UPDATE pages SET position = (
	SELECT COUNT(*) FROM pages q
	WHERE q.title < pages.title OR (q.title = pages.title AND q.id < pages.id)
);
CREATE INDEX pages_parent ON pages(parent_id, position);
`
