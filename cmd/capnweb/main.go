// Command capnweb is an interactive REPL for calling methods on capnweb
// RPC servers.
//
// Usage:
//
//	capnweb ws://localhost:8080/ws
//	capnweb http://localhost:8080/rpc
//
// The REPL supports method calls, stub tracking, and dot-commands:
//
//	capnweb> Greet "World"
//	"Hello, World!"
//
//	capnweb> GetChild
//	$0 = Stub(-1)
//
//	capnweb> $0.ChildMethod
//	"from child"
//
//	capnweb> .stubs
//	capnweb> .release $0
//	capnweb> .help
//	capnweb> .quit
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	capnweb "github.com/flaticols/capnweb-go"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stubStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	resultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	infoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: capnweb <url>")
		fmt.Fprintln(os.Stderr, "  capnweb ws://localhost:8080/ws")
		fmt.Fprintln(os.Stderr, "  capnweb http://localhost:8080/rpc")
		os.Exit(1)
	}

	url := os.Args[1]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session, err := connect(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer session.Close()

	m := newModel(session, url)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func connect(ctx context.Context, url string) (*capnweb.Session, error) {
	if strings.HasPrefix(url, "ws://") || strings.HasPrefix(url, "wss://") {
		tr, err := capnweb.WSDial(ctx, url, nil)
		if err != nil {
			return nil, err
		}
		session := capnweb.NewSession(tr, nil)
		go session.Run(ctx)
		return session, nil
	}
	return nil, fmt.Errorf("unsupported URL scheme (use ws:// or wss://)")
}

// --- Bubble Tea model ---

type model struct {
	session *capnweb.Session
	url     string
	input   textinput.Model
	output  []string
	stubs   map[string]*capnweb.Stub
	nextVar int
	width   int
	height  int
}

func newModel(session *capnweb.Session, url string) model {
	ti := textinput.New()
	ti.Prompt = promptStyle.Render("capnweb> ")
	ti.Focus()
	ti.CharLimit = 512

	stubs := map[string]*capnweb.Stub{
		"$main": session.Main(),
	}

	return model{
		session: session,
		url:     url,
		input:   ti,
		output: []string{
			infoStyle.Render(fmt.Sprintf("Connected to %s", url)),
			infoStyle.Render("Type .help for commands, .quit to exit"),
			"",
		},
		stubs: stubs,
	}
}

type callResult struct {
	output string
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			return m, tea.Quit
		case "enter":
			line := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if line == "" {
				return m, nil
			}
			m.output = append(m.output, promptStyle.Render("capnweb> ")+line)
			return m, m.execute(line)
		}

	case callResult:
		m.output = append(m.output, msg.output)
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	// Show last N lines that fit
	maxLines := m.height - 2
	if maxLines < 1 {
		maxLines = 20
	}
	lines := m.output
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return strings.Join(lines, "\n") + "\n" + m.input.View()
}

func (m *model) execute(line string) tea.Cmd {
	// Dot-commands
	if strings.HasPrefix(line, ".") {
		return m.dotCommand(line)
	}
	return m.callCommand(line)
}

func (m *model) dotCommand(line string) tea.Cmd {
	parts := strings.Fields(line)
	cmd := parts[0]

	switch cmd {
	case ".quit", ".exit", ".q":
		return tea.Quit

	case ".help", ".h":
		return func() tea.Msg {
			help := []string{
				infoStyle.Render("Commands:"),
				"  Method arg1 arg2     Call method on $main",
				"  $0.Method arg1       Call method on stub $0",
				"  .stubs               List tracked stubs",
				"  .release $0          Release a stub",
				"  .help                Show this help",
				"  .quit                Exit",
				"",
				infoStyle.Render("Arg types:"),
				`  "string"  42  3.14  true  false  null`,
				`  {"key":"val"}  [1,2,3]`,
			}
			return callResult{output: strings.Join(help, "\n")}
		}

	case ".stubs", ".ls":
		return func() tea.Msg {
			var lines []string
			for name, stub := range m.stubs {
				label := ""
				if name == "$main" {
					label = "  [bootstrap]"
				}
				lines = append(lines, fmt.Sprintf("  %s\t%s%s", stubStyle.Render(name), stub.String(), label))
			}
			if len(lines) == 0 {
				lines = append(lines, infoStyle.Render("  (no stubs)"))
			}
			return callResult{output: strings.Join(lines, "\n")}
		}

	case ".release":
		if len(parts) < 2 {
			return func() tea.Msg {
				return callResult{output: errorStyle.Render("usage: .release $name")}
			}
		}
		name := parts[1]
		return func() tea.Msg {
			stub, ok := m.stubs[name]
			if !ok {
				return callResult{output: errorStyle.Render(fmt.Sprintf("unknown stub: %s", name))}
			}
			if name == "$main" {
				return callResult{output: errorStyle.Render("cannot release $main")}
			}
			_ = stub.Release(context.Background())
			delete(m.stubs, name)
			return callResult{output: infoStyle.Render(fmt.Sprintf("Released %s", name))}
		}

	default:
		return func() tea.Msg {
			return callResult{output: errorStyle.Render(fmt.Sprintf("unknown command: %s (try .help)", cmd))}
		}
	}
}

