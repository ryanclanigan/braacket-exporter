package reconcile

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}

type IdentityReconcilePlayer struct {
	CanonicalPlayerID      int
	CanonicalName          string
	BraacketLeaguePlayerID string
	Name                   string
	Tournaments            int
	Matches                int
}

type IdentityReconcileGroup struct {
	NormalizedName string
	Players        []IdentityReconcilePlayer
}

type IdentityReconcileReport struct {
	MultipleLeagueIDs      []IdentityReconcileGroup
	MixedLeagueAndNameOnly []IdentityReconcileGroup
}

type IdentityRepairResult struct {
	NormalizedName              string
	TargetCanonicalPlayerID     int
	MergedCanonicalPlayerIDs    []int
	AliasValuesCreated          []string
	TournamentPlayerRowsUpdated int
}

type identitySummaryRow struct {
	NormalizedName         string
	CanonicalPlayerID      int
	CanonicalName          string
	BraacketLeaguePlayerID sql.NullString
	Name                   string
	Tournaments            int
	Matches                int
}

type playerVariantRow struct {
	Name        string
	Tournaments int
}

type canonicalPlayerMatchProfile struct {
	TotalMatches int
	DQMatches    int
}

func NewService(db *sql.DB) *Service {
	return &Service{
		db:  db,
		now: time.Now,
	}
}

func (s *Service) BuildIdentityReport(limit int) (IdentityReconcileReport, error) {
	if limit < 1 {
		return IdentityReconcileReport{}, fmt.Errorf("limit must be a positive integer")
	}

	multipleLeagueRows, err := s.queryIdentitySummaryRows(`
WITH duplicate_names AS (
  SELECT lower(name) AS normalized_name
  FROM players
  WHERE braacket_league_player_id IS NOT NULL
  GROUP BY lower(name)
  HAVING COUNT(DISTINCT braacket_league_player_id) > 1
  ORDER BY COUNT(DISTINCT braacket_league_player_id) DESC, normalized_name
  LIMIT ?
)
SELECT
  lower(p.name) AS normalized_name,
  p.id AS canonical_player_id,
  p.canonical_name,
  p.braacket_league_player_id,
  p.name,
  COUNT(DISTINCT tp.tournament_id) AS tournaments,
  COUNT(DISTINCT m.id) AS matches
FROM duplicate_names d
JOIN players p ON lower(p.name) = d.normalized_name
LEFT JOIN tournament_players tp ON tp.canonical_player_id = p.id
LEFT JOIN matches m
  ON m.player1_tournament_player_id = tp.id
  OR m.player2_tournament_player_id = tp.id
GROUP BY lower(p.name), p.id, p.canonical_name, p.braacket_league_player_id, p.name`, limit)
	if err != nil {
		return IdentityReconcileReport{}, err
	}

	mixedRows, err := s.queryIdentitySummaryRows(`
WITH mixed_names AS (
  SELECT lower(name) AS normalized_name
  FROM players
  GROUP BY lower(name)
  HAVING SUM(CASE WHEN braacket_league_player_id IS NOT NULL THEN 1 ELSE 0 END) > 0
     AND SUM(CASE WHEN braacket_league_player_id IS NULL THEN 1 ELSE 0 END) > 0
  ORDER BY normalized_name
  LIMIT ?
)
SELECT
  lower(p.name) AS normalized_name,
  p.id AS canonical_player_id,
  p.canonical_name,
  p.braacket_league_player_id,
  p.name,
  COUNT(DISTINCT tp.tournament_id) AS tournaments,
  COUNT(DISTINCT m.id) AS matches
FROM mixed_names d
JOIN players p ON lower(p.name) = d.normalized_name
LEFT JOIN tournament_players tp ON tp.canonical_player_id = p.id
LEFT JOIN matches m
  ON m.player1_tournament_player_id = tp.id
  OR m.player2_tournament_player_id = tp.id
GROUP BY lower(p.name), p.id, p.canonical_name, p.braacket_league_player_id, p.name`, limit)
	if err != nil {
		return IdentityReconcileReport{}, err
	}

	return IdentityReconcileReport{
		MultipleLeagueIDs:      groupRows(multipleLeagueRows),
		MixedLeagueAndNameOnly: groupRows(mixedRows),
	}, nil
}

