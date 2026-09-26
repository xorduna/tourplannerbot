package telegram

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"
)

var orderedListItemPattern = regexp.MustCompile(`^(\d+)[.)]\s+(.+)$`)

type markdownTable struct {
	headers []string
	rows    [][]string
}

// formatTelegramHTML converts the common Markdown emitted by the model into
// the small HTML subset supported by Telegram. All text is escaped first so a
// model response cannot introduce raw Telegram HTML.
func formatTelegramHTML(markdown string) string {
	formattedHTML, _ := formatTelegramMarkdown(markdown, false)
	return formattedHTML
}

// formatTelegramRichHTML converts Markdown to Telegram Rich HTML and emits
// native table tags when at least one valid GitHub-style table is present.
func formatTelegramRichHTML(markdown string) (string, bool) {
	return formatTelegramMarkdown(markdown, true)
}

// formatTelegramMarkdown formats common Markdown and chooses between native
// rich tables and a readable list fallback for regular Telegram messages.
func formatTelegramMarkdown(markdown string, useRichTables bool) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	formattedLines := make([]string, 0, len(lines))
	inCodeBlock := false
	codeLines := make([]string, 0)
	hasTables := false

	flushCodeBlock := func() {
		formattedLines = append(formattedLines, "<pre>"+html.EscapeString(strings.Join(codeLines, "\n"))+"</pre>")
		codeLines = codeLines[:0]
	}

	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		line := lines[lineIndex]
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCodeBlock {
				flushCodeBlock()
				inCodeBlock = false
			} else {
				inCodeBlock = true
			}
			continue
		}
		if inCodeBlock {
			codeLines = append(codeLines, line)
			continue
		}
		if table, lastTableLineIndex, isTable := parseMarkdownTable(lines, lineIndex); isTable {
			hasTables = true
			if useRichTables {
				formattedLines = append(formattedLines, formatTelegramRichTable(table))
			} else {
				formattedLines = append(formattedLines, formatTelegramTableFallback(table))
			}
			lineIndex = lastTableLineIndex
			continue
		}

		formattedLines = append(formattedLines, formatTelegramLine(line))
	}
	if inCodeBlock {
		flushCodeBlock()
	}

	return strings.TrimSpace(strings.Join(formattedLines, "\n")), hasTables
}

// parseMarkdownTable recognizes a GitHub-style header, delimiter row, and any
// following rows with the same number of cells.
func parseMarkdownTable(lines []string, firstLineIndex int) (markdownTable, int, bool) {
	if firstLineIndex+1 >= len(lines) {
		return markdownTable{}, firstLineIndex, false
	}
	headers, validHeader := parseMarkdownTableRow(lines[firstLineIndex])
	delimiters, validDelimiters := parseMarkdownTableRow(lines[firstLineIndex+1])
	if !validHeader || !validDelimiters || len(headers) < 2 || len(delimiters) != len(headers) {
		return markdownTable{}, firstLineIndex, false
	}
	for _, delimiter := range delimiters {
		trimmedDelimiter := strings.Trim(strings.TrimSpace(delimiter), ":")
		if len(trimmedDelimiter) < 3 || strings.Trim(trimmedDelimiter, "-") != "" {
			return markdownTable{}, firstLineIndex, false
		}
	}

	table := markdownTable{headers: headers, rows: make([][]string, 0)}
	lastTableLineIndex := firstLineIndex + 1
	for candidateLineIndex := firstLineIndex + 2; candidateLineIndex < len(lines); candidateLineIndex++ {
		row, validRow := parseMarkdownTableRow(lines[candidateLineIndex])
		if !validRow || len(row) != len(headers) {
			break
		}
		table.rows = append(table.rows, row)
		lastTableLineIndex = candidateLineIndex
	}
	return table, lastTableLineIndex, true
}

