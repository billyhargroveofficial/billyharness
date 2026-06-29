package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	tuirender "github.com/billyhargroveofficial/billyharness/internal/tui/render"
)

func (m Model) styles() themeStyles {
	theme, ok := tuiThemes[m.theme]
	if !ok {
		theme = tuiThemes["light"]
	}
	return newThemeStyles(theme)
}

var (
	tuiThemes = map[string]tuiTheme{
		"light": {
			background:      "#F7F3EA",
			foreground:      "#1D1B16",
			headerFg:        "#1D1B16",
			headerBg:        "#E9DFC9",
			statusFg:        "#2D3524",
			statusBg:        "#DDE8D7",
			footerFg:        "#6F675C",
			footerBg:        "#E9DFC9",
			inputFg:         "#1D1B16",
			inputBg:         "#FFFDF8",
			mutedFg:         "#6F675C",
			inputBorder:     "#CFC3AF",
			blockBorder:     "#CFC3AF",
			blockBg:         "#FFFDF8",
			userBg:          "#ECECEC",
			userBorder:      "#B9B9B9",
			userFg:          "#222222",
			assistantBg:     "#FFFDF8",
			assistantBorder: "#D8D1C3",
			assistantFg:     "#1D1B16",
			reasoningBg:     "#F4E6C4",
			reasoningFg:     "#4A3512",
			reasoningBorder: "#D2A747",
			toolBg:          "#EDF0E6",
			toolFg:          "#2D3524",
			toolBorder:      "#A4B27C",
			errorBg:         "#F8DAD3",
			errorFg:         "#5A1D15",
			errorBorder:     "#C86552",
		},
		"dark": {
			background:      "#050505",
			foreground:      "#E7E7E7",
			headerFg:        "#E7E7E7",
			headerBg:        "#111111",
			statusFg:        "#E7E7E7",
			statusBg:        "#050505",
			footerFg:        "#8A8A8A",
			footerBg:        "#050505",
			inputFg:         "#F2F2F2",
			inputBg:         "#0B0B0B",
			mutedFg:         "#8A8A8A",
			inputBorder:     "#303030",
			blockBorder:     "#2B2B2B",
			blockBg:         "#080808",
			userBg:          "#161616",
			userBorder:      "#4A4A4A",
			userFg:          "#D8D8D8",
			assistantBg:     "#080808",
			assistantBorder: "#2B2B2B",
			assistantFg:     "#F0F0F0",
			reasoningBg:     "#171104",
			reasoningFg:     "#FFDFA3",
			reasoningBorder: "#F59E0B",
			toolBg:          "#0D1208",
			toolFg:          "#E7F6D4",
			toolBorder:      "#84CC16",
			errorBg:         "#210909",
			errorFg:         "#FFD1D1",
			errorBorder:     "#F87171",
		},
	}
)

type tuiTheme struct {
	background      string
	foreground      string
	headerFg        string
	headerBg        string
	statusFg        string
	statusBg        string
	footerFg        string
	footerBg        string
	inputFg         string
	inputBg         string
	mutedFg         string
	inputBorder     string
	blockBorder     string
	blockBg         string
	userBg          string
	userBorder      string
	userFg          string
	assistantBg     string
	assistantBorder string
	assistantFg     string
	reasoningBg     string
	reasoningFg     string
	reasoningBorder string
	toolBg          string
	toolFg          string
	toolBorder      string
	errorBg         string
	errorFg         string
	errorBorder     string
}

type themeStyles struct {
	background      string
	foreground      string
	header          lipgloss.Style
	status          lipgloss.Style
	footer          lipgloss.Style
	input           lipgloss.Style
	runStatus       lipgloss.Style
	popup           lipgloss.Style
	popupLine       lipgloss.Style
	popupMuted      lipgloss.Style
	popupSelected   lipgloss.Style
	statusState     lipgloss.Style
	statusModel     lipgloss.Style
	statusReasoning lipgloss.Style
	statusAccess    lipgloss.Style
	statusUsage     lipgloss.Style
	statusCost      lipgloss.Style
	statusDim       lipgloss.Style
	statusSeparator lipgloss.Style
	selection       lipgloss.Style
	activity        tuirender.ActivityStyles
	markdown        tuirender.TerminalMarkdownStyles
	textarea        textarea.Styles
	block           lipgloss.Style
	user            lipgloss.Style
	assistant       lipgloss.Style
	reasoning       lipgloss.Style
	tool            lipgloss.Style
	error           lipgloss.Style
	statusBlock     lipgloss.Style
}

