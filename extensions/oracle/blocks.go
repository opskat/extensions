package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// RowInsertStatement writes a row whose CLOB, NCLOB or BLOB value does not fit
// one literal as a PL/SQL block: the INSERT gives each such column an empty LOB
// and returns its locator, and the value is appended piece by piece. The text of
// an XMLType, JSON or VECTOR value that does not fit one literal (a
// 1536-dimension vector does not) is built the same way in a temporary CLOB
// before the INSERT, which gives it to the column — XMLType through its
// constructor, which PL/SQL needs, the others converting it as they convert a
// literal — and frees it then. The block ends with a line holding only /. Every
// other row is the shared INSERT (ok false).
//
// A CLOB's piece is its string literal (TO_CLOB('…')). An NCLOB's and a BLOB's
// piece is base64 (UTL_ENCODE.BASE64_DECODE), of the bytes or of the text in
// UTF-16 (UTL_I18N.RAW_TO_NCHAR from AL16UTF16): like UNISTR it does not pass
// through the database character set, and it takes 4 bytes per 3 where HEXTORAW
// takes 2 per byte and UNISTR's escapes 5 per character outside ASCII. Each
// piece's literal stays within 4000 bytes, so a line of the file stays within
// SQL*Plus's 4999.
//
// Where the statement runs, go-ora holds its whole text several times over (as
// runes among them, 4 bytes a character), so a row whose appends take more than
// about 1 MB (blockPieces) is several blocks when the statement writes every
// column of row.Key (the primary key or a unique key of NOT NULL columns,
// InsertRowKey) with a value: the first inserts the row and appends its first
// pieces, each further block finds the row by its key — the key's literals are
// the INSERT's, so it compares each key column with what the INSERT stored — with
// SELECT … FOR UPDATE, which locks the row so that its LOBs can be written, and
// appends the next pieces to the LOBs it names. Without such a key the row stays
// one block, which depends on nothing another statement left in the session:
// one of more than maxBlockBytes is refused before any of it is written.
//
// The blocks are written one at a time (rowBlocks), the same for a row whose
// values are all in memory, as here, and for a dump's, whose large values are
// read in pieces as they are written (TableStatements, dumprows.go); here they
// are joined, each after a line break, into a buffer sized for them up front.
func (dialect) RowInsertStatement(row sqldb.InsertRow) (string, bool, error) {
	blocks, ok, err := newRowBlocks(row, nil)
	if err != nil || !ok {
		return "", false, err
	}
	first, last, err := blocks.next()
	if err != nil {
		return "", false, err
	}
	if last {
		return first, true, nil
	}
	var b strings.Builder
	b.Grow(len(first) + blocks.restSize())
	b.WriteString(first)
	for !last {
		var block string
		if block, last, err = blocks.next(); err != nil {
			return "", false, err
		}
		b.WriteString("\n" + block)
	}
	return b.String(), true, nil
}

// keyCondition is the condition that finds the row by row.Key, each column
// compared with the literal the INSERT writes for it; ok false when there is no
// key or the statement does not write every key column with a value.
func keyCondition(row sqldb.InsertRow) (string, bool, error) {
	if len(row.Key) == 0 {
		return "", false, nil
	}
	conditions := make([]string, len(row.Key))
	for i, name := range row.Key {
		j := slices.IndexFunc(row.Columns, func(c sqldb.InsertColumn) bool { return c.Name == name })
		if j < 0 || row.Columns[j].Value == nil {
			return "", false, nil
		}
		literal, err := row.Literals.Literal(row.Columns[j].Value, row.Columns[j].Type)
		if err != nil {
			return "", false, err
		}
		conditions[i] = row.Quote(name) + " = " + literal
	}
	return strings.Join(conditions, " AND "), true, nil
}