// parseMarkdownTableRow splits one pipe-delimited row while preserving escaped
// pipe characters inside cell text.
func parseMarkdownTableRow(line string) ([]string, bool) {
	trimmedLine := strings.TrimSpace(line)
	if !strings.Contains(trimmedLine, "|") {
		return nil, false
	}
	trimmedLine = strings.TrimPrefix(trimmedLine, "|")
	trimmedLine = strings.TrimSuffix(trimmedLine, "|")

	cells := make([]string, 0)
	var currentCell strings.Builder
	escaped := false
	for _, character := range trimmedLine {
		if escaped {
			if character != '|' {
				currentCell.WriteRune('\\')
			}
			currentCell.WriteRune(character)
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '|' {
			cells = append(cells, strings.TrimSpace(currentCell.String()))
			currentCell.Reset()
			continue
		}
		currentCell.WriteRune(character)
	}
	if escaped {
		currentCell.WriteRune('\\')
	}
	cells = append(cells, strings.TrimSpace(currentCell.String()))
	return cells, true
}

// formatTelegramRichTable produces sanitized Rich HTML understood by
// Telegram's native table renderer.
func formatTelegramRichTable(table markdownTable) string {
	var formattedTable strings.Builder
	formattedTable.WriteString("<table bordered striped compact><tr>")
	for _, header := range table.headers {
		formattedTable.WriteString("<th>")
		formattedTable.WriteString(formatTelegramInline(header))
		formattedTable.WriteString("</th>")
	}
	formattedTable.WriteString("</tr>")
	for _, row := range table.rows {
		formattedTable.WriteString("<tr>")
		for _, cell := range row {
			formattedTable.WriteString("<td>")
			formattedTable.WriteString(formatTelegramInline(cell))
			formattedTable.WriteString("</td>")
		}
		formattedTable.WriteString("</tr>")
	}
	formattedTable.WriteString("</table>")
	return formattedTable.String()
}

// formatTelegramTableFallback renders table rows as labeled cards that remain
// readable in regular Telegram HTML messages and on older clients.
func formatTelegramTableFallback(table markdownTable) string {
	formattedRows := make([]string, 0, len(table.rows))
	for _, row := range table.rows {
		rowLines := []string{"• " + formatTelegramTablePrimaryCell(row[0])}
		for cellIndex := 1; cellIndex < len(row); cellIndex++ {
			if row[cellIndex] == "" {
				continue
			}
			rowLines = append(rowLines, "  <b>"+formatTelegramInline(table.headers[cellIndex])+":</b> "+formatTelegramInline(row[cellIndex]))
		}
		formattedRows = append(formattedRows, strings.Join(rowLines, "\n"))
	}
	return strings.Join(formattedRows, "\n\n")
}

// formatTelegramTablePrimaryCell makes the first column bold without creating
// redundant nested tags when the model already emphasized the cell.
func formatTelegramTablePrimaryCell(cell string) string {
	trimmedCell := strings.TrimSpace(cell)
	for _, marker := range []string{"**", "__"} {
		if strings.HasPrefix(trimmedCell, marker) && strings.HasSuffix(trimmedCell, marker) && len(trimmedCell) > 2*len(marker) {
			trimmedCell = strings.TrimSpace(trimmedCell[len(marker) : len(trimmedCell)-len(marker)])
			break
		}
	}
	return "<b>" + formatTelegramInline(trimmedCell) + "</b>"
}

func formatTelegramLine(line string) string {
	trimmedLine := strings.TrimSpace(line)
	if trimmedLine == "---" || trimmedLine == "***" || trimmedLine == "___" {
		return "────────"
	}
	if heading, isHeading := markdownHeading(trimmedLine); isHeading {
		return "<b>" + formatTelegramInline(heading) + "</b>"
	}
	if strings.HasPrefix(trimmedLine, "> ") {
		return "│ " + formatTelegramInline(strings.TrimPrefix(trimmedLine, "> "))
	}
	if item, isBullet := markdownBulletItem(line); isBullet {
		return "• " + formatTelegramInline(item)
	}
	if matches := orderedListItemPattern.FindStringSubmatch(trimmedLine); matches != nil {
		return matches[1] + ". " + formatTelegramInline(matches[2])
	}
	return formatTelegramInline(line)
}

func markdownHeading(line string) (string, bool) {
	headingLength := 0
	for headingLength < len(line) && line[headingLength] == '#' {
		headingLength++
	}
	if headingLength == 0 || headingLength > 6 || len(line) == headingLength || line[headingLength] != ' ' {
		return "", false
	}
	return strings.TrimSpace(line[headingLength:]), true
}

func markdownBulletItem(line string) (string, bool) {
	trimmedLeft := strings.TrimLeft(line, " \t")
	if len(trimmedLeft) < 3 || (trimmedLeft[0] != '-' && trimmedLeft[0] != '*' && trimmedLeft[0] != '+') || trimmedLeft[1] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmedLeft[2:]), true
}

