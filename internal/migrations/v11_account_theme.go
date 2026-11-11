package migrations

// v11 stores the colour scheme each account chose. Existing accounts follow
// the device setting, as before.
const v11 = `
ALTER TABLE accounts ADD COLUMN theme TEXT NOT NULL DEFAULT 'auto' CHECK (theme IN ('auto', 'light', 'dark'));
`