// blockTooLarge is the error for a row whose (first) block would take size bytes,
// more than maxBlockBytes: because of the text built before the INSERT (built),
// or because the row cannot be found by a key to append the rest in further blocks.
func blockTooLarge(row sqldb.InsertRow, size int, built bool) error {
	reason := "the table has neither a primary key nor a unique key of NOT NULL columns by which further blocks could find the row"
	switch {
	case built:
		reason = "the text of its XMLType, JSON or VECTOR values is built within the first block"
	case len(row.Key) > 0:
		quoted := make([]string, len(row.Key))
		for i, name := range row.Key {
			quoted[i] = row.Quote(name)
		}
		reason = "the statement does not write the columns that find the row (" + strings.Join(quoted, ", ") + ") with values"
	}
	return fmt.Errorf("oracle: a row of table %s would be a PL/SQL block of about %.1f MB (%d bytes) of SQL text, "+
		"more than the %d MB (%d bytes) one block may take: %s",
		row.TableRef, float64(size)/(1<<20), size, maxBlockBytes>>20, maxBlockBytes, reason)
}

// blockPieces is the most pieces one block appends when a row is several blocks:
// about 1 MB of appends (blockAppendBytes), a piece's append taking at most its
// literal or base64 of maxTextLiteral bytes and pieceCall. maxBlockBytes is the
// most SQL text a row's one block may take: a 16 MB BLOB's block (about 23 MB)
// runs within the extension's memory, a 50 MB one exhausts it.
const (
	blockAppendBytes = 1 << 20
	blockPieces      = blockAppendBytes / (maxTextLiteral + pieceCall)
	maxBlockBytes    = 24 << 20
	// furtherBlockAppends is the most a further block's appends take.
	furtherBlockAppends = blockPieces * (maxTextLiteral + pieceCall)
)

// blockEnd ends every block.
const blockEnd = "END;\n/"

// largeLob is a LOB value that does not fit one literal: the PL/SQL type of its
// locator, the empty LOB the INSERT writes and its pieces. built is set instead
// of empty for the text of a type that is not a LOB, built in a temporary CLOB:
// the INSERT's value, given the variable. size and count are about how many
// bytes its appends take (lobSize) and how many pieces it has: exact for a value
// in memory, at most that for one read in parts (streamedLob).
type largeLob struct {
	kind, empty, built string
	pieces             pieceSource
	size, count        int
}

// lobPiece is a piece of a large value: a part of it (a string of a text, []byte
// or a string of bytes), not a copy unless it spans two parts the value was read
// in; measure is what it adds to its appends' size (lobSize).
type lobPiece struct {
	value   any
	measure int
}

// pieceSource gives a large value's pieces in order.
type pieceSource interface {
	// next gives the next piece; false at the end of the value.
	next() (lobPiece, bool, error)
}

// memoryPieces are the pieces of a value in memory.
type memoryPieces []lobPiece

func (p *memoryPieces) next() (lobPiece, bool, error) {
	if len(*p) == 0 {
		return lobPiece{}, false, nil
	}
	piece := (*p)[0]
	*p = (*p)[1:]
	return piece, true, nil
}

// lobSize is about how many bytes a large value's appends take, from its pieces'
// measures: a CLOB's literals, an NCLOB's UTF-16 and a BLOB's bytes in base64
// (4 bytes per 3), and pieceCall for each piece's call.
type lobSize struct{ measure, pieces int }

func (s lobSize) bytes(kind string) int {
	switch kind {
	case "NCLOB":
		return 2*s.measure*4/3 + s.pieces*pieceCall
	case "BLOB":
		return s.measure*4/3 + s.pieces*pieceCall
	}
	return s.measure + s.pieces*pieceCall
}

// builtValues is how the INSERT gives each type whose text is built in a
// temporary CLOB (largeLob.built) its variable.
var builtValues = map[string]string{"XMLTYPE": "XMLTYPE(%s)", "JSON": "%s", "VECTOR": "%s"}

// pieceCall is the most an append takes besides its piece's literal or base64.
const pieceCall = 128

// maxBase64Piece is the bytes one base64 literal of 4000 characters holds.
const maxBase64Piece = maxTextLiteral / 4 * 3

// maxNclobPiece is the UTF-16 units of an NCLOB's piece: its bytes in base64.
const maxNclobPiece = maxBase64Piece / 2

