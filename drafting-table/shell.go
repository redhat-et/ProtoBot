package guard

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type parsedShell struct {
	words   []string
	heredoc bool
	stdin   string
}

const (
	maxShellCommandBytes  = 1 << 20
	maxShellAnalysisDepth = 8
)

var assignmentWord = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*|BASH_FUNC_[^=]*%%)=`)

func parseSimpleShell(command string) (parsedShell, error) {
	if len(command) > maxShellCommandBytes {
		return parsedShell{}, errors.New("command exceeds the 1 MiB limit")
	}
	var words []string
	var word strings.Builder
	wordStarted := false
	quote := byte(0)
	for i := 0; i < len(command); i++ {
		char := command[i]
		if quote == '\'' {
			if char == '\'' {
				quote = 0
			} else {
				word.WriteByte(char)
			}
			continue
		}
		if quote == '"' {
			switch char {
			case '"':
				quote = 0
			case '$', '`':
				return parsedShell{}, errors.New("variable expansion and command substitution are not allowed")
			case '\\':
				if i+1 >= len(command) {
					return parsedShell{}, errors.New("unfinished shell escape")
				}
				i++
				if command[i] == '\n' {
					return parsedShell{}, errors.New("continued shell commands are not allowed")
				}
				word.WriteByte(command[i])
			default:
				word.WriteByte(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			wordStarted = true
			quote = char
		case '\\':
			if i+1 >= len(command) {
				return parsedShell{}, errors.New("unfinished shell escape")
			}
			i++
			if command[i] == '\n' {
				return parsedShell{}, errors.New("continued shell commands are not allowed")
			}
			wordStarted = true
			word.WriteByte(command[i])
		case '$', '`':
			return parsedShell{}, errors.New("variable expansion and command substitution are not allowed")
		case '*', '?', '[', '{', '}':
			return parsedShell{}, errors.New("unquoted pathname expansion is not allowed")
		case '#':
			return parsedShell{}, errors.New("shell comments are not part of an allowed command form")
		case ';', '|', '&', '(', ')':
			return parsedShell{}, errors.New("compound and wrapped shell commands are not allowed")
		case '<':
			if i+1 < len(command) && command[i+1] == '<' {
				if wordStarted {
					words = append(words, word.String())
					word.Reset()
					wordStarted = false
				}
				return parseQuotedHereDocument(command, i, words)
			}
			return parsedShell{}, errors.New("input redirection is not allowed")
		case '>':
			return parsedShell{}, errors.New("output redirection is not allowed")
		case '\n':
			if strings.TrimSpace(command[i+1:]) != "" {
				return parsedShell{}, errors.New("compound and wrapped shell commands are not allowed")
			}
			if wordStarted {
				words = append(words, word.String())
				word.Reset()
				wordStarted = false
			}
		case ' ', '\t', '\r':
			if wordStarted {
				words = append(words, word.String())
				word.Reset()
				wordStarted = false
			}
		default:
			wordStarted = true
			if word.Len() == 0 && char == '~' {
				return parsedShell{}, errors.New("tilde expansion is not allowed")
			}
			word.WriteByte(char)
		}
	}
	if quote != 0 {
		return parsedShell{}, errors.New("unclosed shell quote")
	}
	if wordStarted {
		words = append(words, word.String())
	}
	if len(words) == 0 {
		return parsedShell{}, errors.New("shell command is empty")
	}
	if assignmentWord.MatchString(words[0]) {
		return parsedShell{}, errors.New("environment assignments are not allowed")
	}
	return parsedShell{words: words}, nil
}

func parseQuotedHereDocument(command string, operator int, words []string) (parsedShell, error) {
	if len(words) == 0 {
		return parsedShell{}, errors.New("here-document has no command")
	}
	index := operator + 2
	for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
		index++
	}
	if index >= len(command) || (command[index] != '\'' && command[index] != '"') {
		return parsedShell{}, errors.New("here-document delimiter must be quoted")
	}
	delimiterQuote := command[index]
	index++
	start := index
	for index < len(command) && command[index] != delimiterQuote {
		if command[index] == '\n' || command[index] == '\r' || command[index] == 0 ||
			(delimiterQuote == '"' && (command[index] == '$' || command[index] == '`' || command[index] == '\\')) {
			return parsedShell{}, errors.New("here-document delimiter is not a literal word")
		}
		index++
	}
	if index >= len(command) {
		return parsedShell{}, errors.New("here-document delimiter is not closed")
	}
	delimiter := command[start:index]
	if delimiter == "" || len(delimiter) > 128 || strings.ContainsAny(delimiter, "\x00\r\n") {
		return parsedShell{}, errors.New("here-document delimiter is invalid")
	}
	index++
	for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
		index++
	}
	if index >= len(command) || command[index] != '\n' {
		return parsedShell{}, errors.New("here-document must be followed by its body")
	}
	bodyStart := index + 1
	for lineStart := bodyStart; lineStart <= len(command); {
		lineEnd := strings.IndexByte(command[lineStart:], '\n')
		end := len(command)
		if lineEnd >= 0 {
			end = lineStart + lineEnd
		}
		line := strings.TrimSuffix(command[lineStart:end], "\r")
		if line == delimiter {
			trailing := command[end:]
			if strings.TrimSpace(trailing) != "" {
				return parsedShell{}, errors.New("here-document ended early or a second command follows")
			}
			return parsedShell{words: words, heredoc: true, stdin: command[bodyStart:lineStart]}, nil
		}
		if end == len(command) {
			break
		}
		lineStart = end + 1
	}
	return parsedShell{}, errors.New("here-document terminator is missing")
}

