package files

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/frheg/athena/internal/pg"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExtractText(t *testing.T) {
	x := Extract("main.go", []byte("package main\n\nfunc main() {}\n"))
	if x.Kind != "text" || x.Mime != "text/x-go" || !strings.Contains(x.Text, "func main") {
		t.Fatal(x)
	}
	x = Extract("gammel.csv", []byte("navn;by\nP\xe5l;Troms\xf8\n")) // Latin-1
	if x.Kind != "text" || !strings.Contains(x.Text, "Pål;Tromsø") {
		t.Fatalf("%+v", x)
	}
	x = Extract("w.txt", []byte{0xFF, 0xFE, 'h', 0, 'i', 0}) // UTF-16 LE
	if x.Text != "hi" {
		t.Fatalf("%q", x.Text)
	}
	x = Extract("blob.bin", []byte{0, 1, 2, 3, 0, 0, 7})
	if x.Kind != "binary" || x.Text != "" {
		t.Fatal(x)
	}
	x = Extract("drawing.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if x.Kind != "text" || x.Preview != nil {
		t.Fatal("an SVG is text, never an image to show", x)
	}
}

func TestExtractOffice(t *testing.T) {
	docx := zipOf(t, map[string]string{"word/document.xml": `<w:document xmlns:w="w"><w:body>
		<w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:tab/><w:t>world</w:t></w:r></w:p>
		<w:p><w:r><w:t>Second line</w:t></w:r></w:p></w:body></w:document>`})
	x := Extract("letter.docx", docx)
	if x.Kind != "document" || x.Mime != mimeDocx || x.Text != "Hello\tworld\nSecond line" {
		t.Fatalf("%+v", x)
	}
	pptx := zipOf(t, map[string]string{
		"ppt/slides/slide2.xml":  `<p:sld xmlns:a="a" xmlns:p="p"><a:p><a:r><a:t>Second</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide10.xml": `<p:sld xmlns:a="a" xmlns:p="p"><a:p><a:r><a:t>Tenth</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide1.xml":  `<p:sld xmlns:a="a" xmlns:p="p"><a:p><a:r><a:t>First</a:t></a:r></a:p></p:sld>`,
	})
	x = Extract("deck.pptx", pptx)
	if x.Pages != 3 || strings.Index(x.Text, "First") > strings.Index(x.Text, "Second") || strings.Index(x.Text, "Second") > strings.Index(x.Text, "Tenth") {
		t.Fatalf("slides in number order: %+v", x)
	}
	xlsx := zipOf(t, map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="r"><sheets><sheet name="Budget" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>Item</t></si><si><t>Cost</t></si><si><t>Rent</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
			<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
			<row r="2"><c r="A2" t="s"><v>2</v></c><c r="C2"><v>12000</v></c></row></sheetData></worksheet>`,
	})
	x = Extract("budget.xlsx", xlsx)
	if x.Pages != 1 || !strings.Contains(x.Text, "--- sheet Budget ---") || !strings.Contains(x.Text, "Item\tCost\nRent\t\t12000") {
		t.Fatalf("%+v", x)
	}
	odt := zipOf(t, map[string]string{"mimetype": mimeOdt, "content.xml": `<office:document-content xmlns:text="t" xmlns:office="o"><office:body><office:text>
		<text:h>Title</text:h><text:p>a<text:s text:c="2"/>b</text:p></office:text></office:body></office:document-content>`})
	x = Extract("notat.odt", odt)
	if x.Mime != mimeOdt || !strings.Contains(x.Text, "Title") || !strings.Contains(x.Text, "a  b") {
		t.Fatalf("%q", x.Text)
	}
	x = Extract("stuff.zip", zipOf(t, map[string]string{"a.txt": "x"}))
	if x.Kind != "binary" || x.Mime != "application/zip" {
		t.Fatal(x)
	}
}

func TestExtractBrokenPDF(t *testing.T) {
	x := Extract("broken.pdf", []byte("%PDF-1.7\nnot really a pdf"))
	if x.Kind != "document" || x.Note == "" {
		t.Fatalf("a broken PDF is stored with a note: %+v", x)
	}
}

func TestImagePreview(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3000, 1000))
	for x := 0; x < 100; x++ { // a red block at the left end, to see where it ends up after turning
		for y := 0; y < 1000; y++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	x := Extract("wide.png", b.Bytes())
	if x.Kind != "image" || x.Width != 3000 || x.Height != 1000 || x.Preview == nil {
		t.Fatalf("%+v", x)
	}
	p, err := jpeg.Decode(bytes.NewReader(x.Preview))
	if err != nil || p.Bounds().Dx() != PreviewSize || p.Bounds().Dy() != 512 {
		t.Fatal("preview size", p.Bounds(), err)
	}

	// a JPEG stored sideways with orientation 6 (turn right to show): the preview must be upright
	var j bytes.Buffer
	_ = jpeg.Encode(&j, img, nil)
	jb := withOrientation(j.Bytes(), 6)
	if o := exifOrientation(jb); o != 6 {
		t.Fatal("orientation", o)
	}
	x = Extract("phone.jpg", jb)
	if x.Width != 1000 || x.Height != 3000 {
		t.Fatalf("upright size: %dx%d", x.Width, x.Height)
	}
	p, _ = jpeg.Decode(bytes.NewReader(x.Preview))
	if p.Bounds().Dx() != 512 || p.Bounds().Dy() != PreviewSize {
		t.Fatal("upright preview", p.Bounds())
	}
	// turned right, the left end is now the top
	if r, g, _, _ := p.At(256, 10).RGBA(); r < 0xc000 || g > 0x4000 {
		t.Fatal("the red block should be at the top", r, g)
	}
}

// withOrientation puts a minimal EXIF segment with an orientation tag after the JPEG's start marker.
func withOrientation(j []byte, o byte) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, o, 0, 0, 0, 0, 0, 0}
	seg := append([]byte("Exif\x00\x00"), tiff...)
	n := len(seg) + 2
	app1 := append([]byte{0xFF, 0xE1, byte(n >> 8), byte(n)}, seg...)
	return append(append(append([]byte{}, j[:2]...), app1...), j[2:]...)
}

func TestFindAndRead(t *testing.T) {
	text := strings.Repeat("filler text here. ", 100) + "The invoice total is 4200 NOK, due Friday. " + strings.Repeat("more filler. ", 100) + "Invoice paid."
	hits := Find(text, "invoice total", 10)
	if len(hits) != 1 || !strings.Contains(hits[0].Snippet, "4200 NOK") {
		t.Fatal(hits)
	}
	if all := Find(text, "INVOICE", 10); len(all) != 2 {
		t.Fatal("case-insensitive, two places", all)
	}
	part, total := Read(text, hits[0].Offset, 50)
	if total != len([]rune(text)) || len([]rune(part)) != 50 {
		t.Fatal(part, total)
	}
	if hits := Find("Blåbær og tyttebær. Blåbærsyltetøy.", "blåbær", 10); len(hits) != 1 || hits[0].Offset != 0 {
		t.Fatal("non-ASCII, and snippets do not overlap", hits)
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		`../../etc/passwd`:       "passwd",
		`C:\Users\x\rapport.pdf`: "rapport.pdf",
		"a\x00b\"c.txt":          "abc.txt",
		"..":                     "file",
		"":                       "file",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStore(t *testing.T) {
	url := os.Getenv("ATHENA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ATHENA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pg.Open(ctx, url, 2, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := fs.Sub(Migrations, "migrations")
	if err := pg.Migrate(ctx, db, sub, "files_migrations"); err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: db, MaxBytes: 1 << 20, Quota: 3000}
	ten := "f-" + newID()[:8]
	f, err := s.Put(ctx, ten, "me", "notes.md", []byte("# Plan\nBuy milk"))
	if err != nil || f.Kind != "text" || f.Chars != 15 || f.SHA256 == "" {
		t.Fatal(f, err)
	}
	if _, err := s.Get(ctx, ten, "other", f.ID); err != ErrNotFound {
		t.Fatal("another subject must not see it")
	}
	if _, txt, _ := s.Text(ctx, ten, "me", f.ID); txt != "# Plan\nBuy milk" {
		t.Fatal(txt)
	}
	if _, b, _ := s.Content(ctx, ten, "me", f.ID, false); string(b) != "# Plan\nBuy milk" {
		t.Fatal(string(b))
	}
	if _, _, err := s.Content(ctx, ten, "me", f.ID, true); err != ErrNotFound {
		t.Fatal("a text file has no preview")
	}
	if _, err := s.Put(ctx, ten, "me", "big.txt", bytes.Repeat([]byte("x"), 3000)); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatal("over quota must be refused", err)
	}
	if _, err := s.Put(ctx, ten, "me", "huge.txt", bytes.Repeat([]byte("x"), 2<<20)); err == nil {
		t.Fatal("over the size limit must be refused")
	}
	if u, _ := s.Usage(ctx, ten, "me"); u.Files != 1 || u.Bytes != 15 {
		t.Fatal(u)
	}
	if err := s.Delete(ctx, ten, "other", f.ID); err != ErrNotFound {
		t.Fatal("another subject must not delete it")
	}
	if err := s.Delete(ctx, ten, "me", f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ten, "me", "not-an-id"); err != ErrNotFound {
		t.Fatal(err)
	}
}

// tinyPDF builds a valid two-page PDF with one line of text on each page.
func tinyPDF(lines ...string) []byte {
	var b bytes.Buffer
	var offs []int
	obj := func(s string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), s)
	}
	b.WriteString("%PDF-1.4\n")
	kids := ""
	for i := range lines {
		kids += fmt.Sprintf("%d 0 R ", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, len(lines)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, l := range lines {
		stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", l)
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return b.Bytes()
}

func TestExtractPDF(t *testing.T) {
	x := Extract("report.pdf", tinyPDF("Quarterly report for the boat club", "Membership grew to forty two people"))
	if x.Kind != "document" || x.Pages != 2 {
		t.Fatalf("%+v", x)
	}
	if !strings.Contains(x.Text, "--- page 1 ---") || !strings.Contains(x.Text, "boat club") || !strings.Contains(x.Text, "--- page 2 ---") || !strings.Contains(x.Text, "forty two") {
		t.Fatalf("%q", x.Text)
	}
	if x.Note != "" {
		t.Fatal("a PDF with text has no note:", x.Note)
	}
}