// write writes the append of one piece to the LOB in variable.
func (lob *largeLob) write(b *strings.Builder, variable string, piece any, literals sqldb.Literals) error {
	switch lob.kind {
	case "CLOB":
		literal, err := literals.Literal(piece, "CLOB")
		if err != nil {
			return err
		}
		b.WriteString("  DBMS_LOB.APPEND(" + variable + ", TO_CLOB(" + literal + "));\n")
	case "NCLOB":
		text := piece.(string)
		units := utf16.Encode([]rune(text))
		raw := make([]byte, 2*len(units))
		for i, u := range units {
			binary.BigEndian.PutUint16(raw[2*i:], u)
		}
		b.WriteString("  DBMS_LOB.APPEND(" + variable + ", TO_NCLOB(UTL_I18N.RAW_TO_NCHAR(" + base64Raw(raw) + ", 'AL16UTF16')));\n")
	default:
		var raw []byte
		switch v := piece.(type) {
		case []byte:
			raw = v
		case string:
			raw = []byte(v)
		}
		b.WriteString("  DBMS_LOB.WRITEAPPEND(" + variable + ", " + strconv.Itoa(len(raw)) + ", " + base64Raw(raw) + ");\n")
	}
	return nil
}

// base64Raw is the PL/SQL expression of raw: its base64 decoded.
func base64Raw(raw []byte) string {
	return "UTL_ENCODE.BASE64_DECODE(UTL_RAW.CAST_TO_RAW('" + base64.StdEncoding.EncodeToString(raw) + "'))"
}

// splitLob reports whether column holds a CLOB, NCLOB or BLOB value, or the text
// of an XMLType, JSON or VECTOR value (builtValues), too large for one literal,
// and splits it into pieces that each fit one: a CLOB's or that text's string
// literal (textLiteralSize), an NCLOB's UTF-16 or a BLOB's bytes in base64
// (maxBase64Piece bytes, a surrogate pair never split).
func splitLob(column sqldb.InsertColumn) (largeLob, bool) {
	kind := typeName(column.Type)
	if built, ok := builtValues[kind]; ok {
		text, ok := column.Value.(string)
		if !ok {
			return largeLob{}, false
		}
		lob, ok := memoryLob("CLOB", text)
		lob.built = built
		return lob, ok && lob.count > 1
	}
	switch kind {
	case "CLOB", "NCLOB":
		var text string
		switch v := column.Value.(type) {
		case string:
			text = v
		case []byte:
			text = string(v)
		default:
			return largeLob{}, false
		}
		if kind == "NCLOB" && fitsNationalLiteral(text) {
			return largeLob{}, false
		}
		lob, _ := memoryLob(kind, text)
		return lob, kind == "NCLOB" || lob.count > 1
	case "BLOB":
		// A dump's value is bytes, the grid's the string of them; a piece is a
		// part of either, never a copy.
		var n int
		var piece func(start, end int) any
		switch v := column.Value.(type) {
		case []byte:
			n, piece = len(v), func(start, end int) any { return v[start:end] }
		case string:
			n, piece = len(v), func(start, end int) any { return v[start:end] }
		default:
			return largeLob{}, false
		}
		if n <= maxRawLiteral {
			return largeLob{}, false
		}
		var pieces memoryPieces
		for start := 0; start < n; start += maxBase64Piece {
			end := min(start+maxBase64Piece, n)
			pieces = append(pieces, lobPiece{value: piece(start, end), measure: end - start})
		}
		lob := largeLob{kind: kind, empty: "EMPTY_BLOB()", pieces: &pieces, count: len(pieces)}
		lob.size = lobSize{measure: n, pieces: len(pieces)}.bytes(kind)
		return lob, true
	}
	return largeLob{}, false
}

