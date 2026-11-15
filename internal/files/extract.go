package files

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// MaxText is the most text kept from one file (in characters); the rest is cut and the note says so.
const MaxText = 2_000_000

// Extracted is what Extract could read from a file.
type Extracted struct {
	Mime, Kind    string
	Text          string
	Pages         int
	Width, Height int
	Preview       []byte // JPEG, images only
	Note          string
}

// text types by extension; anything else that is valid text is text/plain
var textMime = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown", ".csv": "text/csv", ".tsv": "text/tab-separated-values",
	".json": "application/json", ".yaml": "application/yaml", ".yml": "application/yaml", ".toml": "application/toml",
	".xml": "application/xml", ".html": "text/html", ".htm": "text/html", ".css": "text/css", ".svg": "image/svg+xml",
	".js": "text/javascript", ".mjs": "text/javascript", ".ts": "text/x-typescript", ".go": "text/x-go", ".py": "text/x-python",
	".rs": "text/x-rust", ".c": "text/x-c", ".h": "text/x-c", ".cpp": "text/x-c++", ".java": "text/x-java", ".kt": "text/x-kotlin",
	".sh": "text/x-shellscript", ".sql": "application/sql", ".tex": "text/x-tex", ".typ": "text/x-typst", ".svelte": "text/x-svelte",
	".nix": "text/x-nix", ".r": "text/x-r", ".rb": "text/x-ruby", ".php": "text/x-php", ".swift": "text/x-swift", ".log": "text/plain",
}

const (
	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimePptx = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	mimeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimeOdt  = "application/vnd.oasis.opendocument.text"
)

// Extract works out what a file is and takes out its text or preview. It never fails: what it cannot read is
// stored as is, with a note.
func Extract(name string, data []byte) (x Extracted) {
	ext := strings.ToLower(path.Ext(name))
	sniff := http.DetectContentType(data)
	defer func() {
		if r := recover(); r != nil { // a parser choked on a broken or hostile file
			x = Extracted{Mime: "application/octet-stream", Kind: "binary", Note: fmt.Sprintf("could not be read: %v", r)}
		}
		if r := []rune(x.Text); len(r) > MaxText {
			x.Text = string(r[:MaxText])
			x.Note = strings.TrimSpace(x.Note + fmt.Sprintf(" Only the first %d characters were kept.", MaxText))
		}
	}()
	switch {
	case sniff == "image/png" || sniff == "image/jpeg" || sniff == "image/gif" || sniff == "image/webp":
		return imageFile(sniff, data)
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return pdfText(data)
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		if x, ok := office(data); ok {
			return x
		}
		return Extracted{Mime: "application/zip", Kind: "binary", Note: "an archive: its contents are not read"}
	case ext == ".heic" || ext == ".heif":
		return Extracted{Mime: "image/heic", Kind: "binary", Note: "HEIC images cannot be shown or read: save it as JPEG"}
	}
	if t, ok := decodeText(data); ok {
		mime := textMime[ext]
		if mime == "" {
			mime = "text/plain"
		}
		return Extracted{Mime: mime, Kind: "text", Text: t}
	}
	return Extracted{Mime: "application/octet-stream", Kind: "binary", Note: "not a kind of file that can be read"}
}

// decodeText reads UTF-8, UTF-16 with a byte order mark, or (failing both, with no NUL bytes) Latin-1.
func decodeText(b []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		b = b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			if le {
				u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
			} else {
				u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
			}
		}
		return string(utf16.Decode(u)), true
	}
	if bytes.IndexByte(b[:min(len(b), 8192)], 0) >= 0 {
		return "", false
	}
	if utf8.Valid(b) {
		return string(b), true
	}
	// Latin-1 (older Norwegian CSV exports): every byte is one character; control bytes mean it is not text
	r := make([]rune, len(b))
	for i, c := range b {
		if c < 9 || (c > 13 && c < 32) {
			return "", false
		}
		r[i] = rune(c)
	}
	return string(r), true
}