func formatTelegramInline(markdown string) string {
	var formatted strings.Builder
	for position := 0; position < len(markdown); {
		if strings.HasPrefix(markdown[position:], "\\") {
			_, characterSize := utf8.DecodeRuneInString(markdown[position+1:])
			if position+1 < len(markdown) && characterSize > 0 {
				formatted.WriteString(html.EscapeString(markdown[position+1 : position+1+characterSize]))
				position += 1 + characterSize
				continue
			}
		}
		if strings.HasPrefix(markdown[position:], "[") {
			if endLabel := strings.Index(markdown[position:], "]("); endLabel > 1 {
				labelEnd := position + endLabel
				if endURL := strings.Index(markdown[labelEnd+2:], ")"); endURL >= 0 {
					urlEnd := labelEnd + 2 + endURL
					if linkURL, valid := telegramURL(markdown[labelEnd+2 : urlEnd]); valid {
						formatted.WriteString("<a href=\"")
						formatted.WriteString(html.EscapeString(linkURL))
						formatted.WriteString("\">")
						formatted.WriteString(formatTelegramInline(markdown[position+1 : labelEnd]))
						formatted.WriteString("</a>")
						position = urlEnd + 1
						continue
					}
				}
			}
		}
		if marker, tag, found := markdownInlineMarker(markdown[position:]); found {
			contentStart := position + len(marker)
			if contentEnd := strings.Index(markdown[contentStart:], marker); contentEnd >= 0 {
				contentEnd += contentStart
				if contentEnd > contentStart {
					formatted.WriteString("<")
					formatted.WriteString(tag)
					formatted.WriteString(">")
					if marker == "`" {
						formatted.WriteString(html.EscapeString(markdown[contentStart:contentEnd]))
					} else {
						formatted.WriteString(formatTelegramInline(markdown[contentStart:contentEnd]))
					}
					formatted.WriteString("</")
					formatted.WriteString(tag)
					formatted.WriteString(">")
					position = contentEnd + len(marker)
					continue
				}
			}
		}

		character, characterSize := utf8.DecodeRuneInString(markdown[position:])
		formatted.WriteString(html.EscapeString(string(character)))
		position += characterSize
	}
	return formatted.String()
}

func markdownInlineMarker(markdown string) (marker string, tag string, found bool) {
	switch {
	case strings.HasPrefix(markdown, "**"):
		return "**", "b", true
	case strings.HasPrefix(markdown, "__"):
		return "__", "u", true
	case strings.HasPrefix(markdown, "~~"):
		return "~~", "s", true
	case strings.HasPrefix(markdown, "`"):
		return "`", "code", true
	case strings.HasPrefix(markdown, "*"):
		return "*", "i", true
	case strings.HasPrefix(markdown, "_"):
		return "_", "i", true
	default:
		return "", "", false
	}
}

func telegramURL(rawURL string) (string, bool) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") || parsedURL.Host == "" {
		return "", false
	}
	return rawURL, true
}

// disabledLinkPreview suppresses Telegram's link preview card. Answers cite the
// page every web fact came from, so a message commonly carries several links
// and Telegram would otherwise expand the first one into a large card that
// buries the answer itself.
func disabledLinkPreview() *models.LinkPreviewOptions {
	linkPreviewDisabled := true
	return &models.LinkPreviewOptions{IsDisabled: &linkPreviewDisabled}
}