// memoryLob is a CLOB's or NCLOB's text in memory cut into its pieces
// (lobSplitter).
func memoryLob(kind, text string) (largeLob, bool) {
	split := lobSplitter(kind)
	pieces := memoryPieces(split.end(split.text(text, nil)))
	size := lobSize{pieces: len(pieces)}
	for _, piece := range pieces {
		size.measure += piece.measure
	}
	return largeLob{kind: kind, empty: "EMPTY_CLOB()", pieces: &pieces, size: size.bytes(kind), count: len(pieces)}, true
}

// lobSplitter cuts a large value of kind into its pieces: a CLOB's text into
// string literals of maxTextLiteral bytes (textLiteralSize), an NCLOB's into
// maxNclobPiece UTF-16 units (a surrogate pair never split), a BLOB's bytes into
// maxBase64Piece.
func lobSplitter(kind string) *pieceSplitter {
	switch kind {
	case "CLOB":
		return &pieceSplitter{size: textLiteralSize, limit: maxTextLiteral}
	case "NCLOB":
		return &pieceSplitter{size: func(r rune) (int, int) { return utf16.RuneLen(r), 0 }, limit: maxNclobPiece}
	}
	return &pieceSplitter{}
}

// pieceSplitter cuts a value into pieces as its parts arrive, the same pieces
// however it is parted. Text is cut greedily before each character that would
// take the piece past limit in either of the two sizes size gives it (a piece's
// measure is the first), never inside a character; bytes every maxBase64Piece.
// A piece is a part of a part, or a copy when it spans two.
type pieceSplitter struct {
	size  func(r rune) (int, int)
	limit int
	// carry is the piece begun in an earlier part; n1, n2 its sizes.
	carry  string
	n1, n2 int
	bytes  []byte
}

// text adds the pieces that part completes to pieces.
func (s *pieceSplitter) text(part string, pieces []lobPiece) []lobPiece {
	start := 0
	for i, r := range part {
		n1, n2 := s.size(r)
		if s.n1+n1 > s.limit || s.n2+n2 > s.limit {
			pieces = append(pieces, lobPiece{value: s.carry + part[start:i], measure: s.n1})
			s.carry, start, s.n1, s.n2 = "", i, 0, 0
		}
		s.n1, s.n2 = s.n1+n1, s.n2+n2
	}
	if start == 0 {
		s.carry += part
	} else {
		s.carry = part[start:]
	}
	return pieces
}

// binary adds the pieces that part completes to pieces.
func (s *pieceSplitter) binary(part []byte, pieces []lobPiece) []lobPiece {
	if len(s.bytes) > 0 {
		n := min(maxBase64Piece-len(s.bytes), len(part))
		s.bytes, part = append(s.bytes, part[:n]...), part[n:]
		if len(s.bytes) < maxBase64Piece {
			return pieces
		}
		pieces = append(pieces, lobPiece{value: s.bytes, measure: len(s.bytes)})
		s.bytes = nil
	}
	for ; len(part) >= maxBase64Piece; part = part[maxBase64Piece:] {
		pieces = append(pieces, lobPiece{value: part[:maxBase64Piece], measure: maxBase64Piece})
	}
	if len(part) > 0 {
		s.bytes = append([]byte(nil), part...)
	}
	return pieces
}

// end adds the last piece to pieces: text's always (as literalPieces), bytes'
// when there are some left.
func (s *pieceSplitter) end(pieces []lobPiece) []lobPiece {
	if s.size != nil {
		return append(pieces, lobPiece{value: s.carry, measure: s.n1})
	}
	if len(s.bytes) > 0 {
		pieces = append(pieces, lobPiece{value: s.bytes, measure: len(s.bytes)})
	}
	return pieces
}

