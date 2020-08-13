package ui

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/charm"
	"github.com/charmbracelet/charm/ui/common"
	"github.com/charmbracelet/glamour"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/ansi"
	te "github.com/muesli/termenv"
)

const (
	maxDocumentWidth = 120
	statusBarHeight  = 1
	gray             = "#333333"
	yellowGreen      = "#ECFD65"
	fuschia          = "#EE6FF8"
	noteHeadingText  = " Set Memo "
	notePromptText   = " > "
)

var (
	pagerHelpHeight = strings.Count(pagerHelpView(pagerModel{}, 0), "\n")

	noteHeading = te.String(noteHeadingText).
			Foreground(common.Cream.Color()).
			Background(common.Green.Color()).
			String()

	statusBarBg          = common.NewColorPair("#242424", "#E6E6E6")
	statusBarNoteFg      = common.NewColorPair("#7D7D7D", "#656565")
	statusBarScrollPosFg = common.NewColorPair("#5A5A5A", "#949494")

	statusBarScrollPosStyle = te.Style{}.
				Foreground(statusBarScrollPosFg.Color()).
				Background(statusBarBg.Color()).
				Styled

	statusBarNoteStyle = te.Style{}.
				Foreground(statusBarNoteFg.Color()).
				Background(statusBarBg.Color()).
				Styled

	statusBarHelpStyle = te.Style{}.
				Foreground(statusBarNoteFg.Color()).
				Background(common.NewColorPair("#323232", "#DCDCDC").Color()).
				Styled

	helpViewStyle = te.Style{}.
			Foreground(statusBarNoteFg.Color()).
			Background(common.NewColorPair("#1B1B1B", "#f2f2f2").Color()).
			Styled
)

// MSG

type contentRenderedMsg string
type noteSavedMsg *charm.Markdown

// MODEL

type pagerState int

const (
	pagerStateBrowse pagerState = iota
	pagerStateSetNote
)

type pagerModel struct {
	cc           *charm.Client
	viewport     viewport.Model
	state        pagerState
	glamourStyle string
	width        int
	height       int
	textInput    textinput.Model
	showHelp     bool

	// Current document being rendered, sans-glamour rendering. We cache
	// it here so we can re-render it on resize.
	currentDocument *markdown
}

func newPagerModel(glamourStyle string) pagerModel {

	// Init viewport
	vp := viewport.Model{}
	vp.YPosition = 0
	vp.HighPerformanceRendering = config.HighPerformancePager

	// Init text input UI for notes/memos
	ti := textinput.NewModel()
	ti.Prompt = te.String(notePromptText).
		Foreground(te.ColorProfile().Color(gray)).
		Background(te.ColorProfile().Color(yellowGreen)).
		String()
	ti.TextColor = gray
	ti.BackgroundColor = yellowGreen
	ti.CursorColor = fuschia
	ti.CharLimit = noteCharacterLimit
	ti.Focus()

	return pagerModel{
		state:        pagerStateBrowse,
		glamourStyle: glamourStyle,
		textInput:    ti,
		viewport:     vp,
	}
}

func (m *pagerModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = h - statusBarHeight
	m.textInput.Width = w - len(noteHeadingText) - len(notePromptText) - 1

	if m.showHelp {
		m.viewport.Height -= (statusBarHeight + pagerHelpHeight)
	}
}

func (m *pagerModel) setContent(s string) {
	m.viewport.SetContent(s)
}

func (m *pagerModel) toggleHelp() {
	m.showHelp = !m.showHelp
	m.setSize(m.width, m.height)
	if m.viewport.PastBottom() {
		m.viewport.GotoBottom()
	}
}

func (m *pagerModel) unload() {
	if m.showHelp {
		m.toggleHelp()
	}
	m.state = pagerStateBrowse
	m.viewport.SetContent("")
	m.viewport.YOffset = 0
	m.textInput.Reset()
}

// UPDATE

