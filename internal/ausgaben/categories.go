package ausgaben

import "strings"

// Categories are a fixed list: subcategories under seven main categories,
// the same set Splitwise uses. Only the subcategory key is stored; the main
// category follows from it. Keys are English and never change (a new key
// needs a schema step, see migrations v12); names are German.

// Main is a main category ("Bereich").
type Main struct {
	Key  string
	Name string
	Subs []Sub
}

// Sub is a subcategory, the value stored with an expense.
type Sub struct {
	Key  string
	Main string // key of the main category
	Name string
	// Splitwise is the name a Splitwise export uses for it.
	Splitwise string
}

// Mains lists the main categories in display order with their subcategories.
var Mains = []Main{
	{Key: "food_drink", Name: "Essen & Trinken", Subs: []Sub{
		{"groceries", "food_drink", "Lebensmittel", "Groceries"},
		{"dining_out", "food_drink", "Auswärts essen", "Dining out"},
		{"liquor", "food_drink", "Alkohol", "Liquor"},
		{"food_other", "food_drink", "Sonstiges", "Food and drink - Other"},
	}},
	{Key: "home", Name: "Wohnen", Subs: []Sub{
		{"rent", "home", "Miete", "Rent"},
		{"mortgage", "home", "Hypothek", "Mortgage"},
		{"furniture", "home", "Möbel", "Furniture"},
		{"electronics", "home", "Elektronik", "Electronics"},
		{"household_supplies", "home", "Haushaltsartikel", "Household supplies"},
		{"maintenance", "home", "Unterhalt", "Maintenance"},
		{"pets", "home", "Haustiere", "Pets"},
		{"services", "home", "Dienstleistungen", "Services"},
		{"home_other", "home", "Sonstiges", "Home - Other"},
	}},
	{Key: "utilities", Name: "Nebenkosten", Subs: []Sub{
		{"electricity", "utilities", "Strom", "Electricity"},
		{"heat_gas", "utilities", "Heizung/Gas", "Heat/gas"},
		{"water", "utilities", "Wasser", "Water"},
		{"trash", "utilities", "Abfall", "Trash"},
		{"cleaning", "utilities", "Reinigung", "Cleaning"},
		{"tv_phone_internet", "utilities", "TV/Telefon/Internet", "TV/Phone/Internet"},
		{"utilities_other", "utilities", "Sonstiges", "Utilities - Other"},
	}},
	{Key: "transportation", Name: "Verkehr", Subs: []Sub{
		{"car", "transportation", "Auto", "Car"},
		{"gas_fuel", "transportation", "Benzin", "Gas/fuel"},
		{"parking", "transportation", "Parkieren", "Parking"},
		{"bus_train", "transportation", "Bus/Bahn", "Bus/train"},
		{"bicycle", "transportation", "Velo", "Bicycle"},
		{"taxi", "transportation", "Taxi", "Taxi"},
		{"plane", "transportation", "Flug", "Plane"},
		{"hotel", "transportation", "Hotel", "Hotel"},
		{"transportation_other", "transportation", "Sonstiges", "Transportation - Other"},
	}},
	{Key: "entertainment", Name: "Freizeit", Subs: []Sub{
		{"movies", "entertainment", "Filme", "Movies"},
		{"music", "entertainment", "Musik", "Music"},
		{"games", "entertainment", "Spiele", "Games"},
		{"sports", "entertainment", "Sport", "Sports"},
		{"entertainment_other", "entertainment", "Sonstiges", "Entertainment - Other"},
	}},
	{Key: "life", Name: "Leben", Subs: []Sub{
		{"clothing", "life", "Kleidung", "Clothing"},
		{"gifts", "life", "Geschenke", "Gifts"},
		{"medical", "life", "Gesundheit", "Medical expenses"},
		{"insurance", "life", "Versicherung", "Insurance"},
		{"childcare", "life", "Kinderbetreuung", "Childcare"},
		{"education", "life", "Bildung", "Education"},
		{"taxes", "life", "Steuern", "Taxes"},
		{"life_other", "life", "Sonstiges", "Life - Other"},
	}},
	{Key: "general", Name: "Allgemein", Subs: []Sub{
		{"general", "general", "Allgemein", "General"},
	}},
}

var (
	subsByKey       = map[string]Sub{}
	subsBySplitwise = map[string]Sub{}
	mainsByKey      = map[string]Main{}
)

func init() {
	for _, m := range Mains {
		mainsByKey[m.Key] = m
		for _, s := range m.Subs {
			if s.Main != m.Key {
				panic("ausgaben: sub " + s.Key + " listed under " + m.Key)
			}
			if _, dup := subsByKey[s.Key]; dup {
				panic("ausgaben: duplicate category " + s.Key)
			}
			subsByKey[s.Key] = s
			subsBySplitwise[s.Splitwise] = s
		}
	}
	// Splitwise files an expense without a category as "General"; older
	// exports call the main category "Uncategorized". A bare main category
	// name means its "Other".
	subsBySplitwise["Uncategorized"] = subsByKey["general"]
	for _, k := range []string{"food_other", "home_other", "utilities_other", "transportation_other", "entertainment_other", "life_other"} {
		sub := subsByKey[k]
		subsBySplitwise[strings.TrimSuffix(sub.Splitwise, " - Other")] = sub
	}
}

// Category returns the subcategory with key k.
func Category(k string) (Sub, bool) {
	s, ok := subsByKey[k]
	return s, ok
}

// MainOf returns the main category of subcategory key k.
func MainOf(k string) (Main, bool) {
	s, ok := subsByKey[k]
	if !ok {
		return Main{}, false
	}
	return mainsByKey[s.Main], true
}

// MainByKey returns main category k.
func MainByKey(k string) (Main, bool) {
	m, ok := mainsByKey[k]
	return m, ok
}

// FromSplitwise maps a category name of a Splitwise export.
func FromSplitwise(name string) (Sub, bool) {
	s, ok := subsBySplitwise[name]
	return s, ok
}
