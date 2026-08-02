package socialbrowser

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

// Autopilot manages the Social Media agent's autonomous scheduling. When
// enabled, it creates a recurring cron job that triggers the social-media-manager
// role to check inbox, learn platforms, and manage accounts.
type Autopilot struct {
	db  store.Store
	cfg *config.Config
}

// NewAutopilot creates an Autopilot tied to the store and config.
func NewAutopilot(db store.Store, cfg *config.Config) *Autopilot {
	return &Autopilot{db: db, cfg: cfg}
}

// Sync ensures the social media autopilot cron job exists when enabled, and
// removes it when disabled. Call this on startup and after config reload.
func (a *Autopilot) Sync(ctx context.Context) error {
	cfg := a.cfg
	if cfg == nil {
		return nil
	}

	enabled := cfg.Social.Enabled && cfg.Social.AutopilotEnabled
	jobID := "social_autopilot"

	existing, err := a.db.GetCronJob(ctx, jobID)
	if err != nil && err != store.ErrNotFound {
		return fmt.Errorf("check social autopilot: %w", err)
	}

	if !enabled {
		if existing != nil {
			slog.Info("social autopilot disabled, removing cron job")
			return a.db.DeleteCronJob(ctx, jobID)
		}
		return nil
	}

	// Create or update the job.
	prompt := "You are the social-media-manager. Run your autopilot routine:\n" +
		"1. Check the Gmail inbox for verification emails and OTP codes.\n" +
		"2. Check each connected social media account's health.\n" +
		"3. Learn about platform changes (algorithm updates, new features, UI changes).\n" +
		"4. Create and publish content if the content plan has due items.\n" +
		"5. Update skills and RAG with any new findings.\n" +
		"Report what you did concisely."

	schedule := "0 */6 * * *" // Every 6 hours

	if existing == nil {
		job := &store.CronJob{
			ID:        jobID,
			Name:      "Social Media Autopilot",
			Schedule:  schedule,
			Prompt:    prompt,
			Enabled:   true,
			Meta:      store.Meta{"role": "social-media-manager"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		slog.Info("social autopilot enabled, creating cron job", "schedule", schedule)
		return a.db.PutCronJob(ctx, job)
	}

	// Update if changed.
	existingRole := ""
	if r, ok := existing.Meta["role"]; ok {
		if s, ok := r.(string); ok {
			existingRole = s
		}
	}
	if existing.Prompt != prompt || existing.Schedule != schedule || existingRole != "social-media-manager" {
		existing.Prompt = prompt
		existing.Schedule = schedule
		if existing.Meta == nil {
			existing.Meta = store.Meta{}
		}
		existing.Meta["role"] = "social-media-manager"
		existing.Enabled = true
		existing.UpdatedAt = time.Now()
		return a.db.PutCronJob(ctx, existing)
	}

	return nil
}