func pagerUpdate(msg tea.Msg, m pagerModel) (pagerModel, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch m.state {
		case pagerStateSetNote:
			switch msg.String() {
			case "esc":
				m.state = pagerStateBrowse
				return m, nil
			case "enter":
				var cmd tea.Cmd
				if m.textInput.Value() != m.currentDocument.Note { // don't update if the note didn't change
					m.currentDocument.Note = m.textInput.Value() // update optimistically
					cmd = saveDocumentNote(m.cc, m.currentDocument.ID, m.currentDocument.Note)
				}
				m.state = pagerStateBrowse
				m.textInput.Reset()
				return m, cmd
			}
		default:
			switch msg.String() {
			case "q", "esc":
				if m.state != pagerStateBrowse {
					m.state = pagerStateBrowse
					return m, nil
				}
			case "m":
				// Users can only set the note on user-stashed markdown
				if m.currentDocument.markdownType != stashedMarkdown {
					break
				}

				m.state = pagerStateSetNote
				if m.textInput.Value() == "" {
					// Pre-populate note with existing value
					m.textInput.SetValue(m.currentDocument.Note)
					m.textInput.CursorEnd()
				}
				return m, textinput.Blink(m.textInput)
			case "?":
				m.toggleHelp()
				if m.viewport.HighPerformanceRendering {
					cmds = append(cmds, viewport.Sync(m.viewport))
				}
			}
		}

	// Glow has rendered the content
	case contentRenderedMsg:
		m.setContent(string(msg))
		if m.viewport.HighPerformanceRendering {
			cmds = append(cmds, viewport.Sync(m.viewport))
		}

	// We've reveived terminal dimensions, either for the first time or
	// after a resize
	case tea.WindowSizeMsg:
		var cmd tea.Cmd
		if m.currentDocument != nil {
			cmd = renderWithGlamour(m, m.currentDocument.Body)
		}
		return m, cmd
	}

	switch m.state {
	case pagerStateSetNote:
		m.textInput, cmd = textinput.Update(msg, m.textInput)
		cmds = append(cmds, cmd)
	default:
		m.viewport, cmd = viewport.Update(msg, m.viewport)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// VIEW

func pagerView(m pagerModel) string {
	var b strings.Builder

	fmt.Fprint(&b, viewport.View(m.viewport)+"\n")

	// Footer
	if m.state == pagerStateSetNote {
		pagerSetNoteView(&b, m)
	} else {
		pagerStatusBarView(&b, m)
	}

	if m.showHelp {
		fmt.Fprintf(&b, pagerHelpView(m, m.width))
	}

	return b.String()
}

func pagerStatusBarView(b *strings.Builder, m pagerModel) {
	// Logo
	logo := glowLogoView(" Glow ")

	// Scroll percent
	scrollPercent := math.Max(0.0, math.Min(1.0, m.viewport.ScrollPercent()))
	percentText := fmt.Sprintf(" %3.f%% ", scrollPercent*100)

	// "Help" note
	helpNote := statusBarHelpStyle(" ? Help ")

	// Note
	noteText := m.currentDocument.Note
	if len(noteText) == 0 {
		noteText = "(No title)"
	}
	noteText = truncate(" "+noteText+" ", max(0,
		m.width-
			ansi.PrintableRuneWidth(logo)-
			ansi.PrintableRuneWidth(percentText)-
			ansi.PrintableRuneWidth(helpNote),
	))

	// Empty space
	emptyCell := te.String(" ").Background(statusBarBg.Color()).String()
	padding := max(0,
		m.width-
			ansi.PrintableRuneWidth(logo)-
			ansi.PrintableRuneWidth(noteText)-
			ansi.PrintableRuneWidth(percentText)-
			ansi.PrintableRuneWidth(helpNote),
	)
	emptySpace := strings.Repeat(emptyCell, padding)

	fmt.Fprintf(b, "%s%s%s%s%s",
		logo,
		statusBarNoteStyle(noteText),
		emptySpace,
		statusBarScrollPosStyle(percentText),
		helpNote,
	)
}

func pagerSetNoteView(b *strings.Builder, m pagerModel) {
	fmt.Fprint(b, noteHeading)
	fmt.Fprint(b, textinput.View(m.textInput))
}

func pagerHelpView(m pagerModel, width int) (s string) {
	col1 := [...]string{
		"m       set memo",
		"esc     back to files",
		"q       quit",
	}
	if m.currentDocument != nil && m.currentDocument.markdownType != stashedMarkdown {
		col1[0] = col1[1]
		col1[1] = col1[2]
		col1[2] = ""
	}

	s += "\n"
	s += "k/↑      up                  " + col1[0] + "\n"
	s += "j/↓      down                " + col1[1] + "\n"
	s += "j/↓      down                " + col1[2] + "\n"
	s += "b/pgup   page up\n"
	s += "f/pgdn   page down\n"
	s += "u        ½ page up\n"
	s += "d        ½ page down"

	s = indent(s, 2)

	// Fill up empty cells with spaces for background coloring
	if width > 0 {
		lines := strings.Split(s, "\n")
		for i := 0; i < len(lines); i++ {
			l := runewidth.StringWidth(lines[i])
			n := max(width-l, 0)
			lines[i] += strings.Repeat(" ", n)
		}

		s = strings.Join(lines, "\n")
	}

	return helpViewStyle(s)
}

// CMD

func renderWithGlamour(m pagerModel, md string) tea.Cmd {
	return func() tea.Msg {
		s, err := glamourRender(m, md)
		if err != nil {
			return errMsg(err)
		}
		return contentRenderedMsg(s)
	}
}

func saveDocumentNote(cc *charm.Client, id int, note string) tea.Cmd {
	if cc == nil {
		return func() tea.Msg {
			return errMsg(errors.New("can't set note; no charm client"))
		}
	}
	return func() tea.Msg {
		if err := cc.SetMarkdownNote(id, note); err != nil {
			return errMsg(err)
		}
		return noteSavedMsg(&charm.Markdown{ID: id, Note: note})
	}
}

// This is where the magic happens
func glamourRender(m pagerModel, markdown string) (string, error) {

	if !config.GlamourEnabled {
		return markdown, nil
	}

	// initialize glamour
	var gs glamour.TermRendererOption
	if m.glamourStyle == "auto" {
		gs = glamour.WithAutoStyle()
	} else {
		gs = glamour.WithStylePath(m.glamourStyle)
	}

	width := max(0, min(maxDocumentWidth, m.viewport.Width))
	r, err := glamour.NewTermRenderer(
		gs,
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}

	out, err := r.Render(markdown)
	if err != nil {
		return "", err
	}

	// trim lines
	lines := strings.Split(string(out), "\n")

	var content string
	for i, s := range lines {
		content += strings.TrimSpace(s)

		// don't add an artificial newline after the last split
		if i+1 < len(lines) {
			content += "\n"
		}
	}

	return content, nil
}
