package migrations

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/tiliavir/allmend/internal/db"
)

// v12 turns the free-text category of Ausgaben into a fixed list
// (ADR-20261109-ausgaben): a table of the 43 subcategory keys, every
// existing value mapped to one of them, and triggers that refuse anything
// else from now on.
//
// The mapping is a fixed table (v12Map): the keys themselves, the names of a
// Splitwise export, and the German names shown in the app (case-insensitive).
// An expense without a category becomes "general" ("Allgemein" is Splitwise's
// "uncategorized"). Any other value aborts the migration and names every
// unmapped value, so they can be added here before the release; nothing is
// guessed. Payments must have no category.
//
// The list is frozen here on purpose: a later change to the Go catalog needs
// its own step.
func v12(ctx context.Context, tx db.Tx, _ db.MigrationEnv) error {
	if _, err := tx.ExecContext(ctx, v12Schema); err != nil {
		return err
	}
	for _, c := range v12Cats {
		if _, err := tx.ExecContext(ctx, `INSERT INTO expense_categories (key, main) VALUES (?, ?)`, c[0], c[1]); err != nil {
			return err
		}
	}
	var payCats int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM expense_entries WHERE kind = 'zahlung' AND category != ''`).Scan(&payCats); err != nil {
		return err
	}
	if payCats > 0 {
		return fmt.Errorf("v12: %d payments carry a category", payCats)
	}
	var unmapped []string
	for _, table := range []string{"expense_entries", "expense_recurring"} {
		where := ""
		if table == "expense_entries" {
			where = " WHERE kind = 'ausgabe'"
		}
		vals, err := v12Distinct(ctx, tx, `SELECT DISTINCT category FROM `+table+where)
		if err != nil {
			return err
		}
		for _, v := range vals {
			key, ok := v12Lookup(v)
			if !ok {
				if !slices.Contains(unmapped, v) {
					unmapped = append(unmapped, v)
				}
				continue
			}
			if key == v {
				continue
			}
			upd := `UPDATE ` + table + ` SET category = ? WHERE category = ?` + strings.ReplaceAll(where, "WHERE", "AND")
			if _, err := tx.ExecContext(ctx, upd, key, v); err != nil {
				return err
			}
		}
	}
	if len(unmapped) > 0 {
		slices.Sort(unmapped)
		return fmt.Errorf("v12: categories without a mapping (add them to v12Map): %q", unmapped)
	}
	_, err := tx.ExecContext(ctx, v12Triggers)
	return err
}

func v12Distinct(ctx context.Context, tx db.Tx, q string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// v12Lookup maps an old category value to a key.
func v12Lookup(v string) (string, bool) {
	t := strings.TrimSpace(v)
	if t == "" {
		return "general", true
	}
	for _, c := range v12Cats {
		if t == c[0] {
			return t, true
		}
	}
	if k, ok := v12Map[t]; ok {
		return k, true
	}
	for name, k := range v12Map {
		if strings.EqualFold(name, t) {
			return k, true
		}
	}
	return "", false
}

const v12Schema = `
CREATE TABLE expense_categories (
	key  TEXT NOT NULL PRIMARY KEY,
	main TEXT NOT NULL CHECK (main IN ('food_drink', 'home', 'utilities', 'transportation', 'entertainment', 'life', 'general'))
) STRICT;
`

const v12Triggers = `
CREATE TRIGGER expense_entries_category_insert BEFORE INSERT ON expense_entries
WHEN (NEW.kind = 'ausgabe' AND NEW.category NOT IN (SELECT key FROM expense_categories))
  OR (NEW.kind = 'zahlung' AND NEW.category != '')
BEGIN SELECT RAISE(ABORT, 'expense_entries: invalid category'); END;

CREATE TRIGGER expense_entries_category_update BEFORE UPDATE OF kind, category ON expense_entries
WHEN (NEW.kind = 'ausgabe' AND NEW.category NOT IN (SELECT key FROM expense_categories))
  OR (NEW.kind = 'zahlung' AND NEW.category != '')
BEGIN SELECT RAISE(ABORT, 'expense_entries: invalid category'); END;

CREATE TRIGGER expense_recurring_category_insert BEFORE INSERT ON expense_recurring
WHEN NEW.category NOT IN (SELECT key FROM expense_categories)
BEGIN SELECT RAISE(ABORT, 'expense_recurring: invalid category'); END;

CREATE TRIGGER expense_recurring_category_update BEFORE UPDATE OF category ON expense_recurring
WHEN NEW.category NOT IN (SELECT key FROM expense_categories)
BEGIN SELECT RAISE(ABORT, 'expense_recurring: invalid category'); END;
`

// v12Cats are the subcategory keys with their main category.
var v12Cats = [][2]string{
	{"groceries", "food_drink"}, {"dining_out", "food_drink"}, {"liquor", "food_drink"}, {"food_other", "food_drink"},
	{"rent", "home"}, {"mortgage", "home"}, {"furniture", "home"}, {"electronics", "home"}, {"household_supplies", "home"},
	{"maintenance", "home"}, {"pets", "home"}, {"services", "home"}, {"home_other", "home"},
	{"electricity", "utilities"}, {"heat_gas", "utilities"}, {"water", "utilities"}, {"trash", "utilities"},
	{"cleaning", "utilities"}, {"tv_phone_internet", "utilities"}, {"utilities_other", "utilities"},
	{"car", "transportation"}, {"gas_fuel", "transportation"}, {"parking", "transportation"}, {"bus_train", "transportation"},
	{"bicycle", "transportation"}, {"taxi", "transportation"}, {"plane", "transportation"}, {"hotel", "transportation"},
	{"transportation_other", "transportation"},
	{"movies", "entertainment"}, {"music", "entertainment"}, {"games", "entertainment"}, {"sports", "entertainment"},
	{"entertainment_other", "entertainment"},
	{"clothing", "life"}, {"gifts", "life"}, {"medical", "life"}, {"insurance", "life"}, {"childcare", "life"},
	{"education", "life"}, {"taxes", "life"}, {"life_other", "life"},
	{"general", "general"},
}

// v12Map maps old category values (Splitwise export names and German names,
// matched case-insensitively) to keys.
var v12Map = map[string]string{
	// Splitwise export.
	"Groceries": "groceries", "Dining out": "dining_out", "Liquor": "liquor", "Food and drink": "food_other",
	"Rent": "rent", "Mortgage": "mortgage", "Furniture": "furniture", "Electronics": "electronics",
	"Household supplies": "household_supplies", "Maintenance": "maintenance", "Pets": "pets", "Services": "services", "Home": "home_other",
	"Electricity": "electricity", "Heat/gas": "heat_gas", "Water": "water", "Trash": "trash", "Cleaning": "cleaning",
	"TV/Phone/Internet": "tv_phone_internet", "Utilities": "utilities_other",
	"Car": "car", "Gas/fuel": "gas_fuel", "Parking": "parking", "Bus/train": "bus_train", "Bicycle": "bicycle",
	"Taxi": "taxi", "Plane": "plane", "Hotel": "hotel", "Transportation": "transportation_other",
	"Movies": "movies", "Music": "music", "Games": "games", "Sports": "sports", "Entertainment": "entertainment_other",
	"Clothing": "clothing", "Gifts": "gifts", "Medical expenses": "medical", "Insurance": "insurance",
	"Childcare": "childcare", "Education": "education", "Taxes": "taxes", "Life": "life_other",
	"General": "general", "Uncategorized": "general",
	// German names as shown in the app (main categories map to their "Sonstiges").
	"Lebensmittel": "groceries", "Auswärts essen": "dining_out", "Alkohol": "liquor", "Essen & Trinken": "food_other",
	"Miete": "rent", "Hypothek": "mortgage", "Möbel": "furniture", "Elektronik": "electronics",
	"Haushaltsartikel": "household_supplies", "Unterhalt": "maintenance", "Haustiere": "pets",
	"Dienstleistungen": "services", "Wohnen": "home_other",
	"Strom": "electricity", "Heizung/Gas": "heat_gas", "Wasser": "water", "Abfall": "trash", "Reinigung": "cleaning",
	"TV/Telefon/Internet": "tv_phone_internet", "Nebenkosten": "utilities_other",
	"Auto": "car", "Benzin": "gas_fuel", "Parkieren": "parking", "Bus/Bahn": "bus_train", "Velo": "bicycle",
	"Flug": "plane", "Verkehr": "transportation_other",
	"Filme": "movies", "Musik": "music", "Spiele": "games", "Sport": "sports", "Freizeit": "entertainment_other",
	"Kleidung": "clothing", "Geschenke": "gifts", "Gesundheit": "medical", "Versicherung": "insurance",
	"Kinderbetreuung": "childcare", "Bildung": "education", "Steuern": "taxes", "Leben": "life_other",
	"Allgemein": "general",
}