func parseOtherShellRedirects(command string) ([]string, bool) {
	return parseOtherShellRedirectsAtDepth(command, 0)
}

func parseOtherShellRedirectsAtDepth(command string, depth int) ([]string, bool) {
	if len(command) > maxShellCommandBytes || depth > maxShellAnalysisDepth {
		return nil, true
	}
	var targets []string
	quote := byte(0)
	for i := 0; i < len(command); i++ {
		char := command[i]
		if quote == '\'' {
			if char == '\'' {
				quote = 0
			}
			continue
		}
		if quote == '"' {
			switch char {
			case '"':
				quote = 0
			case '\\':
				i++
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case '\\':
			i++
		case '<':
			if i+1 < len(command) && command[i+1] == '<' {
				document, ok := parseOtherHereDocument(command, i)
				if !ok {
					return targets, true
				}
				trailingTargets, unsafe := parseOtherShellRedirectsAtDepth(document.tail, depth+1)
				if unsafe {
					return targets, true
				}
				targets = append(targets, trailingTargets...)
				i = document.afterBody - 1
			}
		case '>':
			operatorEnd := i + 1
			if operatorEnd < len(command) && (command[operatorEnd] == '>' || command[operatorEnd] == '|') {
				operatorEnd++
			}
			if operatorEnd < len(command) && command[operatorEnd] == '&' {
				// Bash/zsh classify the complete word after &: only an all-digit
				// word duplicates a descriptor, and only "-" closes it.
				operandStart := operatorEnd + 1
				for operandStart < len(command) && (command[operandStart] == ' ' || command[operandStart] == '\t') {
					operandStart++
				}
				operand, end, ok := readShellTarget(command, operandStart)
				if !ok {
					return targets, true
				}
				if operand == "-" || descriptorNumber(operand) {
					i = end - 1
					continue
				}
				if ambiguousRedirectTarget(operand) {
					return targets, true
				}
				targets = append(targets, operand)
				i = end - 1
				continue
			}
			for operatorEnd < len(command) && (command[operatorEnd] == ' ' || command[operatorEnd] == '\t') {
				operatorEnd++
			}
			target, end, ok := readShellTarget(command, operatorEnd)
			if !ok {
				return targets, true
			}
			if ambiguousRedirectTarget(target) {
				return targets, true
			}
			targets = append(targets, target)
			i = end - 1
		}
	}
	if quote != 0 {
		return targets, true
	}
	return targets, false
}

func descriptorNumber(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func ambiguousRedirectTarget(target string) bool {
	if strings.ContainsAny(target, "$`*?[]{}()") {
		return true
	}
	return strings.HasPrefix(target, "~")
}

func otherHereDocument(command string, operator int) (string, int, bool) {
	document, ok := parseOtherHereDocument(command, operator)
	if !ok {
		return "", 0, false
	}
	return document.tail, document.afterBody, true
}

type parsedOtherHereDocument struct {
	tail      string
	body      string
	afterBody int
	quoted    bool
}

func parseOtherHereDocument(command string, operator int) (parsedOtherHereDocument, bool) {
	index := operator + 2
	stripTabs := false
	if index < len(command) && command[index] == '-' {
		stripTabs = true
		index++
	}
	for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
		index++
	}
	quote := byte(0)
	if index < len(command) && (command[index] == '\'' || command[index] == '"') {
		quote = command[index]
		index++
	}
	quoted := quote != 0
	start := index
	for index < len(command) {
		if quote != 0 {
			if quote == '"' && (command[index] == '$' || command[index] == '`' || command[index] == '\\') {
				return parsedOtherHereDocument{}, false
			}
			if command[index] == quote {
				break
			}
		} else {
			if strings.ContainsRune(" \t\r\n;|&<>", rune(command[index])) {
				break
			}
			if command[index] == '$' || command[index] == '`' || command[index] == '\\' {
				return parsedOtherHereDocument{}, false
			}
		}
		index++
	}
	delimiter := command[start:index]
	if delimiter == "" {
		return parsedOtherHereDocument{}, false
	}
	if quote != 0 {
		if index >= len(command) {
			return parsedOtherHereDocument{}, false
		}
		index++
	}
	delimiterEnd := index
	headerLineBreak := strings.IndexByte(command[index:], '\n')
	if headerLineBreak < 0 {
		return parsedOtherHereDocument{}, false
	}
	headerEnd := index + headerLineBreak
	bodyStart := headerEnd + 1
	lineStart := bodyStart
	for lineStart <= len(command) {
		lineEnd := strings.IndexByte(command[lineStart:], '\n')
		end := len(command)
		if lineEnd >= 0 {
			end = lineStart + lineEnd
		}
		line := strings.TrimSuffix(command[lineStart:end], "\r")
		if stripTabs {
			line = strings.TrimLeft(line, "\t")
		}
		if line == delimiter {
			after := end
			if end < len(command) {
				after++
			}
			return parsedOtherHereDocument{
				tail:      command[delimiterEnd:headerEnd],
				body:      command[bodyStart:lineStart],
				afterBody: after,
				quoted:    quoted,
			}, true
		}
		if end == len(command) {
			break
		}
		lineStart = end + 1
	}
	return parsedOtherHereDocument{}, false
}

// otherShellSyntaxInspectable rejects shell syntax whose expansions or grammar
// the lightweight argv scanner cannot faithfully inspect. Single-quoted text
// remains literal data; quoted here-documents are likewise opaque data.
func otherShellSyntaxInspectable(command string) bool {
	return otherShellSyntaxInspectableAtDepth(command, 0)
}

func otherShellSyntaxInspectableAtDepth(command string, depth int) bool {
	if len(command) > maxShellCommandBytes || depth > maxShellAnalysisDepth || strings.ContainsRune(command, '\x00') {
		return false
	}
	quote := byte(0)
	wordStart := true
	for i := 0; i < len(command); i++ {
		char := command[i]
		if quote == '\'' {
			if char == '\'' {
				quote = 0
			}
			continue
		}
		if quote == '"' {
			switch char {
			case '"':
				quote = 0
			case '$', '`':
				return false
			case '\\':
				if i+1 >= len(command) || command[i+1] == '\n' {
					return false
				}
				if !strings.ContainsRune("$`\"\\", rune(command[i+1])) {
					return false
				}
				i++
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
			wordStart = false
		case '\\':
			if i+1 >= len(command) || command[i+1] == '\n' {
				return false
			}
			i++
			wordStart = false
		case '#':
			if wordStart {
				return false
			}
		case '$', '`', '*', '?', '[', '(', ')':
			return false
		case '{', '}':
			if (wordStart && (i+1 == len(command) || strings.ContainsRune(" \t\r\n;|&<>", rune(command[i+1])))) ||
				(char == '}' && wordStart) || (char == '{' && shellBraceExpansionAt(command, i)) {
				return false
			}
			wordStart = false
		case '~':
			if wordStart && i+1 < len(command) && command[i+1] != '/' && !strings.ContainsRune(" \t\r\n;|&<>", rune(command[i+1])) {
				return false
			}
			wordStart = false
		case '<':
			// Input redirection can feed unscanned code to a wrapper or
			// interpreter. The other-role path cannot prove its consumer is
			// data-only, so fail closed on files, here-documents, and here-strings.
			return false
		case ' ', '\t', '\r', '\n', ';', '|', '&', '>':
			wordStart = true
		default:
			wordStart = false
		}
	}
	return quote == 0
}

func shellBraceExpansionAt(command string, open int) bool {
	for index := open + 1; index < len(command); index++ {
		char := command[index]
		if strings.ContainsRune(" \t\r\n;|&<>", rune(char)) {
			return false
		}
		if char == '}' {
			text := command[open+1 : index]
			return strings.Contains(text, ",") || strings.Contains(text, "..")
		}
	}
	return false
}

func readShellTarget(command string, index int) (string, int, bool) {
	if index >= len(command) || strings.ContainsRune(";&|<>\n", rune(command[index])) {
		return "", index, false
	}
	var result strings.Builder
	quote := byte(0)
	for index < len(command) {
		char := command[index]
		if quote == '\'' {
			if char == '\'' {
				quote = 0
			} else {
				result.WriteByte(char)
			}
			index++
			continue
		}
		if quote == '"' {
			if char == '"' {
				quote = 0
			} else if char == '\\' {
				index++
				if index >= len(command) {
					return "", index, false
				}
				result.WriteByte(command[index])
			} else {
				result.WriteByte(char)
			}
			index++
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			index++
			continue
		}
		if char == '\\' {
			index++
			if index >= len(command) || command[index] == '\n' {
				return "", index, false
			}
			result.WriteByte(command[index])
			index++
			continue
		}
		if char == ' ' || char == '\t' || strings.ContainsRune(";&|<>\n", rune(char)) {
			break
		}
		result.WriteByte(char)
		index++
	}
	if quote != 0 || result.Len() == 0 {
		return "", index, false
	}
	return result.String(), index, true
}

func shellValueError(err error) decision {
	return refuse("rule 6", fmt.Sprintf("not an allowed shell operation: %s", err.Error()))
}