// streamedLob is a large CLOB, NCLOB or BLOB value of length DBMS_LOB.GETLENGTH
// units that read gives part by part — a string of whole characters, []byte for a
// BLOB; false at the end — cut into pieces as they arrive (a dump's,
// dumprows.go). Its size and count are at most what its appends take: 3 bytes a
// UTF-16 unit of a CLOB's literals.
func streamedLob(kind string, length int64, read func() (any, bool, error)) *largeLob {
	lob := &largeLob{kind: kind, empty: "EMPTY_CLOB()", pieces: &partPieces{read: read, split: lobSplitter(kind)}}
	n := int(length)
	switch kind {
	case "CLOB":
		lob.count = 3*n/(maxTextLiteral-3) + 1
		lob.size = lobSize{measure: 3 * n, pieces: lob.count}.bytes(kind)
	case "NCLOB":
		lob.count = n/(maxNclobPiece-1) + 1
		lob.size = lobSize{measure: n, pieces: lob.count}.bytes(kind)
	default:
		lob.empty = "EMPTY_BLOB()"
		lob.count = n/maxBase64Piece + 1
		lob.size = lobSize{measure: n, pieces: lob.count}.bytes(kind)
	}
	return lob
}

// partPieces are the pieces of a value read part by part.
type partPieces struct {
	read  func() (any, bool, error)
	split *pieceSplitter
	queue []lobPiece
	ended bool
}

func (p *partPieces) next() (lobPiece, bool, error) {
	for len(p.queue) == 0 {
		if p.ended {
			return lobPiece{}, false, nil
		}
		part, ok, err := p.read()
		switch {
		case err != nil:
			return lobPiece{}, false, err
		case !ok:
			p.ended, p.queue = true, p.split.end(nil)
		default:
			switch v := part.(type) {
			case string:
				p.queue = p.split.text(v, nil)
			case []byte:
				p.queue = p.split.binary(v, nil)
			default:
				return lobPiece{}, false, fmt.Errorf("oracle: a part of a large value is a %T", part)
			}
		}
	}
	piece := p.queue[0]
	p.queue = p.queue[1:]
	return piece, true, nil
}

// rowBlocks writes the blocks of a row with large values (RowInsertStatement) one
// at a time, pulling each value's pieces as it appends them, so that a value read
// in parts is never whole in memory: a block of about 1 MB of appends, or the
// row's one block of at most maxBlockBytes.
type rowBlocks struct {
	row sqldb.InsertRow
	// lobs are the row's large values by column (nil for a literal); variables
	// their locators' variables.
	lobs      []*largeLob
	names     []string
	variables []string
	// declare declares every variable; insert is the INSERT and the frees after it.
	declare, insert   string
	returning, into   []string
	header, builtSize int
	where             string
	keyable           bool
	started           bool
	// b is the block being written: all of the first, a further one's appends;
	// appending are the columns a further block appends to.
	b         strings.Builder
	appending []int
	// col is the column being appended, held the piece read that starts the next
	// block, written the pieces appended, size the column's appends so far and
	// appendSize the appends of the columns before it.
	col        int
	held       *lobPiece
	written    int
	size       lobSize
	appendSize int
	// blocks is how many blocks were given; over that the row's one block passed
	// maxBlockBytes and is no longer written, only measured.
	blocks int
	over   bool
}

