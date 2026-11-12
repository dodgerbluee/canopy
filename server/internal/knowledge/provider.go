// Package knowledge enriches plant records from explicitly applicable public
// sources. It never promotes generated advice to a reviewed care plan.
package knowledge

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"canopy/server/internal/media/processor"
	"golang.org/x/net/html"
)

//go:embed providers.json
var providerJSON []byte

type Source struct {
	ID, Crop, Country, Title, Publisher, Scope, URL, Hemisphere string
	Regions                                                     []string
}

func Registry() []Source {
	var s []Source
	if err := json.Unmarshal(providerJSON, &s); err != nil {
		panic(err)
	}
	return s
}

var cropAliases = map[string][]string{
	"avocado": {"avocado", "avocados", "persea americana"}, "olive": {"olive", "olives", "olea europaea"},
	"citrus": {"citrus", "lemon", "lime", "mandarin", "satsuma", "orange", "grapefruit", "kumquat"},
	"fig":    {"fig", "ficus carica"}, "strawberry": {"strawberry", "strawberries", "fragaria"},
	"blackberry": {"blackberry", "blackberries", "rubus fruticosus", "rubus argutus", "rubus allegheniensis"}, "pomegranate": {"pomegranate", "punica"},
}

func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(regexp.MustCompile(`[^\pL\pN]+`).ReplaceAllString(s, " ")), " "))
}
func cropFor(name, scientific, category string) string {
	// A supplied botanical identity takes precedence over broad display names
	// and categories. Ambiguous/unrecognized taxa stay unsupported; Rubus is
	// not synonymous with blackberry and an indoor Ficus is not a fruiting fig.
	if botanical := normalize(scientific); botanical != "" {
		for _, crop := range []string{"citrus", "fig", "strawberry", "blackberry", "pomegranate", "avocado", "olive"} {
			for _, alias := range cropAliases[crop] {
				isBotanical := strings.Contains(alias, " ") || alias == "citrus" || alias == "fragaria" || alias == "punica"
				if isBotanical && (botanical == alias || strings.HasPrefix(botanical, alias+" ")) {
					return crop
				}
			}
		}
		return ""
	}
	text := " " + normalize(name+" "+scientific+" "+category) + " "
	keys := []string{"citrus", "fig", "strawberry", "blackberry", "pomegranate", "avocado", "olive"}
	for _, crop := range keys {
		for _, alias := range cropAliases[crop] {
			if strings.Contains(text, " "+alias+" ") {
				return crop
			}
		}
	}
	return ""
}
func Applicable(sources []Source, country, region, crop string) []Source {
	out := []Source{}
	for _, s := range sources {
		if !strings.EqualFold(country, s.Country) || s.Crop != crop {
			continue
		}
		for _, r := range s.Regions {
			if normalize(region) == normalize(r) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// ExtractionVersion is part of both cache identity and persisted fact provenance.
// Increment when validation/normalization changes; old advice is re-queued.
const ExtractionVersion = 4

type Fact struct {
	ExtractionVersion int       `json:"extractionVersion"`
	TimingText        string    `json:"timingText,omitempty"`
	ID                string    `json:"id"`
	Kind              string    `json:"kind"`
	Label             string    `json:"label"`
	Summary           string    `json:"summary"`
	Months            []int     `json:"months"`
	TimingBasis       string    `json:"timingBasis"`
	Scope             string    `json:"scope"`
	SourceTitle       string    `json:"sourceTitle"`
	SourceURL         string    `json:"sourceUrl"`
	Publisher         string    `json:"publisher"`
	SourceRegion      string    `json:"sourceRegion"`
	RetrievedAt       time.Time `json:"retrievedAt"`
	ContentSHA256     string    `json:"contentSha256"`
	Subjects          []string  `json:"subjects,omitempty"`
}
type DraftFact struct {
	Kind, Label, Summary, Evidence, Timing string
	Subjects                               []string
	// Only our bounded repair fallback can set this. The model cannot opt out of
	// completeness checks by returning an unverified flag in its JSON response.
	timingUnverified bool
}

var factKinds = map[string]bool{"GROWTH": true, "BLOOM": true, "HARVEST": true, "FERTILIZE": true, "WATER_CHECK": true, "PRUNE": true, "DORMANCY": true, "PROTECT": true}

func ValidateFacts(s Source, text string, drafts []DraftFact, now time.Time) ([]Fact, error) {
	if len(drafts) == 0 || len(drafts) > 16 {
		return nil, errors.New("source extraction returned no usable bounded guidance")
	}
	normalized := strings.Join(strings.Fields(text), " ")
	sum := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(sum[:])
	out := []Fact{}
	words := 0
	for i, d := range drafts {
		evidence := strings.Join(strings.Fields(d.Evidence), " ")
		timing := strings.Join(strings.Fields(d.Timing), " ")
		if !factKinds[d.Kind] || len(d.Label) < 2 || len(d.Label) > 100 || len(d.Summary) > 1000 || len(d.Summary) < 10 || len(evidence) < 10 || len(evidence) > 500 || !strings.Contains(normalized, evidence) {
			return nil, errors.New("extraction evidence did not match the source")
		}
		if timing != "" && (!strings.Contains(evidence, timing) || len(timing) > 500) {
			return nil, errors.New("seasonal timing was not found in the source")
		}
		if d.timingUnverified && timing != "" {
			return nil, errors.New("unverified timing must remain off the calendar")
		}
		if err := validateTiming(text, evidence, timing, d.Summary, d.timingUnverified); err != nil {
			return nil, err
		}
		words += len(strings.Fields(d.Summary))
		if words > 200 {
			return nil, errors.New("source summary exceeds the 200-word budget")
		}
		months, basis := parseTimingForHemisphere(timing, s.Hemisphere)
		idHash := sha256.Sum256([]byte(s.ID + hash + fmt.Sprint(ExtractionVersion, i) + d.Kind))
		out = append(out, Fact{ExtractionVersion: ExtractionVersion, TimingText: timing, ID: hex.EncodeToString(idHash[:12]), Kind: d.Kind, Label: d.Label, Summary: d.Summary, Months: months, TimingBasis: basis, Scope: s.Scope, SourceTitle: s.Title, SourceURL: s.URL, Publisher: s.Publisher, SourceRegion: strings.Join(s.Regions, ", "), RetrievedAt: now, ContentSHA256: hash, Subjects: d.Subjects})
	}
	return out, nil
}
func FilterFacts(facts []Fact, identity string) []Fact {
	result := []Fact{}
	identity = " " + normalize(identity) + " "
	for _, f := range facts {
		match := len(f.Subjects) == 0
		for _, subject := range f.Subjects {
			subject = normalize(subject)
			if subject != "" && strings.Contains(identity, " "+subject+" ") {
				match = true
			}
		}
		if match {
			result = append(result, f)
		}
	}
	return result
}

type Fetcher struct {
	HTTP    *http.Client
	Sandbox processor.Sandbox
}

func NewFetcher(sb processor.Sandbox) *Fetcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsUnspecified() {
				return nil, errors.New("source resolved to a non-public address")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("source has no address")
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	allowed := map[string]bool{}
	for _, s := range Registry() {
		u, _ := url.Parse(s.URL)
		allowed[u.Hostname()] = true
	}
	client := &http.Client{Timeout: 25 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 || req.URL.Scheme != "https" || !allowed[req.URL.Hostname()] {
			return errors.New("source redirect is outside the provider registry")
		}
		return nil
	}}
	return &Fetcher{HTTP: client, Sandbox: sb}
}
func (f *Fetcher) get(ctx context.Context, address string, max int64) ([]byte, string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("User-Agent", "CanopyKnowledge/1.0 (personal garden knowledge; bounded factual summaries)")
	res, err := f.HTTP.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if len(body) > int(max) {
		return nil, "", res.StatusCode, errors.New("source exceeds download limit")
	}
	return body, res.Header.Get("Content-Type"), res.StatusCode, err
}

// RobotsAllowed implements group precedence and longest matching allow/disallow
// rules; unsupported or unavailable access is never interpreted as permission.
func RobotsAllowed(body, path string) bool {
	type group struct {
		agents []string
		rules  [][2]string
	}
	groups := []group{}
	g := group{}
	rulesStarted := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "user-agent" {
			if rulesStarted {
				groups = append(groups, g)
				g = group{}
				rulesStarted = false
			}
			g.agents = append(g.agents, strings.ToLower(value))
		} else if (key == "allow" || key == "disallow") && len(g.agents) > 0 {
			g.rules = append(g.rules, [2]string{key, value})
			rulesStarted = true
		}
	}
	groups = append(groups, g)
	specific := false
	for _, g := range groups {
		for _, a := range g.agents {
			if a != "*" && strings.Contains("canopyknowledge", a) {
				specific = true
			}
		}
	}
	best := -1
	allow := true
	for _, g := range groups {
		applies := false
		for _, a := range g.agents {
			if specific {
				applies = applies || (a != "*" && strings.Contains("canopyknowledge", a))
			} else {
				applies = applies || a == "*"
			}
		}
		if !applies {
			continue
		}
		for _, r := range g.rules {
			if r[1] == "" {
				continue
			}
			pattern := regexp.QuoteMeta(r[1])
			pattern = strings.ReplaceAll(pattern, `\*`, ".*")
			pattern = strings.TrimSuffix(pattern, `\$`)
			if strings.HasSuffix(r[1], "$") {
				pattern += "$"
			}
			matched, _ := regexp.MatchString("^"+pattern, path)
			length := len(strings.ReplaceAll(r[1], "*", ""))
			if matched && (length > best || (length == best && r[0] == "allow")) {
				best = length
				allow = r[0] == "allow"
			}
		}
	}
	return allow
}
func (f *Fetcher) Read(ctx context.Context, s Source) (string, error) {
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "", errors.New("invalid registered source")
	}
	body, _, status, err := f.get(ctx, u.Scheme+"://"+u.Host+"/robots.txt", 128<<10)
	if err != nil {
		return "", errors.New("source access policy could not be checked")
	}
	if status != 404 && (status != 200 || !RobotsAllowed(string(body), u.EscapedPath())) {
		return "", errors.New("source access policy does not permit fetching this guide")
	}
	body, ctype, status, err := f.get(ctx, s.URL, 12<<20)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("source returned HTTP %d", status)
	}
	var text string
	if strings.HasPrefix(string(body), "%PDF-") || strings.Contains(ctype, "application/pdf") {
		if f.Sandbox == nil {
			return "", errors.New("PDF decoder is unavailable")
		}
		dir, err := os.MkdirTemp("", "canopy-knowledge-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		if err = os.WriteFile(filepath.Join(dir, "source.pdf"), body, 0600); err != nil {
			return "", err
		}
		out, err := f.Sandbox.Run(ctx, dir, "pdftotext", "-f", "1", "-l", "20", "-layout", "source.pdf", "-")
		if err != nil {
			return "", errors.New("source PDF could not be read")
		}
		// PDF line wraps are whitespace; blank lines retain paragraph boundaries.
		paragraphs := strings.Split(strings.ReplaceAll(string(out.Stdout), "\r\n", "\n"), "\n\n")
		for i := range paragraphs {
			paragraphs[i] = strings.Join(strings.Fields(paragraphs[i]), " ")
		}
		text = strings.Join(paragraphs, "\n")
	} else {
		root, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			return "", err
		}
		var b strings.Builder
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "nav" || n.Data == "header" || n.Data == "footer") {
				return
			}
			block := n.Type == html.ElementNode && sourceBlock(n.Data)
			if block {
				b.WriteByte('\n')
			}
			if n.Type == html.TextNode {
				b.WriteString(strings.Join(strings.Fields(n.Data), " "))
				b.WriteByte(' ')
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if block {
				b.WriteByte('\n')
			}
		}
		walk(root)
		text = b.String()
	}
	text = normalizeSourceText(text)
	if len(text) < 300 {
		return "", errors.New("source did not contain a readable guide")
	}
	if len(text) > 70000 {
		text = text[:70000]
	}
	return text, nil
}