func (s *Service) FixMixedLeagueAndNameOnly(displayName string) (IdentityRepairResult, error) {
	normalizedName := canonicalizePlayerName(displayName)
	tx, err := s.db.Begin()
	if err != nil {
		return IdentityRepairResult{}, err
	}
	defer tx.Rollback()

	players, err := s.listPlayersByNormalizedName(tx, normalizedName)
	if err != nil {
		return IdentityRepairResult{}, err
	}

	leagueBacked := []identityPlayerRow{}
	nameOnly := []identityPlayerRow{}
	for _, player := range players {
		if player.BraacketLeaguePlayerID == "" {
			nameOnly = append(nameOnly, player)
			continue
		}
		leagueBacked = append(leagueBacked, player)
	}
	if len(leagueBacked) != 1 || len(nameOnly) < 1 {
		return IdentityRepairResult{}, fmt.Errorf("expected exactly one league-backed row and at least one name-only row for %s", normalizedName)
	}

	targetID := leagueBacked[0].CanonicalPlayerID
	aliasValuesCreated := []string{}
	created, err := s.insertAlias(tx, "normalized_name", normalizedName, targetID)
	if err != nil {
		return IdentityRepairResult{}, err
	}
	if created {
		aliasValuesCreated = append(aliasValuesCreated, normalizedName)
	}

	sourceIDs := make([]int, 0, len(nameOnly))
	for _, player := range nameOnly {
		profile, err := s.canonicalPlayerMatchProfile(tx, player.CanonicalPlayerID)
		if err != nil {
			return IdentityRepairResult{}, err
		}
		if profile.TotalMatches > profile.DQMatches {
			return IdentityRepairResult{}, fmt.Errorf(
				"refusing to merge %s: name-only canonical player %d has %d non-DQ match(es)",
				normalizedName,
				player.CanonicalPlayerID,
				profile.TotalMatches-profile.DQMatches,
			)
		}
		sourceIDs = append(sourceIDs, player.CanonicalPlayerID)
	}
	updatedRows, err := s.mergeCanonicalPlayers(tx, targetID, sourceIDs)
	if err != nil {
		return IdentityRepairResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdentityRepairResult{}, err
	}

	return IdentityRepairResult{
		NormalizedName:              normalizedName,
		TargetCanonicalPlayerID:     targetID,
		MergedCanonicalPlayerIDs:    sourceIDs,
		AliasValuesCreated:          aliasValuesCreated,
		TournamentPlayerRowsUpdated: updatedRows,
	}, nil
}

