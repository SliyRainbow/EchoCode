package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultBusyTimeout  = 5 * time.Second
	defaultQueryTimeout = 10 * time.Second
)

type DB struct {
	path string
	db   *sql.DB
}

type UsageRecord struct {
	ID                       int64     `json:"id"`
	CreatedAt                time.Time `json:"createdAt"`
	SessionID                string    `json:"sessionId"`
	ProviderID               string    `json:"providerId"`
	ProviderName             string    `json:"providerName"`
	Protocol                 string    `json:"protocol"`
	ModelID                  string    `json:"modelId"`
	Reasoning                string    `json:"reasoning"`
	PromptCacheHitTokens     int64     `json:"promptCacheHitTokens"`
	PromptCacheMissTokens    int64     `json:"promptCacheMissTokens"`
	InputTokens              int64     `json:"inputTokens"`
	OutputTokens             int64     `json:"outputTokens"`
	EffectiveCost            float64   `json:"effectiveCost"`
	PricingSource            string    `json:"pricingSource,omitempty"`
	PricingVersion           string    `json:"pricingVersion,omitempty"`
	InputPricePerMillion     float64   `json:"inputPricePerMillion,omitempty"`
	OutputPricePerMillion    float64   `json:"outputPricePerMillion,omitempty"`
	CacheHitPricePerMillion  float64   `json:"cacheHitPricePerMillion,omitempty"`
	CacheMissPricePerMillion float64   `json:"cacheMissPricePerMillion,omitempty"`
}

type UsageTotals struct {
	Samples               int64   `json:"samples"`
	PromptCacheHitTokens  int64   `json:"promptCacheHitTokens"`
	PromptCacheMissTokens int64   `json:"promptCacheMissTokens"`
	InputTokens           int64   `json:"inputTokens"`
	OutputTokens          int64   `json:"outputTokens"`
	EffectiveCost         float64 `json:"effectiveCost"`
	LastRecordedAt        string  `json:"lastRecordedAt,omitempty"`
}

