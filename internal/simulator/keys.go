package simulator

// HID keyboard usages the text mapping needs beyond letters and digits.
const (
	usageLeftShift = 0xE1
	usageReturn    = 0x28
	usageTab       = 0x2B
)

// maxUsagesPerCommand bounds one keys command, so a long text is spread over
// several commands that each finish well inside the helper's own limit.
const maxUsagesPerCommand = 64

// asciiUsage maps a character on the US layout to its HID usage and whether
// shift is needed. ok is false for anything that has no key.
func asciiUsage(r rune) (usage int, shift, ok bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return 4 + int(r-'a'), false, true
	case r >= 'A' && r <= 'Z':
		return 4 + int(r-'A'), true, true
	case r >= '1' && r <= '9':
		return 30 + int(r-'1'), false, true
	case r == '0':
		return 39, false, true
	}
	switch r {
	case '\n':
		return usageReturn, false, true
	case '\t':
		return usageTab, false, true
	case ' ':
		return 0x2C, false, true
	}
	return symbolUsage(r)
}

// symbolUsage maps punctuation on the US layout.
func symbolUsage(r rune) (usage int, shift, ok bool) {
	switch r {
	case '-':
		return 0x2D, false, true
	case '=':
		return 0x2E, false, true
	case '[':
		return 0x2F, false, true
	case ']':
		return 0x30, false, true
	case '\\':
		return 0x31, false, true
	case ';':
		return 0x33, false, true
	case '\'':
		return 0x34, false, true
	case '`':
		return 0x35, false, true
	case ',':
		return 0x36, false, true
	case '.':
		return 0x37, false, true
	case '/':
		return 0x38, false, true
	case '!':
		return 30, true, true
	case '@':
		return 31, true, true
	case '#':
		return 32, true, true
	case '$':
		return 33, true, true
	case '%':
		return 34, true, true
	case '^':
		return 35, true, true
	case '&':
		return 36, true, true
	case '*':
		return 37, true, true
	case '(':
		return 38, true, true
	case ')':
		return 39, true, true
	case '_':
		return 0x2D, true, true
	case '+':
		return 0x2E, true, true
	case '{':
		return 0x2F, true, true
	case '}':
		return 0x30, true, true
	case '|':
		return 0x31, true, true
	case ':':
		return 0x33, true, true
	case '"':
		return 0x34, true, true
	case '~':
		return 0x35, true, true
	case '<':
		return 0x36, true, true
	case '>':
		return 0x37, true, true
	case '?':
		return 0x38, true, true
	}
	return 0, false, false
}

// keyChunks maps text to keys commands. ok is false when any character has no
// key, in which case the text goes through the clipboard instead. A shifted
// character's modifier and key always land in the same chunk.
func keyChunks(text string) (chunks [][]int, ok bool) {
	var cur []int
	for _, r := range text {
		usage, shift, found := asciiUsage(r)
		if !found {
			return nil, false
		}
		n := 1
		if shift {
			n = 2
		}
		if len(cur)+n > maxUsagesPerCommand {
			chunks = append(chunks, cur)
			cur = nil
		}
		if shift {
			cur = append(cur, usageLeftShift)
		}
		cur = append(cur, usage)
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, true
}