func (m *model) callCommand(line string) tea.Cmd {
	// Parse: "$stub.Method arg1 arg2" or "Method arg1 arg2"
	var stubName, method string
	var argStr string

	if idx := strings.IndexByte(line, ' '); idx > 0 {
		argStr = strings.TrimSpace(line[idx+1:])
		line = line[:idx]
	}

	if dotIdx := strings.LastIndexByte(line, '.'); dotIdx > 0 && strings.HasPrefix(line, "$") {
		stubName = line[:dotIdx]
		method = line[dotIdx+1:]
	} else {
		stubName = "$main"
		method = line
	}

	stub, ok := m.stubs[stubName]
	if !ok {
		return func() tea.Msg {
			return callResult{output: errorStyle.Render(fmt.Sprintf("unknown stub: %s", stubName))}
		}
	}

	args := parseArgs(argStr)

	return func() tea.Msg {
		ctx := context.Background()
		result, err := stub.Call(ctx, method, args...)
		if err != nil {
			return callResult{output: errorStyle.Render(err.Error())}
		}
		return callResult{output: m.formatResult(result)}
	}
}

func (m *model) formatResult(val any) string {
	if val == nil {
		return resultStyle.Render("null")
	}
	if stub, ok := val.(*capnweb.Stub); ok {
		name := fmt.Sprintf("$%d", m.nextVar)
		m.nextVar++
		m.stubs[name] = stub
		return stubStyle.Render(fmt.Sprintf("%s = %s", name, stub.String()))
	}
	data, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return resultStyle.Render(fmt.Sprintf("%v", val))
	}
	return resultStyle.Render(string(data))
}

func parseArgs(s string) []any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	var args []any
	for len(s) > 0 {
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}

		var arg any
		var consumed int

		switch {
		case s[0] == '"':
			// Quoted string
			end := 1
			for end < len(s) {
				if s[end] == '\\' {
					end += 2
					continue
				}
				if s[end] == '"' {
					end++
					break
				}
				end++
			}
			var str string
			if err := json.Unmarshal([]byte(s[:end]), &str); err == nil {
				arg = str
			}
			consumed = end

		case s[0] == '{' || s[0] == '[':
			// JSON object or array
			var raw json.RawMessage
			if err := json.Unmarshal([]byte(s), &raw); err == nil {
				var v any
				json.Unmarshal(raw, &v)
				arg = v
				consumed = len(s)
			} else {
				arg = s
				consumed = len(s)
			}

		case strings.HasPrefix(s, "true"):
			arg = true
			consumed = 4

		case strings.HasPrefix(s, "false"):
			arg = false
			consumed = 5

		case strings.HasPrefix(s, "null"):
			arg = nil
			consumed = 4

		default:
			// Number or unquoted token
			end := strings.IndexByte(s, ' ')
			if end < 0 {
				end = len(s)
			}
			token := s[:end]
			if n, err := strconv.ParseFloat(token, 64); err == nil {
				arg = n
			} else {
				arg = token
			}
			consumed = end
		}

		args = append(args, arg)
		s = s[consumed:]
	}

	return args
}