// newRowBlocks prepares the blocks of row: its values too large for one literal
// (splitLob) and, instead of the columns streamed names, the values it gives part
// by part. ok false when the row has no large value.
func newRowBlocks(row sqldb.InsertRow, streamed map[string]*largeLob) (*rowBlocks, bool, error) {
	r := &rowBlocks{row: row, lobs: make([]*largeLob, len(row.Columns))}
	large := false
	for i, column := range row.Columns {
		if lob, ok := streamed[column.Name]; ok {
			r.lobs[i], large = lob, true
		} else if lob, ok := splitLob(column); ok {
			r.lobs[i], large = &lob, true
		}
	}
	if !large {
		return nil, false, nil
	}
	var declare, free strings.Builder
	r.names = make([]string, len(row.Columns))
	values := make([]string, len(row.Columns))
	r.variables = make([]string, len(row.Columns))
	n := 0
	for i, column := range row.Columns {
		r.names[i] = row.Quote(column.Name)
		lob := r.lobs[i]
		if lob == nil {
			value, err := row.Literals.Literal(column.Value, column.Type)
			if err != nil {
				return nil, false, err
			}
			values[i] = value
			continue
		}
		n++
		r.variables[i] = fmt.Sprintf("l%d", n)
		fmt.Fprintf(&declare, "  %s %s;\n", r.variables[i], lob.kind)
		if lob.built != "" {
			values[i] = fmt.Sprintf(lob.built, r.variables[i])
			fmt.Fprintf(&free, "  DBMS_LOB.FREETEMPORARY(%s);\n", r.variables[i])
			continue
		}
		values[i] = lob.empty
		r.returning, r.into = append(r.returning, r.names[i]), append(r.into, r.variables[i])
	}
	modifier := ""
	if row.Modifier != "" {
		modifier = " " + row.Modifier
	}
	insert := fmt.Sprintf("  INSERT INTO %s (%s)%s VALUES (%s)", row.TableRef,
		strings.Join(r.names, ", "), modifier, strings.Join(values, ", "))
	if len(r.into) > 0 {
		insert += fmt.Sprintf(" RETURNING %s INTO %s", strings.Join(r.returning, ", "), strings.Join(r.into, ", "))
	}
	r.declare, r.insert = declare.String(), insert+";\n"+free.String()
	r.header = len("DECLARE\n") + len(r.declare) + len("BEGIN\n")
	var err error
	if r.where, r.keyable, err = keyCondition(row); err != nil {
		return nil, false, err
	}
	return r, true, nil
}

// next gives the next block and whether it is the last one.
func (r *rowBlocks) next() (string, bool, error) {
	if !r.started {
		r.started = true
		if err := r.begin(); err != nil {
			return "", false, err
		}
	}
	for {
		starts := r.held != nil // the piece that starts a further block
		piece, ok, err := r.nextPiece()
		if err != nil {
			return "", false, err
		}
		if !ok {
			return r.finish()
		}
		if !starts && r.written > 0 && r.written%blockPieces == 0 && r.keyable {
			// The row has more pieces than one block appends, and further blocks
			// find it by its key: the first block takes the INSERT, the text
			// built before it and blockPieces appends at most.
			if r.blocks == 0 {
				first := r.header + r.builtSize + len(r.insert) + furtherBlockAppends + len(blockEnd)
				if first > maxBlockBytes {
					return "", false, blockTooLarge(r.row, first, true)
				}
			}
			r.held = &piece
			return r.endBlock(), false, nil
		}
		if err := r.write(piece); err != nil {
			return "", false, err
		}
	}
}

// begin writes the first block up to its appends: the declarations, the text
// built before the INSERT, and the INSERT.
func (r *rowBlocks) begin() error {
	appends := 0
	for _, lob := range r.lobs {
		if lob != nil && lob.built == "" {
			appends += lob.size
		}
	}
	if r.keyable {
		appends = min(appends, furtherBlockAppends)
	}
	built := 0
	for _, lob := range r.lobs {
		if lob != nil && lob.built != "" {
			built += lob.size
		}
	}
	r.b.Grow(min(r.header+built+len(r.insert)+appends+len(blockEnd), maxBlockBytes))
	r.b.WriteString("DECLARE\n" + r.declare + "BEGIN\n")
	for i, lob := range r.lobs {
		if lob == nil || lob.built == "" {
			continue
		}
		r.b.WriteString("  DBMS_LOB.CREATETEMPORARY(" + r.variables[i] + ", TRUE);\n")
		size := lobSize{}
		for {
			piece, ok, err := lob.pieces.next()
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			if err := lob.write(&r.b, r.variables[i], piece.value, r.row.Literals); err != nil {
				return err
			}
			size.measure, size.pieces = size.measure+piece.measure, size.pieces+1
		}
		r.builtSize += size.bytes(lob.kind)
	}
	r.b.WriteString(r.insert)
	return nil
}

// nextPiece gives the next piece to append, column by column; r.col is its column.
func (r *rowBlocks) nextPiece() (lobPiece, bool, error) {
	if r.held != nil {
		piece := *r.held
		r.held = nil
		return piece, true, nil
	}
	for ; r.col < len(r.lobs); r.col++ {
		lob := r.lobs[r.col]
		if lob == nil || lob.built != "" {
			continue
		}
		piece, ok, err := lob.pieces.next()
		if err != nil || ok {
			return piece, ok, err
		}
		r.appendSize += r.size.bytes(lob.kind)
		r.size = lobSize{}
	}
	return lobPiece{}, false, nil
}

