package telegrambot

import (
	"html"
	"unicode/utf16"
)

func trimTelegram(text string) string {
	if telegramUTF16Len(text) <= telegramLimit {
		return text
	}
	runes := []rune(text)
	n := telegramRunePrefixLen(runes, telegramLimit-64)
	return string(runes[:n]) + "\n...[truncated]"
}

func trimTelegramTail(text string) string {
	return trimTelegramTailLimit(text, telegramLimit)
}

func trimTelegramTailLimit(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if telegramUTF16Len(text) <= limit {
		return text
	}
	marker := "…[truncated]\n"
	budget := limit - telegramUTF16Len(marker)
	if budget <= 0 {
		return marker
	}
	return marker + trimToUTF16Tail(text, budget)
}

func trimToUTF16Tail(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if telegramUTF16Len(text) <= limit {
		return text
	}
	marker := "…"
	budget := limit - telegramUTF16Len(marker)
	if budget <= 0 {
		return marker
	}
	runes := []rune(text)
	n := telegramRuneSuffixLen(runes, budget)
	return marker + string(runes[len(runes)-n:])
}

func esc(text string) string {
	return html.EscapeString(text)
}

func telegramUTF16Len(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func splitTelegramUTF16Raw(text string, limit int) []string {
	if limit <= 0 {
		limit = 1
	}
	runes := []rune(text)
	var chunks []string
	for len(runes) > 0 {
		n := telegramRunePrefixLen(runes, limit)
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}

func telegramRunePrefixLen(runes []rune, limit int) int {
	if len(runes) == 0 {
		return 0
	}
	used := 0
	for i, r := range runes {
		next := 1
		if r > 0xFFFF {
			next = 2
		}
		if used+next > limit {
			return max(1, i)
		}
		used += next
	}
	return len(runes)
}

func telegramRuneSuffixLen(runes []rune, limit int) int {
	if len(runes) == 0 {
		return 0
	}
	used := 0
	for i := len(runes) - 1; i >= 0; i-- {
		next := 1
		if runes[i] > 0xFFFF {
			next = 2
		}
		if used+next > limit {
			return max(1, len(runes)-1-i)
		}
		used += next
	}
	return len(runes)
}
