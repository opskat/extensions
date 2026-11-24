package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// Run SQL File (dbdriver.CapRunSQLFile). The host reads the file and hands the
// splitter one chunk at a time; the statements run in order on one connection,
// each through the connection's ExecContext, which splits a text once more with
// splitOracle (exec.go) and finds each of these statements whole.
//
// Statements end as splitOracle ends them: a plain statement at ';' (not sent),
// a PL/SQL block at a '/' line (its ';' sent). The file is a SQL*Plus or SQL
// Developer script, so the client commands that start a line between statements
// are SQL*Plus's: the ones that only change what the client shows are skipped and
// logged, EXECUTE runs as an anonymous block, and the ones that change what runs
// (another file, error handling, another connection, substitution variables)
// stop the run as a construct to run with SQL*Plus.

var _ dbdriver.ScriptSplitter = dialect{}

// sqlPlus is the tool a script that stops on a client command needs.
const sqlPlus = "SQL*Plus"

// SplitScript splits a chunk of a SQL file, logging each skipped client command.
func (dialect) SplitScript(_ context.Context, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	return splitScriptChunk(chunk, func(s skippedCommand) {
		opskat.Log("info", fmt.Sprintf("oracle: run SQL file: skipped the SQL*Plus command %s on line %d", s.Command, s.Line))
	})
}

// skippedCommand is a client command the splitter skipped and the line it is on.
type skippedCommand struct {
	Line    int
	Command string
}

// scriptState is what a split carries to the next chunk.
type scriptState struct {
	// Define is the character SQL*Plus substitutes variables after: "&" until a
	// SET DEFINE changes it, "" after SET DEFINE OFF.
	Define string `json:"define"`
	// Line is the line of the file the chunk's text starts on.
	Line int `json:"line"`
}

func decodeScriptState(s string) (scriptState, error) {
	if s == "" {
		return scriptState{Define: "&", Line: 1}, nil
	}
	var state scriptState
	if err := json.Unmarshal([]byte(s), &state); err != nil {
		return scriptState{}, fmt.Errorf("oracle: run SQL file: bad splitter state: %w", err)
	}
	return state, nil
}

// splitScriptChunk splits chunk into the statements to run, reporting each
// skipped client command to skipped.
func splitScriptChunk(chunk dbdriver.ScriptChunk, skipped func(skippedCommand)) (*dbdriver.ScriptSplit, error) {
	state, err := decodeScriptState(chunk.State)
	if err != nil {
		return nil, err
	}
	text := chunk.Text
	m := newMasker(text, chunk.Final)
	lines := lineCounter{text: text, line: state.Line}
	split := &dbdriver.ScriptSplit{Statements: []dbdriver.ScriptStatement{}}
	consumed := 0
	// emit hands statement on for the file's text at offset, or stops at the
	// substitution variable that text uses.
	emit := func(source string, offset int, statement string) bool {
		if variable, at, ok := substitutionVariable(source, state.Define); ok {
			split.Unsupported = &dbdriver.ScriptUnsupported{Construct: variable, Tool: sqlPlus, Offset: offset + at}
			return false
		}
		split.Statements = append(split.Statements, dbdriver.ScriptStatement{Text: statement, Offset: offset})
		return true
	}
	for pos := 0; ; {
		start := m.skipBlank(pos)
		if start >= len(text) {
			if chunk.Final {
				consumed = len(text)
			}
			break
		}
		if start < m.limit() && m.atLineStart(start) {
			end := lineEnd(text, start)
			if end == len(text) && !chunk.Final {
				break // the command's line goes on in the next chunk
			}
			if cmd, ok := clientCommandAt(text[start:end]); ok {
				switch cmd.kind {
				case commandStop:
					split.Unsupported = &dbdriver.ScriptUnsupported{Construct: cmd.name, Tool: sqlPlus, Offset: start}
					return split, nil
				case commandExec:
					if !emit(text[start:end], start, "BEGIN "+cmd.body+"; END;") {
						return split, nil
					}
				case commandSkip:
					if cmd.name == "SET" {
						state.Define = setDefine(cmd.body, state.Define)
					}
					skipped(skippedCommand{Line: lines.at(start), Command: cmd.name})
				}
				m.restart(end)
				pos, consumed = end, end
				continue
			}
		}
		if end, ok := m.loneSlashAt(start); ok {
			skipped(skippedCommand{Line: lines.at(start), Command: "/"})
			pos, consumed = end, end
			continue
		}
		end, next, closed := m.statementAt(start)
		if !closed {
			if !chunk.Final {
				break // the statement goes on in the next chunk
			}
			if m.open >= 0 {
				// An unterminated quote or comment: the rest of the file is the last
				// statement, and the connection reports where it cannot be split.
				end, next = len(strings.TrimRight(text, " \t\r\n")), len(text)
			}
		}
		if end > start && !emit(text[start:end], start, text[start:end]) {
			return split, nil
		}
		pos, consumed = next, next
	}
	state.Line = lines.at(consumed)
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("oracle: run SQL file: encode splitter state: %w", err)
	}
	split.Consumed, split.State = consumed, string(encoded)
	return split, nil
}

