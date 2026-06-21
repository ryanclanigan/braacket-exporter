package main

import "testing"

func TestParseArgsIdentities(t *testing.T) {
	config, err := parseArgs([]string{"identities", "--limit", "20"})
	if err != nil {
		t.Fatal(err)
	}
	if config.command != "identities" || config.limit != 20 {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParseArgsFixMixedNameOnly(t *testing.T) {
	config, err := parseArgs([]string{"fix-mixed-name-only", "--name", "Dial M"})
	if err != nil {
		t.Fatal(err)
	}
	if config.command != "fix-mixed-name-only" || config.displayName != "Dial M" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParseArgsFixMultipleLeagueIDs(t *testing.T) {
	config, err := parseArgs([]string{"fix-multiple-league-ids", "--name", "Soda cup", "--keep-league-id", "l1"})
	if err != nil {
		t.Fatal(err)
	}
	if config.command != "fix-multiple-league-ids" || config.displayName != "Soda cup" || config.keepLeagueID != "l1" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestParseArgsRequiresFlags(t *testing.T) {
	if _, err := parseArgs([]string{"fix-mixed-name-only"}); err == nil {
		t.Fatal("expected missing --name error")
	}
	if _, err := parseArgs([]string{"fix-multiple-league-ids", "--name", "Soda cup"}); err == nil {
		t.Fatal("expected missing --keep-league-id error")
	}
	if _, err := parseArgs([]string{"identities", "--limit", "0"}); err == nil {
		t.Fatal("expected invalid limit error")
	}
}
