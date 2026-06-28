package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const providerStreamBenchmarkChunks = 2000

func BenchmarkParseSSEHighVolume(b *testing.B) {
	sse := deepSeekBenchmarkSSE(providerStreamBenchmarkChunks)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events := make(chan Event, providerStreamBenchmarkChunks+2)
		if err := parseSSE(context.Background(), strings.NewReader(sse), 0, events); err != nil {
			b.Fatal(err)
		}
		if got, want := len(events), providerStreamBenchmarkChunks+1; got != want {
			b.Fatalf("events = %d, want %d", got, want)
		}
	}
}

func BenchmarkParseResponsesSSEHighVolume(b *testing.B) {
	sse := codexBenchmarkSSE(providerStreamBenchmarkChunks)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events := make(chan Event, providerStreamBenchmarkChunks+2)
		if err := parseResponsesSSE(context.Background(), strings.NewReader(sse), 0, events); err != nil {
			b.Fatal(err)
		}
		if got, want := len(events), providerStreamBenchmarkChunks+1; got != want {
			b.Fatalf("events = %d, want %d", got, want)
		}
	}
}

func deepSeekBenchmarkSSE(chunks int) string {
	var b strings.Builder
	for i := 0; i < chunks; i++ {
		fmt.Fprintf(&b, `data: {"choices":[{"delta":{"content":"token-%04d "}}]}`+"\n\n", i)
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func codexBenchmarkSSE(chunks int) string {
	var b strings.Builder
	for i := 0; i < chunks; i++ {
		fmt.Fprintf(&b, `data: {"type":"response.output_text.delta","delta":"token-%04d "}`+"\n\n", i)
	}
	b.WriteString(`data: {"type":"response.completed","response":{}}` + "\n\n")
	return b.String()
}
