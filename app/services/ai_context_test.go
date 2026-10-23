package services

import (
	"fmt"
	"strings"
	"testing"
)

func TestFormatNewsDigestKeepsFiveNewest(t *testing.T) {
	items := make([]InsightItem, 0, 8)
	for i := 1; i <= 8; i++ {
		items = append(items, InsightItem{
			Title:    fmt.Sprintf("Berita %d", i),
			Category: "Moneter",
		})
	}

	digest := formatNewsDigest(items)

	for i := 1; i <= aiNewsDigestLimit; i++ {
		want := fmt.Sprintf("%d. [Moneter] Berita %d", i, i)
		if !strings.Contains(digest, want) {
			t.Fatalf("digest tidak memuat berita %d: %q", i, digest)
		}
	}

	if strings.Contains(digest, "Berita 6") {
		t.Fatalf("digest mengirim berita melebihi batas %d: %q", aiNewsDigestLimit, digest)
	}
}

func TestFormatChatPlatformsIncludesDescription(t *testing.T) {
	got := formatChatPlatforms([]ChatPlatform{{
		Name:        "Indodax",
		Category:    "crypto",
		Type:        "Lokal",
		Description: "Bursa kripto lokal tertua di Indonesia.",
	}})

	want := "- Indodax (Kripto, Lokal): Bursa kripto lokal tertua di Indonesia."
	if got != want {
		t.Fatalf("baris platform = %q, ingin %q", got, want)
	}
}

func TestCleanChatPlatformsCapsDescription(t *testing.T) {
	got := cleanChatPlatforms([]ChatPlatform{{
		Name:        "Indodax",
		Description: strings.Repeat("a", chatContextPlatformDescLimit+50),
	}})

	if len(got) != 1 {
		t.Fatalf("jumlah platform = %d, ingin 1", len(got))
	}
	if runes := []rune(got[0].Description); len(runes) != chatContextPlatformDescLimit {
		t.Fatalf("panjang deskripsi = %d rune, ingin %d", len(runes), chatContextPlatformDescLimit)
	}
}