func pdfText(data []byte) Extracted {
	x := Extracted{Mime: "application/pdf", Kind: "document"}
	type result struct {
		text  string
		pages int
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{err: fmt.Errorf("%v", r)}
			}
		}()
		rd, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			done <- result{err: err}
			return
		}
		n := rd.NumPage()
		var b strings.Builder
		for i := 1; i <= n && b.Len() < 4*MaxText; i++ {
			p := rd.Page(i)
			if p.V.IsNull() {
				continue
			}
			t, err := p.GetPlainText(nil)
			if err != nil {
				continue
			}
			fmt.Fprintf(&b, "\n\n--- page %d ---\n%s", i, strings.TrimSpace(t))
		}
		done <- result{text: strings.TrimSpace(b.String()), pages: n}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			x.Note = "the PDF could not be read (" + r.err.Error() + "); it may be encrypted or broken"
			return x
		}
		x.Pages = r.pages
		letters := 0
		for _, c := range r.text {
			if c > ' ' && c != '-' {
				letters++
			}
		}
		if r.pages > 0 && letters < 20*r.pages {
			x.Note = "little or no text layer (a scanned PDF?): only the text that was there is read"
		}
		x.Text = r.text
	case <-time.After(30 * time.Second):
		x.Note = "reading the PDF took too long; it is stored, but its text is not read"
	}
	return x
}

// ---------- Office Open XML and OpenDocument ----------

func zipFile(z *zip.Reader, name string) ([]byte, bool) {
	for _, f := range z.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, false
			}
			defer rc.Close()
			b, err := io.ReadAll(io.LimitReader(rc, 64<<20)) // a zip bomb stops here
			return b, err == nil
		}
	}
	return nil, false
}

func office(data []byte) (Extracted, bool) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Extracted{}, false
	}
	if b, ok := zipFile(z, "word/document.xml"); ok {
		return Extracted{Mime: mimeDocx, Kind: "document", Text: xmlText(b, "p", map[string]string{"tab": "\t", "br": "\n", "cr": "\n", "tc": "\t"}, "t")}, true
	}
	if b, ok := zipFile(z, "mimetype"); ok && strings.TrimSpace(string(b)) == mimeOdt {
		c, _ := zipFile(z, "content.xml")
		return Extracted{Mime: mimeOdt, Kind: "document", Text: xmlText(c, "p|h", map[string]string{"tab": "\t", "line-break": "\n", "s": " "}, "*")}, true
	}
	var slides []string
	var sheets bool
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slides = append(slides, f.Name)
		}
		if strings.HasPrefix(f.Name, "xl/worksheets/") {
			sheets = true
		}
	}
	if len(slides) > 0 {
		sort.Slice(slides, func(i, j int) bool { return number(slides[i]) < number(slides[j]) })
		var b strings.Builder
		for i, s := range slides {
			raw, _ := zipFile(z, s)
			fmt.Fprintf(&b, "\n\n--- slide %d ---\n%s", i+1, xmlText(raw, "p", map[string]string{"br": "\n"}, "t"))
		}
		return Extracted{Mime: mimePptx, Kind: "document", Text: strings.TrimSpace(b.String()), Pages: len(slides)}, true
	}
	if sheets {
		t, n := xlsx(z)
		return Extracted{Mime: mimeXlsx, Kind: "document", Text: t, Pages: n}, true
	}
	return Extracted{}, false
}

// number is the number in a name like ppt/slides/slide12.xml.
func number(name string) int {
	base := strings.TrimSuffix(path.Base(name), ".xml")
	i := len(base)
	for i > 0 && base[i-1] >= '0' && base[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(base[i:])
	return n
}

// xmlText collects character data inside elements named textEl ("*" = any), one line per paragraph element (para,
// with | between names), and writes the replacement for the listed empty elements (tabs, breaks). Namespaces are
// ignored: only local names count.
func xmlText(b []byte, para string, repl map[string]string, textEl string) string {
	paras := map[string]bool{}
	for _, p := range strings.Split(para, "|") {
		paras[p] = true
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	var out strings.Builder
	depth := 0 // inside a text element
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if textEl == "*" || t.Name.Local == textEl {
				depth++
			}
			if r, ok := repl[t.Name.Local]; ok {
				if t.Name.Local == "s" { // OpenDocument: <text:s text:c="3"/> is three spaces
					n := 1
					for _, a := range t.Attr {
						if a.Name.Local == "c" {
							n, _ = strconv.Atoi(a.Value)
						}
					}
					r = strings.Repeat(" ", max(n, 1))
				}
				out.WriteString(r)
			}
		case xml.EndElement:
			if textEl == "*" || t.Name.Local == textEl {
				depth--
			}
			if paras[t.Name.Local] {
				out.WriteString("\n")
			}
		case xml.CharData:
			if depth > 0 {
				out.Write(t)
			}
		}
	}
	return strings.TrimSpace(out.String())
}