// lineCounter gives the file line of byte positions of text, asked in order.
type lineCounter struct {
	text      string
	pos, line int
}

func (c *lineCounter) at(pos int) int {
	c.line += strings.Count(c.text[c.pos:pos], "\n")
	c.pos = pos
	return c.line
}

type commandKind int

const (
	commandSkip commandKind = iota // changes only what the client shows
	commandExec                    // EXECUTE: runs its body as an anonymous block
	commandStop                    // changes what runs: SQL*Plus has to run the file
)

// sqlPlusCommands are the SQL*Plus commands the splitter knows, each with the
// shortest abbreviation SQL*Plus accepts.
var sqlPlusCommands = []struct {
	name     string
	shortest int
	kind     commandKind
}{
	{"SET", 3, commandSkip}, {"PROMPT", 3, commandSkip}, {"REMARK", 3, commandSkip}, {"SPOOL", 3, commandSkip},
	{"CLEAR", 2, commandSkip}, {"COLUMN", 3, commandSkip}, {"TTITLE", 3, commandSkip}, {"BTITLE", 3, commandSkip},
	{"SHOW", 3, commandSkip},
	{"EXECUTE", 4, commandExec},
	{"START", 3, commandStop}, {"WHENEVER", 8, commandStop}, {"CONNECT", 4, commandStop}, {"EXIT", 4, commandStop},
	{"QUIT", 4, commandStop}, {"HOST", 2, commandStop}, {"DEFINE", 3, commandStop}, {"UNDEFINE", 5, commandStop},
	{"ACCEPT", 3, commandStop}, {"VARIABLE", 3, commandStop},
}

// sqlSetStatements are the SQL statements that start with SET (SET TRANSACTION,
// SET ROLE, SET CONSTRAINT[S]), which are not SQL*Plus's SET.
var sqlSetStatements = map[string]bool{"TRANSACTION": true, "ROLE": true, "CONSTRAINT": true, "CONSTRAINTS": true}

// clientCommand is a SQL*Plus command line: its full name, what it does, and
// its body (the line after the command word, trailing ';' removed).
type clientCommand struct {
	name string
	kind commandKind
	body string
}

// clientCommandAt reads the line (from its first non-blank byte, newline
// excluded) as a SQL*Plus command; ok is false for a line that starts SQL.
func clientCommandAt(line string) (cmd clientCommand, ok bool) {
	for _, prefix := range []string{"@@", "@", "!"} {
		if strings.HasPrefix(line, prefix) {
			return clientCommand{name: prefix, kind: commandStop}, true
		}
	}
	n := 0
	for n < len(line) && isWordByte(line[n]) {
		n++
	}
	if n < len(line) && !isBlank(line[n]) && line[n] != ';' {
		return clientCommand{}, false
	}
	word := strings.ToUpper(line[:n])
	body := strings.TrimSpace(line[n:])
	body = strings.TrimSpace(strings.TrimSuffix(body, ";"))
	for _, c := range sqlPlusCommands {
		if len(word) < c.shortest || !strings.HasPrefix(c.name, word) {
			continue
		}
		if c.name == "SET" {
			if fields := strings.Fields(body); len(fields) > 0 && sqlSetStatements[strings.ToUpper(fields[0])] {
				return clientCommand{}, false
			}
		}
		return clientCommand{name: c.name, kind: c.kind, body: body}, true
	}
	return clientCommand{}, false
}

// setDefine is the substitution character after a SET command with the given
// body: SET DEF[INE] OFF turns substitution off, ON turns it on with '&', and a
// single character (quoted or not) makes it that character.
func setDefine(body, define string) string {
	fields := strings.Fields(body)
	for i := 0; i+1 < len(fields); i++ {
		if option := strings.ToUpper(fields[i]); len(option) < 3 || !strings.HasPrefix("DEFINE", option) {
			continue
		}
		switch value := fields[i+1]; strings.ToUpper(value) {
		case "OFF":
			define = ""
		case "ON":
			define = "&"
		default:
			if len(value) >= 3 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
				value = value[1 : len(value)-1]
			}
			if len([]rune(value)) == 1 {
				define = value
			}
		}
	}
	return define
}

// substitutionVariable finds the first substitution variable (&name or &&name
// with define '&') in statement, wherever it is: SQL*Plus substitutes inside
// strings too. A define followed by no name ("a & b") is no variable.
func substitutionVariable(statement, define string) (variable string, at int, ok bool) {
	if define == "" {
		return "", 0, false
	}
	for i := 0; ; {
		k := strings.Index(statement[i:], define)
		if k < 0 {
			return "", 0, false
		}
		at = i + k
		name := at + len(define)
		if strings.HasPrefix(statement[name:], define) {
			name += len(define)
		}
		end := name
		for end < len(statement) && isWordByte(statement[end]) {
			end++
		}
		if end > name {
			return statement[at:end], at, true
		}
		i = name
	}
}
