package ecfr

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (rt roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt(req)
}

func TestClientEndpoints(t *testing.T) {
	cli := NewClient("http://example.test", 2*time.Second)
	cli.hc.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/api/versioner/v1/titles.json":
			body = `{"titles":[{"number":1,"name":"Title 1","up_to_date_as_of":"2025-01-02","reserved":false}]}`
		case "/api/admin/v1/agencies.json":
			body = `{"agencies":[{"name":"Agency","slug":"agency","cfr_references":[{"title":1,"chapter":"I"}]}]}`
		case "/api/versioner/v1/full/2025-01-02/title-1.xml":
			body = `<ROOT><DIV1 TYPE="CHAPTER" N="I"><P>Hi</P></DIV1></ROOT>`
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("not found")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	ctx := context.Background()

	titles, err := cli.GetTitles(ctx)
	if err != nil {
		t.Fatalf("get titles: %v", err)
	}
	if len(titles) != 1 || titles[0].Number != 1 {
		t.Fatalf("unexpected titles: %#v", titles)
	}

	agencies, err := cli.GetAgencies(ctx)
	if err != nil {
		t.Fatalf("get agencies: %v", err)
	}
	if len(agencies) != 1 || agencies[0].Slug != "agency" {
		t.Fatalf("unexpected agencies: %#v", agencies)
	}

	xml, err := cli.GetFullTitleXML(ctx, "2025-01-02", 1)
	if err != nil {
		t.Fatalf("get xml: %v", err)
	}
	if len(xml) == 0 {
		t.Fatalf("expected xml content")
	}

	rc, err := cli.GetFullTitleXMLStream(ctx, "2025-01-02", 1)
	if err != nil {
		t.Fatalf("get xml stream: %v", err)
	}
	_ = rc.Close()
}