// xlsx returns every sheet as tab-separated rows, and the number of sheets.
func xlsx(z *zip.Reader) (string, int) {
	var shared []string
	if b, ok := zipFile(z, "xl/sharedStrings.xml"); ok {
		d := xml.NewDecoder(bytes.NewReader(b))
		var cur strings.Builder
		inT := false
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "si" {
					cur.Reset()
				}
				inT = t.Name.Local == "t"
			case xml.EndElement:
				if t.Name.Local == "si" {
					shared = append(shared, cur.String())
				}
				if t.Name.Local == "t" {
					inT = false
				}
			case xml.CharData:
				if inT {
					cur.Write(t)
				}
			}
		}
	}
	// sheet names in workbook order, and where each one is
	type sheet struct{ name, file string }
	var sheets []sheet
	rels := map[string]string{}
	if b, ok := zipFile(z, "xl/_rels/workbook.xml.rels"); ok {
		var r struct {
			Rel []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		_ = xml.Unmarshal(b, &r)
		for _, x := range r.Rel {
			t := strings.TrimPrefix(x.Target, "/xl/")
			rels[x.ID] = "xl/" + strings.TrimPrefix(t, "xl/")
		}
	}
	if b, ok := zipFile(z, "xl/workbook.xml"); ok {
		var w struct {
			Sheets []struct {
				Name  string     `xml:"name,attr"`
				Attrs []xml.Attr `xml:",any,attr"`
			} `xml:"sheets>sheet"`
		}
		_ = xml.Unmarshal(b, &w)
		for _, s := range w.Sheets {
			for _, a := range s.Attrs {
				if a.Name.Local == "id" && rels[a.Value] != "" {
					sheets = append(sheets, sheet{s.Name, rels[a.Value]})
				}
			}
		}
	}
	if len(sheets) == 0 { // no workbook: take the sheet files in order
		for _, f := range z.File {
			if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
				sheets = append(sheets, sheet{strings.TrimSuffix(path.Base(f.Name), ".xml"), f.Name})
			}
		}
		sort.Slice(sheets, func(i, j int) bool { return number(sheets[i].file) < number(sheets[j].file) })
	}
	var out strings.Builder
	for _, s := range sheets {
		b, ok := zipFile(z, s.file)
		if !ok {
			continue
		}
		fmt.Fprintf(&out, "\n\n--- sheet %s ---\n", s.name)
		d := xml.NewDecoder(bytes.NewReader(b))
		var cells []string
		col, typ := 0, ""
		var val strings.Builder
		inV := false
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "row":
					cells = cells[:0]
				case "c":
					typ, col = "", len(cells)
					val.Reset()
					for _, a := range t.Attr {
						switch a.Name.Local {
						case "t":
							typ = a.Value
						case "r":
							col = column(a.Value)
						}
					}
				case "v", "t":
					inV = true
				}
			case xml.EndElement:
				switch t.Name.Local {
				case "v", "t":
					inV = false
				case "c":
					v := val.String()
					if typ == "s" {
						if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(shared) {
							v = shared[i]
						}
					}
					for len(cells) < col {
						cells = append(cells, "")
					}
					if col < 16384 {
						cells = append(cells, strings.ReplaceAll(strings.ReplaceAll(v, "\t", " "), "\n", " "))
					}
				case "row":
					if strings.TrimSpace(strings.Join(cells, "")) != "" {
						out.WriteString(strings.Join(cells, "\t"))
						out.WriteString("\n")
					}
				}
			case xml.CharData:
				if inV {
					val.Write(t)
				}
			}
		}
		if out.Len() > 4*MaxText {
			break
		}
	}
	return strings.TrimSpace(out.String()), len(sheets)
}

// column turns a cell reference like "AB12" into a zero-based column number.
func column(ref string) int {
	n := 0
	for _, c := range ref {
		if c < 'A' || c > 'Z' {
			break
		}
		n = n*26 + int(c-'A'+1)
	}
	return max(n-1, 0)
}
