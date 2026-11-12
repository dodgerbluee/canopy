package aiconnection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}
type wireRequest struct {
	Model               string          `json:"model"`
	Messages            []wireMessage   `json:"messages"`
	MaxTokens           int             `json:"max_tokens"`
	MaxCompletionTokens int             `json:"max_completion_tokens"`
	ResponseFormat      json.RawMessage `json:"response_format"`
}
type gateway struct {
	config Config
	base   http.RoundTripper
}

func restrictedTransport(c Config) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("AI endpoint address is invalid")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("AI endpoint could not be resolved")
		}
		for _, ip := range ips {
			if ip.IP.IsUnspecified() || ip.IP.IsMulticast() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
				return nil, errors.New("AI endpoint address is not allowed")
			}
			if c.DeploymentLocal && !ip.IP.IsPrivate() && !ip.IP.IsLoopback() {
				return nil, errors.New("Deployment-local AI endpoints must resolve to a private or loopback address")
			}
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return t
}
func (m *Manager) Client(c Config) *http.Client {
	base := m.Transport
	if base == nil {
		base = restrictedTransport(c)
	}
	return &http.Client{Transport: &gateway{config: c, base: base}, Timeout: 150 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func apiPath(endpoint, standard, suffix string) string {
	u, _ := url.Parse(endpoint)
	if u.Path == "" || u.Path == "/" {
		return strings.TrimRight(endpoint, "/") + standard
	}
	return strings.TrimRight(endpoint, "/") + suffix
}
func parts(content json.RawMessage) ([]wirePart, error) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return []wirePart{{Type: "text", Text: text}}, nil
	}
	var list []wirePart
	if json.Unmarshal(content, &list) != nil {
		return nil, errors.New("AI message content is invalid")
	}
	return list, nil
}
func embeddedImage(part wirePart) (string, string, error) {
	if part.ImageURL == nil {
		return "", "", errors.New("AI image is missing")
	}
	prefix, data, ok := strings.Cut(part.ImageURL.URL, ",")
	if !ok || !strings.HasPrefix(prefix, "data:image/") || !strings.HasSuffix(prefix, ";base64") {
		return "", "", errors.New("AI images must use embedded image bytes")
	}
	mime := strings.TrimSuffix(strings.TrimPrefix(prefix, "data:"), ";base64")
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		return "", "", errors.New("AI image type is unsupported")
	}
	return mime, data, nil
}
func (g *gateway) RoundTrip(original *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(io.LimitReader(original.Body, (32<<20)+1))
	original.Body.Close()
	if err != nil || len(raw) > 32<<20 {
		return nil, errors.New("AI request exceeds its byte limit")
	}
	var input wireRequest
	if json.Unmarshal(raw, &input) != nil {
		return nil, errors.New("AI request is invalid")
	}
	c := g.config
	target := ""
	var payload any
	if c.Protocol == OpenAI {
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil {
			return nil, errors.New("AI request is invalid")
		}
		body["model"] = c.Model
		if c.TokenLimitField == "MAX_COMPLETION_TOKENS" {
			if limit, ok := body["max_tokens"]; ok {
				body["max_completion_tokens"] = limit
				delete(body, "max_tokens")
			}
			delete(body, "temperature")
		}
		if c.ReasoningEffort != "" {
			body["reasoning_effort"] = c.ReasoningEffort
		} else {
			delete(body, "reasoning_effort")
		}
		target = apiPath(c.Endpoint, "/v1/chat/completions", "/chat/completions")
		payload = body
	} else {
		messages := []map[string]any{}
		system := []string{}
		for _, message := range input.Messages {
			content, err := parts(message.Content)
			if err != nil {
				return nil, err
			}
			blocks := []map[string]any{}
			for _, part := range content {
				if part.Type == "text" {
					if message.Role == "system" {
						system = append(system, part.Text)
						continue
					}
					if c.Protocol == Anthropic {
						blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
					} else {
						blocks = append(blocks, map[string]any{"text": part.Text})
					}
				} else if part.Type == "image_url" {
					mime, data, err := embeddedImage(part)
					if err != nil {
						return nil, err
					}
					if c.Protocol == Anthropic {
						blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mime, "data": data}})
					} else {
						blocks = append(blocks, map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}})
					}
				} else {
					return nil, errors.New("AI message content type is unsupported")
				}
			}
			if message.Role == "system" {
				continue
			}
			role := message.Role
			if role != "user" && role != "assistant" {
				return nil, errors.New("AI message role is unsupported")
			}
			if c.Protocol == Anthropic {
				messages = append(messages, map[string]any{"role": role, "content": blocks})
			} else {
				if role == "assistant" {
					role = "model"
				}
				messages = append(messages, map[string]any{"role": role, "parts": blocks})
			}
		}
		limit := input.MaxTokens
		if limit == 0 {
			limit = input.MaxCompletionTokens
		}
		if limit <= 0 {
			limit = 1024
		}
		if c.Protocol == Anthropic {
			target = apiPath(c.Endpoint, "/v1/messages", "/messages")
			payload = map[string]any{"model": c.Model, "max_tokens": limit, "system": strings.Join(system, "\n"), "messages": messages}
		} else if c.Protocol == Gemini {
			model := url.PathEscape(strings.TrimPrefix(c.Model, "models/"))
			target = apiPath(c.Endpoint, "/v1beta/models/"+model+":generateContent", "/models/"+model+":generateContent")
			options := map[string]any{"maxOutputTokens": limit}
			if len(input.ResponseFormat) > 0 {
				options["responseMimeType"] = "application/json"
			}
			payload = map[string]any{"systemInstruction": map[string]any{"parts": []map[string]string{{"text": strings.Join(system, "\n")}}}, "contents": messages, "generationConfig": options}
		} else {
			return nil, errors.New("AI provider protocol is unsupported")
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("AI request could not be encoded")
	}
	req, err := http.NewRequestWithContext(original.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("AI endpoint is invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	switch c.Protocol {
	case OpenAI:
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
	case Anthropic:
		req.Header.Set("anthropic-version", "2026-10-21")
		if c.APIKey != "" {
			req.Header.Set("x-api-key", c.APIKey)
		}
	case Gemini:
		if c.APIKey != "" {
			req.Header.Set("x-goog-api-key", c.APIKey)
		}
	}
	response, err := g.base.RoundTrip(req)
	if err != nil {
		return nil, errors.New("AI provider could not be reached")
	}
	defer response.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(reply) > 1<<20 {
		return nil, errors.New("AI response exceeds its byte limit")
	}
	makeResponse := func(body []byte) *http.Response {
		return &http.Response{StatusCode: response.StatusCode, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: original}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return makeResponse([]byte(`{"error":"AI provider rejected the request"}`)), nil
	}
	if c.Protocol == OpenAI {
		return makeResponse(reply), nil
	}
	text := ""
	model := c.Model
	finish := "stop"
	if c.Protocol == Anthropic {
		var result struct {
			Model      string                        `json:"model"`
			Content    []struct{ Type, Text string } `json:"content"`
			StopReason string                        `json:"stop_reason"`
		}
		if json.Unmarshal(reply, &result) != nil {
			return nil, errors.New("AI provider returned an unreadable reply")
		}
		for _, block := range result.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
		if result.Model != "" {
			model = result.Model
		}
		if result.StopReason == "max_tokens" {
			finish = "length"
		}
	} else {
		var result struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			ModelVersion string `json:"modelVersion"`
		}
		if json.Unmarshal(reply, &result) != nil {
			return nil, errors.New("AI provider returned an unreadable reply")
		}
		if len(result.Candidates) > 0 {
			for _, part := range result.Candidates[0].Content.Parts {
				if !part.Thought {
					text += part.Text
				}
			}
			if result.Candidates[0].FinishReason == "MAX_TOKENS" {
				finish = "length"
			}
		}
		if result.ModelVersion != "" {
			model = result.ModelVersion
		}
	}
	envelope, _ := json.Marshal(map[string]any{"model": model, "choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": finish}}})
	return makeResponse(envelope), nil
}
func (m *Manager) Probe(ctx context.Context, c Config, vision bool) (bool, string) {
	content := []map[string]any{{"type": "text", "text": "Connection test only. Return exactly this JSON object: {\"status\":\"ok\"}."}}
	if vision {
		if !c.SupportsVision {
			return false, "This connection is not marked as vision-capable."
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": testImage}})
	}
	body, _ := json.Marshal(map[string]any{"model": c.Model, "max_tokens": 512, "response_format": map[string]string{"type": "json_object"}, "messages": []map[string]any{{"role": "system", "content": "You are testing an AI connection. Reply with JSON only. No user garden records are included."}, {"role": "user", "content": content}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Origin()+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return false, "The connection endpoint is invalid."
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := m.Client(c).Do(req)
	if err != nil {
		return false, "Could not reach the provider. Check its address, network access and local-service setting."
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		switch response.StatusCode {
		case 401, 403:
			return false, "The provider rejected authentication. Check the API key and permissions."
		case 429:
			return false, "The provider is rate-limited. Try again later."
		default:
			return false, fmt.Sprintf("The provider returned HTTP %d. Check the protocol, model and advanced options.", response.StatusCode)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return false, "The provider response exceeded its limit."
	}
	var envelope struct {
		Choices []struct{ Message struct{ Content string } }
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Choices) != 1 {
		return false, "The provider returned an unreadable completion."
	}
	var result struct {
		Status string `json:"status"`
	}
	text := strings.TrimSpace(envelope.Choices[0].Message.Content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimSuffix(text, "```")
	if json.Unmarshal([]byte(text), &result) != nil || result.Status != "ok" {
		return false, "The model did not return the expected JSON test response. Check the model and reasoning options."
	}
	if vision {
		return true, "Connected. The provider accepted a synthetic image request."
	}
	return true, "Connected. The model returned a valid test response."
}

const testImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAIAAABLbSncAAAAEUlEQVR4nGNQ6nDBihiGlgQAN6E7gT2iD50AAAAASUVORK5CYII="
