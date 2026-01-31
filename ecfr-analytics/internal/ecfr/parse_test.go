package ecfr

import "testing"

func TestParseTitleChapters(t *testing.T) {
	xml := []byte(`
<ROOT>
  <DIV1 TYPE="CHAPTER" N="I"><P>Alpha beta.</P></DIV1>
  <DIV1 TYPE="CHAPTER" N="II"><P>Gamma delta.</P></DIV1>
</ROOT>`)
	chapters, err := ParseTitleChapters(xml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if chapters["I"] == "" || chapters["II"] == "" {
		t.Fatalf("expected chapter content, got: %#v", chapters)
	}
}

func TestWordCount(t *testing.T) {
	n := WordCount("Hello, world 123.")
	if n != 3 {
		t.Fatalf("unexpected word count: %d", n)
	}
}

func TestChecksumHex(t *testing.T) {
	sum := ChecksumHex("abc")
	if len(sum) != 64 {
		t.Fatalf("unexpected checksum length: %d", len(sum))
	}
}

func TestFleschReadingEase(t *testing.T) {
	score := FleschReadingEase("This is a sentence. This is another one.")
	if score == 0 {
		t.Fatalf("unexpected score: %v", score)
	}
}