// BillingReconciliation stores a user- or API-supplied official billing
// snapshot for one provider and one closed reporting period. The local
// estimate is saved beside it so later price edits cannot rewrite history.
type BillingReconciliation struct {
	ID               int64     `json:"id"`
	ProviderID       string    `json:"providerId"`
	ProviderName     string    `json:"providerName"`
	PeriodStart      time.Time `json:"periodStart"`
	PeriodEnd        time.Time `json:"periodEnd"`
	OfficialCost     float64   `json:"officialCost"`
	EstimatedCost    float64   `json:"estimatedCost"`
	Difference       float64   `json:"difference"`
	Source           string    `json:"source"`
	Note             string    `json:"note,omitempty"`
	UsageDetailsJSON string    `json:"usageDetailsJson,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func Open(path string) (*DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("storage path cannot be empty")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve storage path: %w", err)
		}
		path = absolute
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create storage directory: %w", err)
		}
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	// A single connection keeps in-memory databases stable and serializes writes.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	db := &DB{path: path, db: sqlDB}
	if err := db.configure(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := db.applyMigrations(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) configure() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		fmt.Sprintf("PRAGMA busy_timeout = %d", defaultBusyTimeout.Milliseconds()),
	}
	if db.path != ":memory:" && !strings.Contains(db.path, "mode=memory") {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL")
	}
	for _, pragma := range pragmas {
		if _, err := db.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure storage: %w", err)
		}
	}
	return nil
}

func (db *DB) applyMigrations() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	if _, err := db.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	for _, migration := range InitialMigrations() {
		var applied int
		err := db.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", migration.Version).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read migration %d: %w", migration.Version, err)
		}

		tx, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.Version, err)
		}
		for _, statement := range migration.Statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d (%s): %w", migration.Version, migration.Name, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, ?, ?)",
			migration.Version, migration.Name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", migration.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.Version, err)
		}
	}
	return nil
}

func (db *DB) Path() string {
	if db == nil {
		return ""
	}
	return db.path
}

func (db *DB) Close() error {
	if db == nil || db.db == nil {
		return nil
	}
	return db.db.Close()
}

func (db *DB) AppendUsage(record UsageRecord) error {
	if db == nil || db.db == nil {
		return errors.New("storage is closed")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO usage_metrics (
			created_at, session_id, provider_id, provider_name, protocol, model_id, reasoning,
			prompt_cache_hit_tokens, prompt_cache_miss_tokens, input_tokens, output_tokens, effective_cost,
			pricing_source, pricing_version, input_price_per_million, output_price_per_million,
			cache_hit_price_per_million, cache_miss_price_per_million
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(record.SessionID),
		strings.TrimSpace(record.ProviderID),
		strings.TrimSpace(record.ProviderName),
		strings.TrimSpace(record.Protocol),
		strings.TrimSpace(record.ModelID),
		strings.TrimSpace(record.Reasoning),
		nonNegative(record.PromptCacheHitTokens),
		nonNegative(record.PromptCacheMissTokens),
		nonNegative(record.InputTokens),
		nonNegative(record.OutputTokens),
		nonNegativeFloat(record.EffectiveCost),
		strings.TrimSpace(record.PricingSource),
		strings.TrimSpace(record.PricingVersion),
		nonNegativeFloat(record.InputPricePerMillion),
		nonNegativeFloat(record.OutputPricePerMillion),
		nonNegativeFloat(record.CacheHitPricePerMillion),
		nonNegativeFloat(record.CacheMissPricePerMillion),
	)
	if err != nil {
		return fmt.Errorf("append usage: %w", err)
	}
	return nil
}

func (db *DB) RecentUsage(sessionID string, limit int) ([]UsageRecord, error) {
	if db == nil || db.db == nil {
		return nil, errors.New("storage is closed")
	}
	if limit <= 0 {
		limit = 24
	}
	if limit > 500 {
		limit = 500
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()

	query := `
		SELECT id, created_at, session_id, provider_id, provider_name, protocol, model_id, reasoning,
			prompt_cache_hit_tokens, prompt_cache_miss_tokens, input_tokens, output_tokens, effective_cost,
			pricing_source, pricing_version, input_price_per_million, output_price_per_million,
			cache_hit_price_per_million, cache_miss_price_per_million
		FROM usage_metrics`
	args := []any{}
	if strings.TrimSpace(sessionID) != "" {
		query += " WHERE session_id = ?"
		args = append(args, strings.TrimSpace(sessionID))
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query recent usage: %w", err)
	}
	defer rows.Close()
	records := make([]UsageRecord, 0, limit)
	for rows.Next() {
		record, scanErr := scanUsageRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent usage: %w", err)
	}
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
	return records, nil
}

func (db *DB) Totals(sessionID string) (UsageTotals, error) {
	if db == nil || db.db == nil {
		return UsageTotals{}, errors.New("storage is closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	query := `
		SELECT COUNT(*),
			COALESCE(SUM(prompt_cache_hit_tokens), 0),
			COALESCE(SUM(prompt_cache_miss_tokens), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(effective_cost), 0),
			COALESCE(MAX(created_at), '')
		FROM usage_metrics`
	args := []any{}
	if strings.TrimSpace(sessionID) != "" {
		query += " WHERE session_id = ?"
		args = append(args, strings.TrimSpace(sessionID))
	}
	var totals UsageTotals
	if err := db.db.QueryRowContext(ctx, query, args...).Scan(
		&totals.Samples,
		&totals.PromptCacheHitTokens,
		&totals.PromptCacheMissTokens,
		&totals.InputTokens,
		&totals.OutputTokens,
		&totals.EffectiveCost,
		&totals.LastRecordedAt,
	); err != nil {
		return UsageTotals{}, fmt.Errorf("query usage totals: %w", err)
	}
	return totals, nil
}

// UsageCostForProviderPeriod returns the locally recorded request cost for a
// half-open reporting period [start, end). Keeping the range explicit avoids
// comparing a partial local day against a complete official billing day.
func (db *DB) UsageCostForProviderPeriod(providerID string, start, end time.Time) (float64, error) {
	if db == nil || db.db == nil {
		return 0, errors.New("storage is closed")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0, errors.New("provider id cannot be empty")
	}
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return 0, errors.New("billing period must have a positive duration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	var cost float64
	if err := db.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(effective_cost), 0)
		FROM usage_metrics
		WHERE provider_id = ? AND created_at >= ? AND created_at < ?`,
		providerID,
		start.UTC().Format(time.RFC3339Nano),
		end.UTC().Format(time.RFC3339Nano),
	).Scan(&cost); err != nil {
		return 0, fmt.Errorf("query provider usage cost: %w", err)
	}
	return nonNegativeFloat(cost), nil
}

