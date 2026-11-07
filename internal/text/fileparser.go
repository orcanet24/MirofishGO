// Package text — port de backend/app/utils/file_parser.py y
// backend/app/services/text_processor.py.
//
// Extracción de texto de PDF/MD/TXT con fallback de encoding (Python usaba
// charset_normalizer/chardet; aquí golang.org/x/net/html/charset + x/text,
// puramente Go sin CGo) y chunking idéntico al original.
package text

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

// SupportedExtensions replica SUPPORTED_EXTENSIONS de FileParser.
var SupportedExtensions = map[string]bool{".pdf": true, ".md": true, ".markdown": true, ".txt": true}

// IsSupported — clase-method de FileParser.
func IsSupported(filePath string) bool {
	return SupportedExtensions[strings.ToLower(filepath.Ext(filePath))]
}

// ExtractText replica FileParser.extract_text.
func ExtractText(filePath string) (string, error) {
	st, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("文件不存在: %s", filePath)
	}
	_ = st

	suffix := strings.ToLower(filepath.Ext(filePath))
	switch suffix {
	case ".pdf":
		return extractFromPDF(filePath)
	case ".md", ".markdown", ".txt":
		return ReadTextWithFallback(filePath)
	default:
		return "", fmt.Errorf("不支持的文件格式: %s", suffix)
	}
}

// extractFromPDF replica _extract_from_pdf con fitz. Aquí: ledongthuc/pdf
// GetPlainText (todo el documento de una vez; PyMuPDF unía páginas con \n\n,
// el extractor ya inserta saltos entre bloques de página).
func extractFromPDF(filePath string) (string, error) {
	file, reader, err := pdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("PDF ilegible: %w", err)
	}
	defer file.Close()

	plain, err := reader.GetPlainText()
	if err != nil {
		return "", err
	}
	raw, err := io.ReadAll(plain)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ReadTextWithFallback replica _read_text_with_fallback:
// UTF-8 → charset detect → decode con U+FFFD (errors='replace').
func ReadTextWithFallback(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	if utf8Valid(data) {
		return string(data), nil
	}
	// charset.DetermineEncoding siempre devuelve un encoding no-nil
	// (fallback windows-1252), igual que el chardet final de Python con
	// errors='replace' (el decoder de x/text también inválido→U+FFFD).
	enc, _, _ := charset.DetermineEncoding(data, "text/plain")
	decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), enc.NewDecoder()))
	if err != nil {
		return string(data), nil // último recurso: bytes crudos
	}
	// Python: codecs utf-16 decodifica SIN BOM; x/text lo conserva.
	return strings.TrimPrefix(string(decoded), "\ufeff"), nil
}

func utf8Valid(b []byte) bool { return utf8.Valid(b) }

// ExtractFromMultiple replica FileParser.extract_from_multiple:
// encabezados "=== 文档 N: filename ===", fallos sin abortar el total.
func ExtractFromMultiple(filePaths []string) string {
	parts := make([]string, 0, len(filePaths))
	for i, filePath := range filePaths {
		base := filepath.Base(filePath)
		txt, err := ExtractText(filePath)
		if err != nil {
			parts = append(parts, fmt.Sprintf("=== 文档 %d: %s (提取失败: %v) ===", i+1, filePath, err))
			continue
		}
		parts = append(parts, fmt.Sprintf("=== 文档 %d: %s ===\n%s", i+1, base, txt))
	}
	return strings.Join(parts, "\n\n")
}

// chunkSeparators — la lista de cortes por puntos de frase del Python.
var chunkSeparators = []string{"。", "！", "？", ".\n", "!\n", "?\n", "\n\n", ". ", "! ", "? "}

// SplitTextIntoChunks replica split_text_into_chunks: corta preferentemente
// en el último separador de frase dentro de la ventana, con overlap.
func SplitTextIntoChunks(text string, chunkSize, overlap int) []string {
	chunkSize = maxInt(chunkSize, 1)
	overlap = maxInt(overlap, 0)

	if len([]rune(text)) <= chunkSize {
		if strings.TrimSpace(text) == "" {
			return []string{}
		}
		return []string{text}
	}

	runes := []rune(text)
	chunks := []string{}
	start := 0
	for start < len(runes) {
		end := start + chunkSize
		if end < len(runes) {
			window := string(runes[start:end])
			for _, sep := range chunkSeparators {
				sepRunes := []rune(sep)
				if last := lastIndexRune(window, sepRunes); last != -1 && last > int(float64(chunkSize)*0.3) {
					end = start + last + len(sepRunes)
					break
				}
			}
		}
		// Python text[start:end] recorta automáticamente si end > len(text).
		sliceEnd := end
		if sliceEnd > len(runes) {
			sliceEnd = len(runes)
		}
		if chunk := strings.TrimSpace(string(runes[start:sliceEnd])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end < len(runes) {
			start = end - overlap
		} else {
			start = len(runes)
		}
	}
	return chunks
}

func lastIndexRune(s string, sep []rune) int {
	idx := strings.LastIndex(s, string(sep))
	if idx < 0 {
		return -1
	}
	return len([]rune(s[:idx]))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
