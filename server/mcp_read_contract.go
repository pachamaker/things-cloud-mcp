package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

const maxReadBytes = 24 * 1024

type pageOptions struct {
	Limit    int
	Offset   int
	Revision string
}

type readPage[T any] struct {
	Headings      *[]areaOutput `json:"headings,omitempty"`
	Items         []T           `json:"items"`
	Returned      int           `json:"returned"`
	Total         int           `json:"total"`
	HasMore       bool          `json:"has_more"`
	NextOffset    *int          `json:"next_offset"`
	Revision      string        `json:"revision"`
	Today         string        `json:"today"`
	Timezone      string        `json:"timezone"`
	PageLimitedBy *string       `json:"page_limited_by"`
}

func integerArgument(req mcp.CallToolRequest, name string, fallback, minimum, maximum int) (int, error) {
	value, exists := req.GetArguments()[name]
	if !exists {
		return fallback, nil
	}
	kind := reflect.ValueOf(value)
	if !kind.IsValid() {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	_, number := value.(json.Number)
	if !number && (kind.Kind() < reflect.Int || kind.Kind() > reflect.Float64 || kind.Kind() == reflect.Uintptr) {
		return 0, fmt.Errorf("%s must be a number", name)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a finite integer", name)
	}
	if number {
		raw = []byte(value.(json.Number))
	}
	// Reject extreme exponents before exact rational parsing can allocate large integers.
	f, parseErr := strconv.ParseFloat(string(raw), 64)
	if parseErr != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < float64(minimum) || f > float64(maximum) {
		return 0, fmt.Errorf("%s must be an in-range integer", name)
	}
	r, ok := new(big.Rat).SetString(string(raw))
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("%s must be an in-range integer", name)
	}
	n := r.Num().Int64()
	if n < int64(minimum) || n > int64(maximum) {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return int(n), nil
}

func parseReadOptions(req mcp.CallToolRequest, fallback, maximum int) (pageOptions, error) {
	var p pageOptions
	var err error
	p.Limit, err = integerArgument(req, "limit", fallback, 1, maximum)
	if err != nil {
		return p, err
	}
	p.Offset, err = integerArgument(req, "offset", 0, 0, int(^uint(0)>>1))
	if err != nil {
		return p, err
	}
	if v, ok := req.GetArguments()["revision"]; ok {
		var good bool
		p.Revision, good = v.(string)
		if !good || len(p.Revision) > 128 {
			return p, fmt.Errorf("revision must be a string of at most 128 bytes")
		}
	}
	if p.Offset > 0 && p.Revision == "" {
		return p, fmt.Errorf("revision is required when offset is greater than zero; use the revision from the first page")
	}
	return p, nil
}
func parsePageOptions(req mcp.CallToolRequest) (pageOptions, error) {
	return parseReadOptions(req, 25, 100)
}
func parseTextOptions(req mcp.CallToolRequest) (pageOptions, error) {
	return parseReadOptions(req, 4000, 8000)
}

func readError(code, message string) *mcp.CallToolResult {
	// Bound before marshaling too, including escaping overhead and invalid UTF-8.
	runes := []rune(message)
	if len(runes) > 1024 {
		runes = runes[:1024]
	}
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": string(runes)}})
	res := mcp.NewToolResultText(string(b))
	res.IsError = true
	return res
}
func boundedReadResult(v any) *mcp.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return readError("encode_error", "failed to encode read result JSON")
	}
	if len(b) > maxReadBytes {
		return oversizedReadItem(v)
	}
	return mcp.NewToolResultText(string(b))
}

func encodeCollectionPage[T any](items []T, p pageOptions, revision string, today civil.Date, zone string, headings ...[]areaOutput) *mcp.CallToolResult {
	page := readPage[T]{Items: make([]T, 0), Total: len(items), Revision: revision, Today: today.String(), Timezone: zone}
	if len(headings) > 0 {
		page.Headings = &headings[0]
		if b, err := json.Marshal(page); err != nil {
			return readError("encode_error", "failed to encode collection")
		} else if len(b) > maxReadBytes {
			return oversizedReadItem(page)
		}
	}
	if p.Offset >= len(items) {
		return boundedReadResult(page)
	}
	stop := p.Offset + min(p.Limit, len(items)-p.Offset)
	for i := p.Offset; i < stop; i++ {
		candidate := page
		candidate.Items = append(page.Items, items[i])
		candidate.Returned = len(candidate.Items)
		next := p.Offset + candidate.Returned
		candidate.HasMore = next < len(items)
		candidate.NextOffset = nil
		candidate.PageLimitedBy = nil
		if candidate.HasMore {
			reason := "limit"
			candidate.NextOffset = &next
			candidate.PageLimitedBy = &reason
		}
		// Reserve the longer reason even before knowing why the page ends.
		b, err := json.Marshal(candidate)
		if err != nil {
			return readError("encode_error", "failed to encode collection")
		}
		if len(b) > maxReadBytes {
			if page.Returned == 0 {
				return oversizedReadItem(items[i])
			}
			reason := "bytes"
			page.PageLimitedBy = &reason
			return boundedReadResult(page)
		}
		page = candidate
	}
	return boundedReadResult(page)
}

func runePrefix(text string, count int) string {
	if utf8.RuneCountInString(text) <= count {
		return text
	}
	return string([]rune(text)[:count])
}

// Only identity and the field name are exposed, never the oversized value.
func oversizedReadItem(v any) *mcp.CallToolResult {
	raw, e := json.Marshal(v)
	if e != nil {
		return readError("encode_error", "failed to encode read result")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var uuid string
	_ = json.Unmarshal(fields["uuid"], &uuid)
	largest, field := 0, "result"
	for name, value := range fields {
		if len(value) > largest || (len(value) == largest && name < field) {
			largest, field = len(value), name
		}
	}
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "item_too_large", "message": "Entity metadata exceeds 24 KiB; inspect it in Things", "uuid": runePrefix(uuid, 128), "field": field}})
	result := mcp.NewToolResultText(string(b))
	result.IsError = true
	return result
}