// UpsertBillingReconciliation persists an official bill snapshot. Re-entering
// a closed period replaces the prior snapshot, which is useful when a provider
// finalizes delayed usage data.
func (db *DB) UpsertBillingReconciliation(record BillingReconciliation) (BillingReconciliation, error) {
	if db == nil || db.db == nil {
		return BillingReconciliation{}, errors.New("storage is closed")
	}
	record.ProviderID = strings.TrimSpace(record.ProviderID)
	if record.ProviderID == "" {
		return BillingReconciliation{}, errors.New("provider id cannot be empty")
	}
	if record.PeriodStart.IsZero() || record.PeriodEnd.IsZero() || !record.PeriodEnd.After(record.PeriodStart) {
		return BillingReconciliation{}, errors.New("billing period must have a positive duration")
	}
	record.ProviderName = strings.TrimSpace(record.ProviderName)
	record.Source = strings.TrimSpace(record.Source)
	if record.Source == "" {
		record.Source = "official-manual"
	}
	record.Note = strings.TrimSpace(record.Note)
	record.UsageDetailsJSON = strings.TrimSpace(record.UsageDetailsJSON)
	record.OfficialCost = nonNegativeFloat(record.OfficialCost)
	record.EstimatedCost = nonNegativeFloat(record.EstimatedCost)
	record.Difference = record.OfficialCost - record.EstimatedCost
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now

	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO usage_billing_reconciliations (
			provider_id, provider_name, period_start, period_end, official_cost, estimated_cost,
			difference, source, note, usage_details_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, period_start, period_end) DO UPDATE SET
			provider_name = excluded.provider_name,
			official_cost = excluded.official_cost,
			estimated_cost = excluded.estimated_cost,
			difference = excluded.difference,
			source = excluded.source,
			note = excluded.note,
			usage_details_json = excluded.usage_details_json,
			updated_at = excluded.updated_at`,
		record.ProviderID,
		record.ProviderName,
		record.PeriodStart.UTC().Format(time.RFC3339Nano),
		record.PeriodEnd.UTC().Format(time.RFC3339Nano),
		record.OfficialCost,
		record.EstimatedCost,
		record.Difference,
		record.Source,
		record.Note,
		record.UsageDetailsJSON,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return BillingReconciliation{}, fmt.Errorf("upsert billing reconciliation: %w", err)
	}
	return db.BillingReconciliation(record.ProviderID, record.PeriodStart, record.PeriodEnd)
}

func (db *DB) BillingReconciliation(providerID string, start, end time.Time) (BillingReconciliation, error) {
	if db == nil || db.db == nil {
		return BillingReconciliation{}, errors.New("storage is closed")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return BillingReconciliation{}, errors.New("provider id cannot be empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	row := db.db.QueryRowContext(ctx, `
		SELECT id, provider_id, provider_name, period_start, period_end, official_cost, estimated_cost,
			difference, source, note, usage_details_json, created_at, updated_at
		FROM usage_billing_reconciliations
		WHERE provider_id = ? AND period_start = ? AND period_end = ?`,
		providerID,
		start.UTC().Format(time.RFC3339Nano),
		end.UTC().Format(time.RFC3339Nano),
	)
	record, err := scanBillingReconciliation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BillingReconciliation{}, sql.ErrNoRows
	}
	if err != nil {
		return BillingReconciliation{}, fmt.Errorf("query billing reconciliation: %w", err)
	}
	return record, nil
}

func (db *DB) LatestBillingReconciliation(providerID string) (BillingReconciliation, error) {
	if db == nil || db.db == nil {
		return BillingReconciliation{}, errors.New("storage is closed")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return BillingReconciliation{}, errors.New("provider id cannot be empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()
	row := db.db.QueryRowContext(ctx, `
		SELECT id, provider_id, provider_name, period_start, period_end, official_cost, estimated_cost,
			difference, source, note, usage_details_json, created_at, updated_at
		FROM usage_billing_reconciliations
		WHERE provider_id = ?
		ORDER BY period_end DESC, updated_at DESC, id DESC
		LIMIT 1`, providerID)
	record, err := scanBillingReconciliation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BillingReconciliation{}, sql.ErrNoRows
	}
	if err != nil {
		return BillingReconciliation{}, fmt.Errorf("query latest billing reconciliation: %w", err)
	}
	return record, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanUsageRecord(scanner rowScanner) (UsageRecord, error) {
	var record UsageRecord
	var createdAt string
	if err := scanner.Scan(
		&record.ID,
		&createdAt,
		&record.SessionID,
		&record.ProviderID,
		&record.ProviderName,
		&record.Protocol,
		&record.ModelID,
		&record.Reasoning,
		&record.PromptCacheHitTokens,
		&record.PromptCacheMissTokens,
		&record.InputTokens,
		&record.OutputTokens,
		&record.EffectiveCost,
		&record.PricingSource,
		&record.PricingVersion,
		&record.InputPricePerMillion,
		&record.OutputPricePerMillion,
		&record.CacheHitPricePerMillion,
		&record.CacheMissPricePerMillion,
	); err != nil {
		return UsageRecord{}, fmt.Errorf("scan usage: %w", err)
	}
	if createdAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return UsageRecord{}, fmt.Errorf("parse usage timestamp: %w", err)
		}
		record.CreatedAt = parsed
	}
	return record, nil
}

func scanBillingReconciliation(scanner rowScanner) (BillingReconciliation, error) {
	var record BillingReconciliation
	var periodStart, periodEnd, createdAt, updatedAt string
	if err := scanner.Scan(
		&record.ID,
		&record.ProviderID,
		&record.ProviderName,
		&periodStart,
		&periodEnd,
		&record.OfficialCost,
		&record.EstimatedCost,
		&record.Difference,
		&record.Source,
		&record.Note,
		&record.UsageDetailsJSON,
		&createdAt,
		&updatedAt,
	); err != nil {
		return BillingReconciliation{}, err
	}
	var err error
	if record.PeriodStart, err = time.Parse(time.RFC3339Nano, periodStart); err != nil {
		return BillingReconciliation{}, fmt.Errorf("parse billing period start: %w", err)
	}
	if record.PeriodEnd, err = time.Parse(time.RFC3339Nano, periodEnd); err != nil {
		return BillingReconciliation{}, fmt.Errorf("parse billing period end: %w", err)
	}
	if record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return BillingReconciliation{}, fmt.Errorf("parse billing reconciliation created at: %w", err)
	}
	if record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return BillingReconciliation{}, fmt.Errorf("parse billing reconciliation updated at: %w", err)
	}
	return record, nil
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func nonNegativeFloat(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}
