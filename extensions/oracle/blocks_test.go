package main

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// textParts reads text in parts of about size bytes, each ending at a character
// boundary, as the server's pieces do (spec decision 14).
func textParts(text string, size int) func() (any, bool, error) {
	return func() (any, bool, error) {
		if text == "" {
			return nil, false, nil
		}
		n := min(size, len(text))
		for n < len(text) && !utf8Start(text[n]) {
			n++
		}
		part := text[:n]
		text = text[n:]
		return part, true, nil
	}
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// byteParts reads data in parts of size bytes.
func byteParts(data []byte, size int) func() (any, bool, error) {
	return func() (any, bool, error) {
		if len(data) == 0 {
			return nil, false, nil
		}
		n := min(size, len(data))
		part := data[:n]
		data = data[n:]
		return part, true, nil
	}
}

// lobText is text of quotes, a backslash, Chinese, emoji (a surrogate pair each)
// and line breaks, about n bytes.
func lobText(n int) string {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString("it's \\ 中文 😀 x\n")
	}
	return b.String()
}

func lobBytes(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i*7 + i/251)
	}
	return data
}

// streamedText is a text value of kind read in parts of size bytes.
func streamedText(kind, text string, size int) *largeLob {
	return streamedLob(kind, int64(len(utf16.Encode([]rune(text)))), textParts(text, size))
}

// allPieces reads every piece of lob.
func allPieces(t *testing.T, lob *largeLob) []lobPiece {
	t.Helper()
	var pieces []lobPiece
	for {
		piece, ok, err := lob.pieces.next()
		require.NoError(t, err)
		if !ok {
			return pieces
		}
		pieces = append(pieces, piece)
	}
}

// A value read in parts is cut into the same pieces as the whole value, whatever
// the parts' sizes: a piece never splits a character, nor a surrogate pair of an
// NCLOB's UTF-16, and one that spans two parts is joined.
func TestPiecesDoNotDependOnHowTheValueIsRead(t *testing.T) {
	text := lobText(30_000)
	data := lobBytes(20_000)
	for _, size := range []int{1, 7, 2999, 3000, 4001, 9000, 1 << 20} {
		for _, kind := range []string{"CLOB", "NCLOB"} {
			whole, ok := splitLob(sqldb.InsertColumn{Name: "C", Type: kind, Value: text})
			require.True(t, ok)
			assert.Equal(t, allPieces(t, &whole), allPieces(t, streamedText(kind, text, size)), "%s in parts of %d bytes", kind, size)
		}
		whole, ok := splitLob(sqldb.InsertColumn{Name: "B", Type: "BLOB", Value: data})
		require.True(t, ok)
		assert.Equal(t, allPieces(t, &whole), allPieces(t, streamedLob("BLOB", int64(len(data)), byteParts(data, size))),
			"BLOB in parts of %d bytes", size)
	}
}

// rowStatements is every block of row, whose streamed values are the given ones,
// one statement each.
func rowStatements(t *testing.T, row sqldb.InsertRow, streamed map[string]*largeLob) ([]string, error) {
	t.Helper()
	blocks, ok, err := newRowBlocks(row, streamed)
	require.NoError(t, err)
	require.True(t, ok)
	var statements []string
	for {
		block, last, err := blocks.next()
		if err != nil {
			return statements, err
		}
		statements = append(statements, block)
		if last {
			return statements, nil
		}
	}
}

// A row whose large values are read in parts is written block by block, a block
// one statement, and the blocks are the statement the row writer writes for the
// whole values (RowInsertStatement, spec decision 10): with a key every block of
// about 1 MB of appends, the later ones naming the LOBs they append to; without
// one the whole row one block; the text of an XMLType built in the first block.
func TestStreamedRowIsTheRowWritersStatement(t *testing.T) {
	text, ntext, data := lobText(2_500_000), lobText(700_000), lobBytes(1_300_000)
	xml := "<a>" + strings.Repeat("x", 5000) + "</a>"
	for _, key := range [][]string{{"ID"}, nil} {
		row := sqldb.InsertRow{TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals, Key: key, Modifier: "/*+ APPEND */",
			Columns: []sqldb.InsertColumn{
				{Name: "ID", Type: "NUMBER", Value: "7"},
				{Name: "X", Type: "XMLTYPE", Value: xml},
				{Name: "C", Type: "CLOB", Value: text},
				{Name: "SMALL", Type: "CLOB", Value: "fits"},
				{Name: "NC", Type: "NCLOB", Value: ntext},
				{Name: "B", Type: "BLOB", Value: data},
			}}
		whole := dialect{}.mustRowInsertStatement(t, row)
		streamed := map[string]*largeLob{
			"C":  streamedText("CLOB", text, textReadUnits),
			"NC": streamedText("NCLOB", ntext, 333_333),
			"B":  streamedLob("BLOB", int64(len(data)), byteParts(data, blobReadBytes)),
		}
		statements, err := rowStatements(t, row, streamed)
		require.NoError(t, err)
		assert.Equal(t, splitBlocksAnySize(t, whole), statements, "key %v", key)
		if key == nil {
			assert.Len(t, statements, 1)
		} else {
			assert.Greater(t, len(statements), 5)
		}
	}
}

// splitBlocksAnySize splits a row's statement into its blocks as the run does.
func splitBlocksAnySize(t *testing.T, statement string) []string {
	t.Helper()
	var blocks []string
	for block := range strings.SplitAfterSeq(statement, "\nEND;\n/") {
		if block != "" {
			blocks = append(blocks, strings.TrimPrefix(block, "\n"))
		}
	}
	return blocks
}

// Without a key a row is one block however its values are read, and one whose
// block would take more than 24 MB stops before any of it is given, with the
// error the row writer gives for the whole values.
func TestStreamedRowWithoutAKeyIsOneBlockOfAtMost24MB(t *testing.T) {
	b16, b2 := make([]byte, maxLobBytes), make([]byte, 2<<20)
	row := sqldb.InsertRow{TableRef: `"BIG"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "ID", Type: "NUMBER", Value: "1"}, {Name: "B", Type: "BLOB", Value: b16},
			{Name: "B2", Type: "BLOB", Value: b2}}}
	_, _, want := dialect{}.RowInsertStatement(row)
	require.Error(t, want)

	statements, err := rowStatements(t, row, map[string]*largeLob{
		"B":  streamedLob("BLOB", int64(len(b16)), byteParts(b16, blobReadBytes)),
		"B2": streamedLob("BLOB", int64(len(b2)), byteParts(b2, blobReadBytes)),
	})
	assert.Empty(t, statements)
	assert.EqualError(t, err, want.Error())
}
