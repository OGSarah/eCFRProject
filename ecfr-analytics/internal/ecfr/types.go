package ecfr

// Title models a CFR title from the versioner API.
type Title struct {
	Number       int    `json:"number"`
	Name         string `json:"name"`
	UpToDateAsOf string `json:"up_to_date_as_of"`
	Reserved     bool   `json:"reserved"`
}

// Agency models an agency entry from the admin API.
type Agency struct {
	Name          string        `json:"name"`
	Slug          string        `json:"slug"`
	Children      []Agency      `json:"children"`
	CFRReferences []CFRRef      `json:"cfr_references"`
	DisplayName   string        `json:"display_name"`
	ShortName     string        `json:"short_name"`
}

// CFRRef points to a referenced title/chapter for an agency.
type CFRRef struct {
	Title   int    `json:"title"`
	Chapter string `json:"chapter,omitempty"`  // e.g., "I"
	Subtitle string `json:"subtitle,omitempty"`// some agencies reference subtitle
}