// write appends piece to the block. The row's one block stops being written once
// it would pass maxBlockBytes, and is only measured on (finish).
func (r *rowBlocks) write(piece lobPiece) error {
	lob := r.lobs[r.col]
	r.size.measure, r.size.pieces = r.size.measure+piece.measure, r.size.pieces+1
	r.written++
	if r.blocks > 0 && (len(r.appending) == 0 || r.appending[len(r.appending)-1] != r.col) {
		r.appending = append(r.appending, r.col)
	}
	if r.over {
		return nil
	}
	if r.blocks > 0 && r.b.Len() == 0 {
		r.b.Grow(furtherBlockAppends)
	}
	if err := lob.write(&r.b, r.variables[r.col], piece.value, r.row.Literals); err != nil {
		return err
	}
	if r.blocks == 0 && r.oneBlockSize() > maxBlockBytes {
		r.over, r.b = true, strings.Builder{}
	}
	return nil
}

// oneBlockSize is about how many bytes the row's one block takes with what it
// appended so far.
func (r *rowBlocks) oneBlockSize() int {
	return r.header + r.builtSize + len(r.insert) + r.appendSize + r.size.bytes(r.lobs[r.col].kind) + len(blockEnd)
}

// finish gives the last block. When it is the row's only one, a block of more
// than maxBlockBytes is refused: because of the text built before the INSERT when
// it appends no more than one block of several would, otherwise because no key
// finds the row.
func (r *rowBlocks) finish() (string, bool, error) {
	if r.blocks == 0 {
		if size := r.header + r.builtSize + len(r.insert) + r.appendSize + len(blockEnd); size > maxBlockBytes {
			return "", false, blockTooLarge(r.row, size, r.written <= blockPieces)
		}
	}
	return r.endBlock(), true, nil
}

// endBlock ends the block being written and gives it. A further block declares
// and selects, with FOR UPDATE by the key, the LOBs it appends to.
func (r *rowBlocks) endBlock() string {
	var block string
	if r.blocks == 0 {
		r.b.WriteString(blockEnd)
		block = r.b.String()
	} else {
		var declared strings.Builder
		lobs, vars := make([]string, len(r.appending)), make([]string, len(r.appending))
		for i, col := range r.appending {
			lobs[i], vars[i] = r.names[col], r.variables[col]
			fmt.Fprintf(&declared, "  %s %s;\n", vars[i], r.lobs[col].kind)
		}
		head := fmt.Sprintf("DECLARE\n%sBEGIN\n  SELECT %s INTO %s FROM %s WHERE %s FOR UPDATE;\n", declared.String(),
			strings.Join(lobs, ", "), strings.Join(vars, ", "), r.row.TableRef, r.where)
		var b strings.Builder
		b.Grow(len(head) + r.b.Len() + len(blockEnd))
		b.WriteString(head)
		b.WriteString(r.b.String())
		b.WriteString(blockEnd)
		block = b.String()
	}
	r.blocks++
	r.appending = r.appending[:0]
	r.b = strings.Builder{}
	return block
}

// restSize is at most what the blocks after the first take, each after a line
// break, when the first ends before the row's last piece.
func (r *rowBlocks) restSize() int {
	appends, pieces := 0, 0
	for _, lob := range r.lobs {
		if lob != nil && lob.built == "" {
			appends, pieces = appends+lob.size, pieces+lob.count
		}
	}
	selectAll := len(fmt.Sprintf("  SELECT %s INTO %s FROM %s WHERE %s FOR UPDATE;\n",
		strings.Join(r.returning, ", "), strings.Join(r.into, ", "), r.row.TableRef, r.where))
	return appends + (pieces/blockPieces)*(1+r.header+selectAll+len(blockEnd))
}