func (s *Service) FixMultipleLeagueIDs(displayName string, keepLeaguePlayerID string) (IdentityRepairResult, error) {
	normalizedName := canonicalizePlayerName(displayName)
	tx, err := s.db.Begin()
	if err != nil {
		return IdentityRepairResult{}, err
	}
	defer tx.Rollback()

	players, err := s.listPlayersByNormalizedName(tx, normalizedName)
	if err != nil {
		return IdentityRepairResult{}, err
	}

	var target *identityPlayerRow
	sourcePlayers := []identityPlayerRow{}
	for _, player := range players {
		if player.BraacketLeaguePlayerID == keepLeaguePlayerID {
			copy := player
			target = &copy
			continue
		}
		if player.BraacketLeaguePlayerID != "" {
			sourcePlayers = append(sourcePlayers, player)
		}
	}
	if target == nil {
		return IdentityRepairResult{}, fmt.Errorf("could not find a player row for %s with league id %s", normalizedName, keepLeaguePlayerID)
	}
	if len(sourcePlayers) < 1 {
		return IdentityRepairResult{}, fmt.Errorf("expected at least one other league-backed row for %s", normalizedName)
	}

	for _, sourcePlayer := range sourcePlayers {
		variants, err := s.listTournamentNameVariants(tx, sourcePlayer.CanonicalPlayerID)
		if err != nil {
			return IdentityRepairResult{}, err
		}
		conflicting := []string{}
		for _, variant := range variants {
			if canonicalizePlayerName(variant.Name) != normalizedName {
				conflicting = append(conflicting, fmt.Sprintf("%s (%d tournaments)", variant.Name, variant.Tournaments))
			}
		}
		if len(conflicting) > 0 {
			return IdentityRepairResult{}, fmt.Errorf(
				"refusing to merge %s: canonical player %d has conflicting tournament name variants: %s",
				normalizedName,
				sourcePlayer.CanonicalPlayerID,
				strings.Join(conflicting, ", "),
			)
		}
	}

	aliasValuesCreated := []string{}
	sourceIDs := make([]int, 0, len(sourcePlayers))
	for _, sourcePlayer := range sourcePlayers {
		sourceIDs = append(sourceIDs, sourcePlayer.CanonicalPlayerID)
		created, err := s.insertAlias(tx, "league_id", sourcePlayer.BraacketLeaguePlayerID, target.CanonicalPlayerID)
		if err != nil {
			return IdentityRepairResult{}, err
		}
		if created {
			aliasValuesCreated = append(aliasValuesCreated, sourcePlayer.BraacketLeaguePlayerID)
		}
	}

	updatedRows, err := s.mergeCanonicalPlayers(tx, target.CanonicalPlayerID, sourceIDs)
	if err != nil {
		return IdentityRepairResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdentityRepairResult{}, err
	}

	return IdentityRepairResult{
		NormalizedName:              normalizedName,
		TargetCanonicalPlayerID:     target.CanonicalPlayerID,
		MergedCanonicalPlayerIDs:    sourceIDs,
		AliasValuesCreated:          aliasValuesCreated,
		TournamentPlayerRowsUpdated: updatedRows,
	}, nil
}

type identityPlayerRow struct {
	CanonicalPlayerID      int
	CanonicalName          string
	BraacketLeaguePlayerID string
}

