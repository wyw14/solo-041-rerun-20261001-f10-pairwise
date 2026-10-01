package alarm

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

func raiseAt(service *Service, line model.LineID, device, code string, at time.Time) model.Alarm {
	return service.Raise(model.NewAlarm(line, device, code, "fault", model.SeverityWarning, at))
}

func episodesByStatus(t *testing.T, service *Service, status string) []model.Episode {
	t.Helper()
	episodes, err := service.Episodes(status)
	if err != nil {
		t.Fatalf("episodes(%q): %v", status, err)
	}
	return episodes
}

func TestRaiseMergesSameCodeAndDevice(t *testing.T) {
	service := NewService()
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	first := raiseAt(service, "line-1", "settler-line-1", "SETTLING_BLANKET_HIGH", base)
	second := raiseAt(service, "line-1", "settler-line-1", "SETTLING_BLANKET_HIGH", base.Add(time.Minute))
	raiseAt(service, "line-1", "settler-line-1", "OTHER_CODE", base.Add(2*time.Minute))
	raiseAt(service, "line-2", "settler-line-2", "SETTLING_BLANKET_HIGH", base.Add(3*time.Minute))

	episodes := episodesByStatus(t, service, "")
	if len(episodes) != 3 {
		t.Fatalf("expected 3 episodes, got %d", len(episodes))
	}
	var merged model.Episode
	for _, episode := range episodes {
		if episode.LineID == "line-1" && episode.Code == "SETTLING_BLANKET_HIGH" {
			merged = episode
		}
	}
	if merged.TriggerCount != 2 {
		t.Fatalf("expected trigger count 2, got %d", merged.TriggerCount)
	}
	if !merged.FirstRaisedAt.Equal(first.RaisedAt) || !merged.LastRaisedAt.Equal(second.RaisedAt) {
		t.Fatalf("first/last raised mismatch: %v / %v", merged.FirstRaisedAt, merged.LastRaisedAt)
	}
	if len(merged.AlarmIDs) != 2 {
		t.Fatalf("expected 2 raw alarm references, got %d", len(merged.AlarmIDs))
	}
	if got := len(service.All()); got != 4 {
		t.Fatalf("raw timeline must keep all 4 alarms, got %d", got)
	}
}

func TestAcknowledgeIsIdempotentAndKeepsOperator(t *testing.T) {
	service := NewService()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	raiseAt(service, "line-1", "settler-line-1", "SETTLING_BLANKET_HIGH", now)
	episode := episodesByStatus(t, service, "active")[0]

	acked, err := service.Acknowledge(episode.ID, "operator-a", "降低进水量", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	again, err := service.Acknowledge(episode.ID, "operator-b", "重复确认", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("duplicate acknowledge must not fail: %v", err)
	}
	if again.AcknowledgedBy != "operator-a" || again.Note != "降低进水量" {
		t.Fatalf("duplicate acknowledge overwrote record: %+v", again)
	}
	if !again.AcknowledgedAt.Equal(*acked.AcknowledgedAt) {
		t.Fatalf("duplicate acknowledge moved acknowledged_at")
	}
	if got := len(episodesByStatus(t, service, "acknowledged")); got != 1 {
		t.Fatalf("expected 1 acknowledged episode, got %d", got)
	}
	if got := len(episodesByStatus(t, service, "active")); got != 0 {
		t.Fatalf("expected no active episodes, got %d", got)
	}
	if _, err := service.Acknowledge(episode.ID, "", "x", now); !errors.Is(err, ErrOperatorRequired) {
		t.Fatalf("expected ErrOperatorRequired, got %v", err)
	}
}

func TestRecoverIsIdempotentAndReopensEpisode(t *testing.T) {
	service := NewService()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	raiseAt(service, "line-1", "settler-line-1", "SETTLING_BLANKET_HIGH", base)
	episode := episodesByStatus(t, service, "active")[0]

	recovered, err := service.Recover(episode.ID, base.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	again, err := service.Recover(episode.ID, base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("duplicate recover must not fail: %v", err)
	}
	if !again.RecoveredAt.Equal(*recovered.RecoveredAt) {
		t.Fatalf("duplicate recover moved recovered_at: %v", again.RecoveredAt)
	}
	if got := len(episodesByStatus(t, service, "recovered")); got != 1 {
		t.Fatalf("expected 1 recovered episode, got %d", got)
	}
	if got := len(service.Active("line-1")); got != 0 {
		t.Fatalf("expected no active episodes for line-1, got %d", got)
	}
	for _, raw := range service.All() {
		if raw.RecoveredAt == nil {
			t.Fatalf("raw alarm %s not marked recovered", raw.ID)
		}
	}

	// A new raise after recovery opens a fresh episode instead of touching the closed one.
	raiseAt(service, "line-1", "settler-line-1", "SETTLING_BLANKET_HIGH", base.Add(15*time.Minute))
	if got := len(episodesByStatus(t, service, "")); got != 2 {
		t.Fatalf("expected a new episode after recovery, got %d total", got)
	}
	if got := len(episodesByStatus(t, service, "active")); got != 1 {
		t.Fatalf("expected 1 active episode after re-trigger, got %d", got)
	}
}

func TestAcknowledgeAfterRecoverFails(t *testing.T) {
	service := NewService()
	now := time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
	raiseAt(service, "line-1", "settler-line-1", "SETTLING_BLANKET_HIGH", now)
	episode := episodesByStatus(t, service, "active")[0]
	if _, err := service.Recover(episode.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if _, err := service.Acknowledge(episode.ID, "operator-a", "", now); !errors.Is(err, ErrEpisodeRecovered) {
		t.Fatalf("expected ErrEpisodeRecovered, got %v", err)
	}
	if _, err := service.Acknowledge(uuid.Nil, "operator-a", "", now); !errors.Is(err, ErrEpisodeNotFound) {
		t.Fatalf("expected ErrEpisodeNotFound, got %v", err)
	}
}

func TestEpisodesRejectsUnknownStatus(t *testing.T) {
	service := NewService()
	if _, err := service.Episodes("bogus"); err == nil {
		t.Fatal("expected error for unknown status filter")
	}
}
