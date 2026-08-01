package storage

type Migration struct {
	Version    int
	Name       string
	Statements []string
}

func InitialMigrations() []Migration {
	return []Migration{
		{
			Version: 1,
			Name:    "usage_metrics",
			Statements: []string{
				`CREATE TABLE IF NOT EXISTS usage_metrics (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					prompt_cache_hit_tokens INTEGER NOT NULL DEFAULT 0,
					prompt_cache_miss_tokens INTEGER NOT NULL DEFAULT 0,
					input_tokens INTEGER NOT NULL DEFAULT 0,
					output_tokens INTEGER NOT NULL DEFAULT 0,
					effective_cost REAL NOT NULL DEFAULT 0
				)`,
			},
		},
		{
			Version: 2,
			Name:    "usage_dimensions",
			Statements: []string{
				"ALTER TABLE usage_metrics ADD COLUMN created_at TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN session_id TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN provider_id TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN provider_name TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN protocol TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN model_id TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN reasoning TEXT NOT NULL DEFAULT ''",
				"CREATE INDEX IF NOT EXISTS usage_metrics_session_id_id ON usage_metrics(session_id, id)",
				"CREATE INDEX IF NOT EXISTS usage_metrics_provider_model_id ON usage_metrics(provider_id, model_id, id)",
			},
		},
		{
			Version: 3,
			Name:    "usage_pricing_snapshots_and_reconciliation",
			Statements: []string{
				"ALTER TABLE usage_metrics ADD COLUMN pricing_source TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN pricing_version TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE usage_metrics ADD COLUMN input_price_per_million REAL NOT NULL DEFAULT 0",
				"ALTER TABLE usage_metrics ADD COLUMN output_price_per_million REAL NOT NULL DEFAULT 0",
				"ALTER TABLE usage_metrics ADD COLUMN cache_hit_price_per_million REAL NOT NULL DEFAULT 0",
				"ALTER TABLE usage_metrics ADD COLUMN cache_miss_price_per_million REAL NOT NULL DEFAULT 0",
				`CREATE TABLE IF NOT EXISTS usage_billing_reconciliations (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					provider_id TEXT NOT NULL,
					provider_name TEXT NOT NULL DEFAULT '',
					period_start TEXT NOT NULL,
					period_end TEXT NOT NULL,
					official_cost REAL NOT NULL DEFAULT 0,
					estimated_cost REAL NOT NULL DEFAULT 0,
					difference REAL NOT NULL DEFAULT 0,
					source TEXT NOT NULL DEFAULT '',
					note TEXT NOT NULL DEFAULT '',
					created_at TEXT NOT NULL,
					updated_at TEXT NOT NULL,
					UNIQUE(provider_id, period_start, period_end)
				)`,
				"CREATE INDEX IF NOT EXISTS usage_billing_reconciliations_provider_updated ON usage_billing_reconciliations(provider_id, updated_at DESC)",
			},
		},
		{
			Version: 4,
			Name:    "usage_billing_usage_details",
			Statements: []string{
				"ALTER TABLE usage_billing_reconciliations ADD COLUMN usage_details_json TEXT NOT NULL DEFAULT ''",
			},
		},
	}
}