func (s *Service) listPlayersByNormalizedName(tx *sql.Tx, normalizedName string) ([]identityPlayerRow, error) {
	rows, err := tx.Query(`
SELECT
  id AS canonical_player_id,
  canonical_name,
  COALESCE(braacket_league_player_id, '')
FROM players
WHERE lower(name) = ?
ORDER BY id`, normalizedName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []identityPlayerRow{}
	for rows.Next() {
		var item identityPlayerRow
		if err := rows.Scan(&item.CanonicalPlayerID, &item.CanonicalName, &item.BraacketLeaguePlayerID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) insertAlias(tx *sql.Tx, aliasType string, aliasValue string, canonicalPlayerID int) (bool, error) {
	var existing int
	err := tx.QueryRow(
		`SELECT canonical_player_id
     FROM player_identity_aliases
     WHERE alias_type = ? AND alias_value = ?`,
		aliasType,
		aliasValue,
	).Scan(&existing)
	if err == nil {
		if existing != canonicalPlayerID {
			return false, fmt.Errorf("alias %s:%s already points at canonical player %d", aliasType, aliasValue, existing)
		}
		return false, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	_, err = tx.Exec(
		`INSERT INTO player_identity_aliases (alias_type, alias_value, canonical_player_id, created_at)
     VALUES (?, ?, ?, ?)`,
		aliasType,
		aliasValue,
		canonicalPlayerID,
		s.now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) mergeCanonicalPlayers(tx *sql.Tx, targetCanonicalPlayerID int, sourceCanonicalPlayerIDs []int) (int, error) {
	updated := 0
	for _, sourceID := range sourceCanonicalPlayerIDs {
		result, err := tx.Exec(
			`UPDATE tournament_players
       SET canonical_player_id = ?
       WHERE canonical_player_id = ?`,
			targetCanonicalPlayerID,
			sourceID,
		)
		if err != nil {
			return 0, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		updated += int(rows)
		if _, err := tx.Exec(`DELETE FROM players WHERE id = ?`, sourceID); err != nil {
			return 0, err
		}
	}
	return updated, nil
}

func (s *Service) listTournamentNameVariants(tx *sql.Tx, canonicalPlayerID int) ([]playerVariantRow, error) {
	rows, err := tx.Query(`
SELECT
  name,
  COUNT(DISTINCT tournament_id) AS tournaments
FROM tournament_players
WHERE canonical_player_id = ?
GROUP BY name
ORDER BY tournaments DESC, name ASC`, canonicalPlayerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []playerVariantRow{}
	for rows.Next() {
		var item playerVariantRow
		if err := rows.Scan(&item.Name, &item.Tournaments); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) canonicalPlayerMatchProfile(tx *sql.Tx, canonicalPlayerID int) (canonicalPlayerMatchProfile, error) {
	var profile canonicalPlayerMatchProfile
	err := tx.QueryRow(`
SELECT
  COUNT(DISTINCT m.id) AS total_matches,
  COUNT(DISTINCT CASE
    WHEN COALESCE(m.player1_score, 0) < 0 OR COALESCE(m.player2_score, 0) < 0 THEN m.id
    ELSE NULL
  END) AS dq_matches
FROM tournament_players tp
LEFT JOIN matches m
  ON m.player1_tournament_player_id = tp.id
  OR m.player2_tournament_player_id = tp.id
WHERE tp.canonical_player_id = ?`, canonicalPlayerID).Scan(&profile.TotalMatches, &profile.DQMatches)
	if err != nil {
		return canonicalPlayerMatchProfile{}, err
	}
	return profile, nil
}

func (s *Service) queryIdentitySummaryRows(query string, limit int) ([]identitySummaryRow, error) {
	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []identitySummaryRow{}
	for rows.Next() {
		var item identitySummaryRow
		if err := rows.Scan(
			&item.NormalizedName,
			&item.CanonicalPlayerID,
			&item.CanonicalName,
			&item.BraacketLeaguePlayerID,
			&item.Name,
			&item.Tournaments,
			&item.Matches,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func groupRows(rows []identitySummaryRow) []IdentityReconcileGroup {
	byName := map[string][]IdentityReconcilePlayer{}
	order := []string{}
	for _, row := range rows {
		if _, ok := byName[row.NormalizedName]; !ok {
			order = append(order, row.NormalizedName)
		}
		byName[row.NormalizedName] = append(byName[row.NormalizedName], IdentityReconcilePlayer{
			CanonicalPlayerID:      row.CanonicalPlayerID,
			CanonicalName:          row.CanonicalName,
			BraacketLeaguePlayerID: row.BraacketLeaguePlayerID.String,
			Name:                   row.Name,
			Tournaments:            row.Tournaments,
			Matches:                row.Matches,
		})
	}

	groups := make([]IdentityReconcileGroup, 0, len(order))
	for _, normalizedName := range order {
		players := byName[normalizedName]
		sortPlayers(players)
		groups = append(groups, IdentityReconcileGroup{
			NormalizedName: normalizedName,
			Players:        players,
		})
	}
	sortGroups(groups)
	return groups
}

func sortPlayers(players []IdentityReconcilePlayer) {
	for i := 0; i < len(players); i += 1 {
		for j := i + 1; j < len(players); j += 1 {
			left := players[i]
			right := players[j]
			swap := right.Tournaments > left.Tournaments ||
				(right.Tournaments == left.Tournaments && right.Matches > left.Matches) ||
				(right.Tournaments == left.Tournaments && right.Matches == left.Matches && right.CanonicalPlayerID < left.CanonicalPlayerID)
			if swap {
				players[i], players[j] = players[j], players[i]
			}
		}
	}
}

func sortGroups(groups []IdentityReconcileGroup) {
	for i := 0; i < len(groups); i += 1 {
		for j := i + 1; j < len(groups); j += 1 {
			left := groups[i]
			right := groups[j]
			swap := len(right.Players) > len(left.Players) ||
				(len(right.Players) == len(left.Players) && right.NormalizedName < left.NormalizedName)
			if swap {
				groups[i], groups[j] = groups[j], groups[i]
			}
		}
	}
}

func canonicalizePlayerName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}
