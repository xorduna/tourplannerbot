package telegram

import "testing"

func TestFormatTelegramHTMLConvertsCommonMarkdown(t *testing.T) {
	markdown := "## Títol **important**\n\n- **Primer** punt\n- Segon amb *cursiva* i `codi`\n1. [Enllaç](https://example.com/a?x=1&y=2)\n\n---\n\n<text>"
	want := "<b>Títol <b>important</b></b>\n\n• <b>Primer</b> punt\n• Segon amb <i>cursiva</i> i <code>codi</code>\n1. <a href=\"https://example.com/a?x=1&amp;y=2\">Enllaç</a>\n\n────────\n\n&lt;text&gt;"

	if got := formatTelegramHTML(markdown); got != want {
		t.Errorf("formatTelegramHTML() = %q, want %q", got, want)
	}
}

func TestFormatTelegramHTMLEscapesAndKeepsInvalidLinksAsText(t *testing.T) {
	markdown := "[No segur](javascript:alert(1)) & **negreta**"
	want := "[No segur](javascript:alert(1)) &amp; <b>negreta</b>"

	if got := formatTelegramHTML(markdown); got != want {
		t.Errorf("formatTelegramHTML() = %q, want %q", got, want)
	}
}

func TestFormatTelegramHTMLFormatsFencedCode(t *testing.T) {
	markdown := "```go\nif a < b {\n}\n```"
	want := "<pre>if a &lt; b {\n}</pre>"

	if got := formatTelegramHTML(markdown); got != want {
		t.Errorf("formatTelegramHTML() = %q, want %q", got, want)
	}
}

// TestFormatTelegramRichHTMLFormatsMarkdownTable verifies valid Markdown tables
// become sanitized native Telegram tables.
func TestFormatTelegramRichHTMLFormatsMarkdownTable(t *testing.T) {
	markdown := "Restaurants\n\n| Restaurant | Distància | Tipus |\n|---|---:|---|\n| **Lactuca** | 77 m | espanyola |\n| Pötstot | 133 m | vegana & saludable |"
	want := "Restaurants\n\n<table bordered striped compact><tr><th>Restaurant</th><th>Distància</th><th>Tipus</th></tr><tr><td><b>Lactuca</b></td><td>77 m</td><td>espanyola</td></tr><tr><td>Pötstot</td><td>133 m</td><td>vegana &amp; saludable</td></tr></table>"

	got, hasTables := formatTelegramRichHTML(markdown)
	if !hasTables {
		t.Fatal("formatTelegramRichHTML did not detect the Markdown table")
	}
	if got != want {
		t.Errorf("formatTelegramRichHTML() = %q, want %q", got, want)
	}
}

// TestFormatTelegramHTMLUsesReadableTableFallback verifies regular messages do
// not expose raw Markdown pipes when Rich Messages are unavailable.
func TestFormatTelegramHTMLUsesReadableTableFallback(t *testing.T) {
	markdown := "| Restaurant | Distància | Tipus |\n|---|---:|---|\n| **Lactuca** | 77 m | espanyola |"
	want := "• <b>Lactuca</b>\n  <b>Distància:</b> 77 m\n  <b>Tipus:</b> espanyola"

	if got := formatTelegramHTML(markdown); got != want {
		t.Errorf("formatTelegramHTML() = %q, want %q", got, want)
	}
}

// TestFormatTelegramRichHTMLIgnoresPipesOutsideTables verifies ordinary pipe
// characters are not mistaken for table syntax.
func TestFormatTelegramRichHTMLIgnoresPipesOutsideTables(t *testing.T) {
	markdown := "A | B is ordinary text"
	got, hasTables := formatTelegramRichHTML(markdown)
	if hasTables {
		t.Fatal("formatTelegramRichHTML detected a table without a delimiter row")
	}
	if got != markdown {
		t.Errorf("formatTelegramRichHTML() = %q, want %q", got, markdown)
	}
}
