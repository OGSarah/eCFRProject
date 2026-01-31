package ecfr

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"unicode"
)

type ChapterAgg struct {
	Chapter string
	Text    bytes.Buffer
}

var wsRe = regexp.MustCompile(`\s+`)

func ParseTitleChapters(xmlBytes []byte) (map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(xmlBytes))
	dec.Strict = false

	chapters := map[string]*ChapterAgg{}
	currentChapter := "UNKNOWN"
	get := func(ch string) *ChapterAgg {
		if a, ok := chapters[ch]; ok {
			return a
		}
		a := &ChapterAgg{Chapter: ch}
		chapters[ch] = a
		return a
	}
	agg := get(currentChapter)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "DIV1") || strings.EqualFold(t.Name.Local, "DIV2") || strings.EqualFold(t.Name.Local, "DIV3") {
				typ := attr(t.Attr, "TYPE")
				if strings.EqualFold(typ, "CHAPTER") {
					n := attr(t.Attr, "N")
					if n != "" {
						currentChapter = n
						agg = get(currentChapter)
					}
				}
			}
		case xml.CharData:
			s := normalizeText(string([]byte(t)))
			if s != "" {
				agg.Text.WriteString(s)
				agg.Text.WriteByte(' ')
			}
		}
	}

	out := make(map[string]string, len(chapters))
	for ch, a := range chapters {
		out[ch] = wsRe.ReplaceAllString(a.Text.String(), " ")
	}
	return out, nil
}

func WordCount(s string) int {
	inWord := false
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				n++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return n
}

func ChecksumHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func FleschReadingEase(text string) float64 {
	words := float64(max(1, WordCount(text)))
	sentences := float64(max(1, countSentences(text)))
	syllables := float64(max(1, countSyllables(text)))
	return 206.835 - 1.015*(words/sentences) - 84.6*(syllables/words)
}

func countSentences(s string) int {
	n := 0
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Split(bufio.ScanRunes)
	for sc.Scan() {
		switch sc.Text() {
		case ".", "!", "?":
			n++
		}
	}
	return max(1, n)
}

func countSyllables(s string) int {
	s = strings.ToLower(s)
	vowels := "aeiouy"
	inVowel := false
	n := 0
	for _, r := range s {
		isV := strings.ContainsRune(vowels, r)
		if isV && !inVowel {
			n++
			inVowel = true
		} else if !isV {
			inVowel = false
		}
	}
	return max(1, n)
}

func normalizeText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = wsRe.ReplaceAllString(s, " ")
	return s
}

func attr(attrs []xml.Attr, key string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Name.Local, key) {
			return a.Value
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
