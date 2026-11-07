// Tests del port de file_parser.py + text_processor.py.
package text

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

func TestSplitTextIntoChunksShort(t *testing.T) {
	if got := SplitTextIntoChunks("", 500, 50); len(got) != 0 {
		t.Errorf("texto vacío → %d chunks", len(got))
	}
	if got := SplitTextIntoChunks("   \n  ", 500, 50); len(got) != 0 {
		t.Errorf("solo whitespace → %d chunks", len(got))
	}
	if got := SplitTextIntoChunks("hola", 500, 50); len(got) != 1 || got[0] != "hola" {
		t.Errorf("texto corto → %v", got)
	}
}

// El corte preferente en separador de frase dentro de la ventana.
func TestSplitTextIntoChunksSentenceBoundary(t *testing.T) {
	sentence := "Primera frase. "
	long := strings.Repeat(sentence, 60) // 900 chars
	chunks := SplitTextIntoChunks(long, 500, 50)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	c0 := chunks[0]
	if !strings.HasSuffix(c0, ".") {
		t.Errorf("primer chunk no corta en frase: %q...%q", c0[:30], c0[len(c0)-20:])
	}
}

func TestPreprocessText(t *testing.T) {
	in := "linea\r\n\r\ncon\r\n\n\n\nvacios\r\n\r\n"
	got := PreprocessText(in)
	for _, ln := range strings.Split(got, "\n") {
		if ln != strings.TrimSpace(ln) {
			t.Errorf("línea sin trim: %q", ln)
		}
	}
	if strings.Count(got, "\n\n\n") > 0 {
		t.Error("quedaron 3+ saltos seguidos")
	}
}

func TestGetTextStats(t *testing.T) {
	stats := Stats("hola mundo\nsegunda línea\n")
	if stats["total_lines"] != 3 || stats["total_words"] != 4 {
		t.Errorf("stats = %v", stats)
	}
}

func TestReadTextWithFallbackUTF8AndGBK(t *testing.T) {
	utf8 := "texto unicodeUTF8"
	fWrite := func(b []byte) string {
		path := filepath.Join(t.TempDir(), "doc.txt")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if s, err := ReadTextWithFallback(fWrite([]byte(utf8))); err != nil || s != utf8 {
		t.Errorf("UTF-8 plano: %q err=%v", s, err)
	}

	// GB18030 de chinos: texto no UTF-8 → decoder de charset con replace.
	gbk, _ := simplifiedchinese.GB18030.NewEncoder().String("你好世界")
	if s, err := ReadTextWithFallback(fWrite([]byte(gbk))); err != nil || s == "" {
		t.Errorf("GBK: %q err=%v", s, err)
	}

	// UTF-16 con BOM: DetectEncoding lo identifica seguro.
	enc := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM)
	u16, _ := enc.NewEncoder().String("hola")
	if s, err := ReadTextWithFallback(fWrite([]byte(u16))); err != nil || strings.TrimSpace(s) != "hola" {
		t.Errorf("UTF-16 BOM: %q err=%v", s, err)
	}
}

func TestIsSupported(t *testing.T) {
	for _, ext := range []string{".pdf", ".md", ".markdown", ".txt", ".TXT"} {
		if !IsSupported("file" + ext) {
			t.Errorf("%s no soportado", ext)
		}
	}
	if IsSupported("file.docx") {
		t.Error(".docx no debe soportarse")
	}
}

func TestExtractTextErrors(t *testing.T) {
	if _, err := ExtractText(filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Error("esperado error de archivo inexistente")
	}
	if _, err := ExtractText("algo.docx"); err == nil {
		t.Error("esperado error de formato no soportado")
	}
}
