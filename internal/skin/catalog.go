package skin

// Catalog returns the first-party skins. IDs are stable machine identifiers
// that may be stored as preferences: never rename or remove one without a
// migration. Display names are English defaults until UI localization lands.
func Catalog() map[string]Definition {
	return map[string]Definition{
		// Calm, minimal; default for guardian surfaces.
		"classic": {
			Version: SchemaVersion, ID: "classic", Name: "Classic",
			Colors: Colors{
				Background: "#ffffff", Surface: "#f3f5fa", Text: "#141a2e", TextMuted: "#5c6378", Line: "#dde1ea",
				Accent: "#3355aa", OnAccent: "#ffffff", AccentText: "#3355aa",
				OwnBubble: "#3355aa", OnOwnBubble: "#ffffff", OtherBubble: "#eef1f7", OnOtherBubble: "#141a2e",
				Badge: "#3355aa", OnBadge: "#ffffff", DangerText: "#b3261e", Focus: "#3355aa",
			},
			Typography: Typography{Heading: "system", Body: "system", BaseSize: 16},
			Shape:      Shape{Radius: "soft", Bubble: "flat"},
			Wallpaper:  "none", Avatar: "initials", Density: "comfortable", Motion: "calm",
		},
		// Warm and soft; continuity with the v1 playful palette.
		"playful": {
			Version: SchemaVersion, ID: "playful", Name: "Playful",
			Colors: Colors{
				Background: "#fff7e8", Surface: "#ffecce", Text: "#272044", TextMuted: "#5f5575", Line: "#ecd3b4",
				Accent: "#7140a0", OnAccent: "#ffffff", AccentText: "#7140a0",
				OwnBubble: "#7140a0", OnOwnBubble: "#ffffff", OtherBubble: "#ffecce", OnOtherBubble: "#272044",
				Badge: "#7140a0", OnBadge: "#ffffff", DangerText: "#a3231a", Focus: "#7140a0",
			},
			Typography: Typography{Heading: "nunito", Body: "nunito", BaseSize: 17},
			Shape:      Shape{Radius: "round", Bubble: "flat"},
			Wallpaper:  "none", Avatar: "figure", Density: "comfortable", Motion: "standard",
		},
		// "Bunt": bold outlines, offset shadows, dotted wallpaper.
		"vivid": {
			Version: SchemaVersion, ID: "vivid", Name: "Vivid",
			Colors: Colors{
				Background: "#fbf7ee", Surface: "#ffffff", Text: "#1c2540", TextMuted: "#5a6078", Line: "#e4dccb",
				Accent: "#6856de", OnAccent: "#ffffff", AccentText: "#4f3dc0",
				OwnBubble: "#6856de", OnOwnBubble: "#ffffff", OtherBubble: "#ffffff", OnOtherBubble: "#1c2540",
				Badge: "#d9f692", OnBadge: "#1c2540", DangerText: "#b3261e", Focus: "#6856de",
			},
			Typography: Typography{Heading: "bricolage", Body: "jakarta", BaseSize: 16},
			Shape:      Shape{Radius: "soft", Bubble: "raised"},
			Wallpaper:  "dots", Avatar: "figure", Density: "comfortable", Motion: "standard",
		},
		// "Nacht": dark and understated.
		"night": {
			Version: SchemaVersion, ID: "night", Name: "Night",
			Colors: Colors{
				Background: "#1c2540", Surface: "#283255", Text: "#ffffff", TextMuted: "#b9bed3", Line: "#3a4570",
				Accent: "#d9f692", OnAccent: "#1c2540", AccentText: "#d9f692",
				OwnBubble: "#6856de", OnOwnBubble: "#ffffff", OtherBubble: "#283255", OnOtherBubble: "#ffffff",
				Badge: "#d9f692", OnBadge: "#1c2540", DangerText: "#ffb4ab", Focus: "#d9f692",
			},
			Typography: Typography{Heading: "jakarta", Body: "jakarta", BaseSize: 16},
			Shape:      Shape{Radius: "round", Bubble: "flat"},
			Wallpaper:  "none", Avatar: "figure", Density: "comfortable", Motion: "calm",
		},
		// "Wolke": soft and large type, intended for younger readers.
		"cloud": {
			Version: SchemaVersion, ID: "cloud", Name: "Cloud",
			Colors: Colors{
				Background: "#fff4ef", Surface: "#ffffff", Text: "#1c2540", TextMuted: "#5a6078", Line: "#ffd3c8",
				Accent: "#6856de", OnAccent: "#ffffff", AccentText: "#4f3dc0",
				OwnBubble: "#bdece2", OnOwnBubble: "#1c2540", OtherBubble: "#ffffff", OnOtherBubble: "#1c2540",
				Badge: "#6856de", OnBadge: "#ffffff", DangerText: "#b3261e", Focus: "#6856de",
			},
			Typography: Typography{Heading: "nunito", Body: "nunito", BaseSize: 18},
			Shape:      Shape{Radius: "round", Bubble: "flat"},
			Wallpaper:  "none", Avatar: "figure", Density: "comfortable", Motion: "calm",
		},
		// "Terminal": monospace, square, outlined.
		"terminal": {
			Version: SchemaVersion, ID: "terminal", Name: "Terminal",
			Colors: Colors{
				Background: "#0f1115", Surface: "#1a1e25", Text: "#e8ecf2", TextMuted: "#9aa3b2", Line: "#2c333f",
				Accent: "#d9f692", OnAccent: "#0f1115", AccentText: "#d9f692",
				OwnBubble: "#d9f692", OnOwnBubble: "#0f1115", OtherBubble: "#1a1e25", OnOtherBubble: "#e8ecf2",
				Badge: "#d9f692", OnBadge: "#0f1115", DangerText: "#ff8a80", Focus: "#d9f692",
			},
			Typography: Typography{Heading: "mono", Body: "mono", BaseSize: 16},
			Shape:      Shape{Radius: "square", Bubble: "outlined"},
			Wallpaper:  "lines", Avatar: "initials", Density: "compact", Motion: "calm",
		},
	}
}