func newThemeStyles(theme tuiTheme) themeStyles {
	text := lipgloss.Color(theme.foreground)
	inputText := lipgloss.Color(theme.inputFg)
	inputBg := lipgloss.Color(theme.inputBg)
	muted := lipgloss.Color(theme.mutedFg)
	statusBg := lipgloss.Color(theme.statusBg)
	block := func(fg, bg, border string) lipgloss.Style {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(fg)).
			Background(lipgloss.Color(bg)).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color(border)).
			Padding(0, 0).
			MarginBottom(1)
	}
	textareaStyles := textarea.DefaultLightStyles()
	baseInput := lipgloss.NewStyle().
		Foreground(inputText).
		Background(inputBg)
	textareaStyles.Focused.Base = baseInput
	textareaStyles.Focused.Text = baseInput
	textareaStyles.Focused.CursorLine = baseInput
	textareaStyles.Focused.Placeholder = lipgloss.NewStyle().
		Foreground(muted).
		Background(inputBg)
	textareaStyles.Focused.Prompt = lipgloss.NewStyle().
		Foreground(muted).
		Background(inputBg)
	textareaStyles.Focused.EndOfBuffer = lipgloss.NewStyle().
		Foreground(inputBg).
		Background(inputBg)
	textareaStyles.Focused.LineNumber = baseInput
	textareaStyles.Focused.CursorLineNumber = baseInput
	textareaStyles.Blurred = textareaStyles.Focused
	textareaStyles.Cursor.Color = inputText
	return themeStyles{
		background: theme.background,
		foreground: theme.foreground,
		header: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.headerFg)).
			Background(lipgloss.Color(theme.headerBg)).
			Bold(true),
		status: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.statusFg)).
			Background(statusBg).
			Padding(0, 1),
		footer: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.footerFg)).
			Background(lipgloss.Color(theme.footerBg)).
			Padding(0, 1),
		input: lipgloss.NewStyle().
			Foreground(inputText).
			Background(inputBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(theme.inputBorder)).
			Padding(0, 0),
		runStatus: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.statusFg)).
			Background(statusBg).
			Bold(true),
		popup: lipgloss.NewStyle().
			Foreground(text).
			Background(lipgloss.Color(theme.inputBg)).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(theme.inputBorder)).
			Padding(0, 0),
		popupLine: lipgloss.NewStyle().
			Foreground(text).
			Background(lipgloss.Color(theme.inputBg)),
		popupMuted: lipgloss.NewStyle().
			Foreground(muted).
			Background(lipgloss.Color(theme.inputBg)),
		popupSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.headerFg)).
			Background(lipgloss.Color(theme.headerBg)).
			Bold(false),
		statusState: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(statusBg).
			Bold(true),
		statusModel: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#BFE7FF")).
			Background(statusBg),
		statusReasoning: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.reasoningBorder)).
			Background(statusBg),
		statusAccess: lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.userBorder)).
			Background(statusBg),
		statusUsage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E7D7A9")).
			Background(statusBg),
		statusCost: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#B7E4C7")).
			Background(statusBg),
		statusDim: lipgloss.NewStyle().
			Foreground(muted).
			Background(statusBg),
		statusSeparator: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666")).
			Background(statusBg),
		selection: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#050505")).
			Background(lipgloss.Color("#FFD166")).
			Bold(true),
		activity: tuirender.ActivityStyles{
			Text: lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.foreground)),
			Guide: lipgloss.NewStyle().
				Foreground(muted),
			Tool: lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.toolBorder)).
				Bold(true),
			Reasoning: lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.reasoningBorder)).
				Bold(true),
			Error: lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.errorBorder)).
				Bold(true),
			Status: lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.statusFg)).
				Bold(true),
		},
		markdown: tuirender.TerminalMarkdownStyleSet(tuirender.MarkdownTheme{
			AssistantForeground: theme.assistantFg,
			ToolForeground:      theme.toolFg,
			ToolBackground:      theme.toolBg,
			ToolBorder:          theme.toolBorder,
			BlockBorder:         theme.blockBorder,
			MutedForeground:     theme.mutedFg,
		}),
		textarea:    textareaStyles,
		block:       block(theme.foreground, theme.blockBg, theme.blockBorder),
		user:        block(theme.userFg, theme.userBg, theme.userBorder),
		assistant:   block(theme.assistantFg, theme.assistantBg, theme.assistantBorder),
		reasoning:   block(theme.reasoningFg, theme.reasoningBg, theme.reasoningBorder),
		tool:        block(theme.toolFg, theme.toolBg, theme.toolBorder),
		error:       block(theme.errorFg, theme.errorBg, theme.errorBorder),
		statusBlock: block(theme.statusFg, theme.statusBg, theme.statusFg),
	}
}
