package main

import (
	"encoding/json"
	"fmt"
	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pachamaker/things-cloud-mcp/domain/civil"
	"strings"
	"testing"
)

func BenchmarkReadPayload(b *testing.B) {
	day, _ := civil.Parse("2026-11-14")
	for _, n := range []int{247, 10000} {
		legacy := make([]taskOutput, n)
		compact := make([]taskSummaryOutput, n)
		for i := range legacy {
			task := &things.Task{UUID: fmt.Sprintf("synthetic-%05d", i), Title: "Review estimate", Note: strings.Repeat("Synthetic note. ", 100), Status: things.TaskStatusPending}
			legacy[i] = formatTask(task)
			compact[i] = summarizeTask(task)
		}
		b.Run(fmt.Sprintf("%d/legacy-full", n), func(b *testing.B) {
			for b.Loop() {
				v, _ := json.MarshalIndent(legacy, "", "  ")
				b.ReportMetric(float64(len(v)), "payload-bytes")
			}
		})
		b.Run(fmt.Sprintf("%d/default-page", n), func(b *testing.B) {
			for b.Loop() {
				r := encodeCollectionPage(compact, pageOptions{Limit: 25}, strings.Repeat("a", 64), day, "UTC")
				b.ReportMetric(float64(len(r.Content[0].(mcp.TextContent).Text)), "payload-bytes")
			}
		})
		b.Run(fmt.Sprintf("%d/all-summary-pages", n), func(b *testing.B) {
			for b.Loop() {
				total := 0
				for offset := 0; offset < n; offset += 25 {
					r := encodeCollectionPage(compact, pageOptions{Limit: 25, Offset: offset}, strings.Repeat("a", 64), day, "UTC")
					total += len(r.Content[0].(mcp.TextContent).Text)
				}
				b.ReportMetric(float64(total), "payload-bytes")
			}
		})
	}
}
