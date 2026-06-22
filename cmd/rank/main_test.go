package main

import "testing"

func TestParseArgsColley(t *testing.T) {
	config, err := parseArgs([]string{"colley", "--start-date", "2026-01-01", "--end-date", "2026-06-30", "--min-tournaments", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if config.system != "colley" || config.minimumTournaments != 3 {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParseArgsEloExport(t *testing.T) {
	config, err := parseArgs([]string{"elo", "--start-date", "2026-01-01", "--end-date", "2026-06-30", "--min-tournaments", "2", "--export", "out.json"})
	if err != nil {
		t.Fatal(err)
	}
	if config.system != "elo" || config.exportPath != "out.json" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParseArgsRequiresFlags(t *testing.T) {
	if _, err := parseArgs([]string{"colley"}); err == nil {
		t.Fatal("expected missing flags error")
	}
	if _, err := parseArgs([]string{"trueskill", "--start-date", "2026-01-01", "--end-date", "2026-06-30", "--min-tournaments", "3"}); err == nil {
		t.Fatal("expected unsupported system error")
	}
}
