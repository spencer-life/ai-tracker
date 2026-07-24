package tui

import "github.com/charmbracelet/lipgloss"

// Catppuccin Frappe Palette Colors
var (
	ColorBase      = lipgloss.Color("#303446")
	ColorMantle    = lipgloss.Color("#292c3c")
	ColorCrust     = lipgloss.Color("#232634")
	ColorSurface0  = lipgloss.Color("#414559")
	ColorSurface1  = lipgloss.Color("#51576d")
	ColorSurface2  = lipgloss.Color("#626880")
	ColorOverlay0  = lipgloss.Color("#737994")
	ColorOverlay1  = lipgloss.Color("#838ba7")
	ColorOverlay2  = lipgloss.Color("#949cbb")
	ColorText      = lipgloss.Color("#c6d0f5")
	ColorSubtext0  = lipgloss.Color("#a5adce")
	ColorSubtext1  = lipgloss.Color("#b5bfe2")
	ColorRosewater = lipgloss.Color("#f2d5cf")
	ColorFlamingo  = lipgloss.Color("#eebebe")
	ColorPink      = lipgloss.Color("#f4b8e4")
	ColorMauve     = lipgloss.Color("#ca9ee6")
	ColorRed       = lipgloss.Color("#e78284")
	ColorMaroon    = lipgloss.Color("#ea999c")
	ColorPeach     = lipgloss.Color("#ef9f76")
	ColorYellow    = lipgloss.Color("#e5c890")
	ColorGreen     = lipgloss.Color("#a6d189")
	ColorTeal      = lipgloss.Color("#81c8be")
	ColorSky       = lipgloss.Color("#99d1db")
	ColorSapphire  = lipgloss.Color("#85c1dc")
	ColorBlue      = lipgloss.Color("#8caaee")
	ColorLavender  = lipgloss.Color("#babbf1")
)

// Lipgloss Catppuccin Frappe UI Styles
var (
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorMauve).
			Background(ColorMantle).
			Padding(0, 1)

	ActiveTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCrust).
			Background(ColorMauve).
			Padding(0, 2)

	InactiveTabStyle = lipgloss.NewStyle().
				Foreground(ColorSubtext0).
				Background(ColorSurface0).
				Padding(0, 2)

	CardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorSurface1).
			Background(ColorMantle).
			Padding(1, 2)

	ActiveCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorMauve).
			Background(ColorMantle).
			Padding(1, 2)

	HeaderStyle = lipgloss.NewStyle().
			Foreground(ColorMauve).
			Bold(true)

	MetricValStyle = lipgloss.NewStyle().
			Foreground(ColorGreen).
			Bold(true)

	MetricLabelStyle = lipgloss.NewStyle().
				Foreground(ColorSubtext0)

	StatusBarStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorSurface0).
			Padding(0, 1)

	HelpStyle = lipgloss.NewStyle().
			Foreground(ColorOverlay1)

	BadgeRunning = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCrust).
			Background(ColorGreen).
			Padding(0, 1)

	BadgeQueued = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorCrust).
			Background(ColorPeach).
			Padding(0, 1)
)
