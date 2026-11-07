// Port de backend/app/services/text_processor.py.
package text

import (
	"regexp"
	"strings"
)

// PreprocessText replica TextProcessor.preprocess_text: CRLF→LF, máx 2 saltos
// seguidos, trim de líneas y del conjunto.
func PreprocessText(text string) string {
	// 标准化换行
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// 移除连续空行（保留最多两个换行）
	text = reBlank.ReplaceAllString(text, "\n\n")
	// 移除行首行尾空白
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

var reBlank = regexp.MustCompile(`\n{3,}`)

// ExtractFromFiles replica TextProcessor.extract_from_files (wrapper de FileParser).
func ExtractFromFiles(filePaths []string) string { return ExtractFromMultiple(filePaths) }

// Stats replica get_text_stats: chars (runes), líneas, "palabras".
func Stats(text string) map[string]int {
	return map[string]int{
		"total_chars":  len([]rune(text)),
		"total_lines":  strings.Count(text, "\n") + 1,
		"total_words":  len(strings.Fields(text)),
	}
}
