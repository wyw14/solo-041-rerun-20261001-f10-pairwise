package alarm

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

func testClock() func() time.Time {
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	cursor := base
	return func() time.Time {
		cursor = cursor.Add(time.Minute)
		return cursor
	}
}

func blanketAlarm(line model.LineID, device, message string, at time.Time) model.Alarm {
	return model.NewAlarm(line, "SETTLING_BLANKET_HIGH", message, model.SeverityWarning, at).WithDevice(device)
}

func TestRaiseMergesRepeatedTriggersIntoOneEpisode(t *testing.T) {
	now := testClock()
	service := NewService()

	firstAt, secondAt, thirdAt := now(), now(), now()
	first := service.Raise(blanketAlarm("line-1", "settler-line-1", "high at 3.10 m", firstAt))
	again := service.Raise(blanketAlarm("line-1", "settler-line-1", "high at 3.20 m", secondAt))
	third := service.Raise(blanketAlarm("line-1", "settler-line-1", "high at 3.30 m", thirdAt))

	if first.ID != again.ID || again.ID != third.ID {
		t.Fatalf("repeated triggers must merge into one episode, got %s %s %s", first.ID, again.ID, third.ID)
	}
	episode := third
	if episode.TriggerCount != 3 {
		t.Fatalf("trigger count = %d, want 3", episode.TriggerCount)
	}
	if !episode.FirstRaisedAt.Equal(firstAt) {
		t.Fatalf("first raised time = %v, want %v", episode.FirstRaisedAt, firstAt)
	}
	if !episode.LastRaisedAt.Equal(thirdAt) {
		t.Fatalf("last raised time = %v, want %v", episode.LastRaisedAt, thirdAt)
	}
	if episode.Message != "high at 3.30 m" {
		t.Fatalf("latest message should be kept, got %q", episode.Message)
	}
	if episode.Status() != model.EpisodeActive {
		t.Fatalf("status = %s, want active", episode.Status())
	}
	if len(episode.Timeline) != 3 {
		t.Fatalf("timeline entries = %d, want 3 raw triggers", len(episode.Timeline))
	}
	if len(service.RawAlarms()) != 3 {
		t.Fatalf("raw alarm timeline must keep every record, got %d", len(service.RawAlarms()))
	}
	if len(service.All()) != 1 {
		t.Fatalf("merged episode list = %d, want 1", len(service.All()))
	}
}

func TestDifferentDevicesAndCodesOpenSeparateEpisodes(t *testing.T) {
	now := testClock()
	service := NewService()
	a := service.Raise(blanketAlarm("line-1", "settler-line-1", "basin A", now()))
	b := service.Raise(blanketAlarm("line-1", "settler-line-2", "basin B", now()))
	c := service.Raise(model.NewAlarm("line-1", "PROCESS_EMERGENCY", "stop", model.SeverityCritical, now()).WithDevice("settler-line-1"))

	if a.ID == b.ID || a.ID == c.ID {
		t.Fatalf("different device/code must not merge: %s %s %s", a.ID, b.ID, c.ID)
	}
	if len(service.All()) != 3 {
		t.Fatalf("episodes = %d, want 3", len(service.All()))
	}
}

func TestRetriggerAfterAcknowledgeIsRecorded(t *testing.T) {
	now := testClock()
	service := NewService()
	service.Raise(blanketAlarm("line-1", "settler-line-1", "high", now()))

	ackAt := now()
	ack, err := service.Acknowledge(episodeID(service, t), "operator-li", "checking return pump", ackAt)
	if err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if ack.AcknowledgedBy != "operator-li" || !ack.AcknowledgedAt.Equal(ackAt) {
		t.Fatalf("confirmer not recorded: %+v", ack)
	}

	retriggerAt := now()
	episode := service.Raise(blanketAlarm("line-1", "settler-line-1", "high again", retriggerAt))
	if episode.RetriggeredAt == nil || !episode.RetriggeredAt.Equal(retriggerAt) {
		t.Fatalf("retrigger time = %v, want %v", episode.RetriggeredAt, retriggerAt)
	}
	if episode.AcknowledgedBy != "operator-li" {
		t.Fatalf("acknowledgement must survive re-trigger")
	}
	if episode.TriggerCount != 2 {
		t.Fatalf("trigger count = %d, want 2", episode.TriggerCount)
	}
	kinds := timelineKinds(episode.Timeline)
	want := []string{model.TimelineRaised, model.TimelineConfirmed, model.TimelineHandled, model.TimelineRetriggered}
	if !equalStrings(kinds, want) {
		t.Fatalf("timeline kinds = %v, want %v", kinds, want)
	}
}

func TestRepeatAcknowledgeIsIdempotent(t *testing.T) {
	now := testClock()
	service := NewService()
	service.Raise(blanketAlarm("line-1", "settler-line-1", "high", now()))
	id := episodeID(service, t)
	first, err := service.Acknowledge(id, "operator-li", "first note", now())
	if err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	// Duplicated request, possibly from another operator after a retry.
	again, err := service.Acknowledge(id, "operator-wang", "first note", now())
	if err != nil {
		t.Fatalf("repeat acknowledge must be safe: %v", err)
	}
	if again.AcknowledgedBy != "operator-li" || !again.AcknowledgedAt.Equal(*first.AcknowledgedAt) {
		t.Fatalf("first confirmer/time must be preserved: %+v", again)
	}
	if len(again.Timeline) != len(first.Timeline) {
		t.Fatalf("duplicate acknowledgement must not grow the timeline: %d vs %d", len(again.Timeline), len(first.Timeline))
	}

	// A genuinely new handling note is still accepted.
	updated, err := service.Handle(id, "pump restarted", now())
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if updated.HandledNote != "pump restarted" {
		t.Fatalf("handling note = %q", updated.HandledNote)
	}
	last := updated.Timeline[len(updated.Timeline)-1]
	if last.Kind != model.TimelineHandled || last.Note != "pump restarted" {
		t.Fatalf("handling timeline entry missing: %+v", last)
	}
}

func TestAcknowledgeRequiresConfirmer(t *testing.T) {
	now := testClock()
	service := NewService()
	service.Raise(blanketAlarm("line-1", "settler-line-1", "high", now()))
	if _, err := service.Acknowledge(episodeID(service, t), "", "", now()); !errors.Is(err, ErrConfirmerMissing) {
		t.Fatalf("want ErrConfirmerMissing, got %v", err)
	}
}

func TestRecoverAndNewEpisodeAfterRecovery(t *testing.T) {
	now := testClock()
	service := NewService()
	service.Raise(blanketAlarm("line-1", "settler-line-1", "high", now()))
	id := episodeID(service, t)
	if _, err := service.Acknowledge(id, "operator-li", "", now()); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	recoverAt := now()
	recovered, err := service.Recover(id, recoverAt)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !recovered.RecoveredAt.Equal(recoverAt) {
		t.Fatalf("recovery time = %v, want %v", recovered.RecoveredAt, recoverAt)
	}
	if recovered.Status() != model.EpisodeRecovered {
		t.Fatalf("status = %s, want recovered", recovered.Status())
	}

	// Repeat recovery request is safe and keeps the first time.
	again, err := service.Recover(id, now())
	if err != nil {
		t.Fatalf("repeat recovery must be safe: %v", err)
	}
	if !again.RecoveredAt.Equal(recoverAt) {
		t.Fatalf("first recovery time changed: %v", again.RecoveredAt)
	}
	if len(again.Timeline) != len(recovered.Timeline) {
		t.Fatalf("repeat recovery must not grow the timeline")
	}

	// Confirming a recovered fault is rejected rather than silently mutating it.
	if _, err := service.Acknowledge(id, "operator-li", "", now()); !errors.Is(err, ErrEpisodeRecovered) {
		t.Fatalf("want ErrEpisodeRecovered, got %v", err)
	}

	// A later trigger of the same type/device starts a fresh complete fault.
	next := service.Raise(blanketAlarm("line-1", "settler-line-1", "high next cycle", now()))
	if next.ID == id {
		t.Fatalf("trigger after recovery must open a new episode")
	}
	if next.TriggerCount != 1 || next.Status() != model.EpisodeActive {
		t.Fatalf("new episode state wrong: %+v", next)
	}
	// The recovered episode and its original timeline are still listed.
	all := service.List(model.EpisodeAll)
	if len(all) != 2 {
		t.Fatalf("episodes = %d, want 2 (recovered history retained)", len(all))
	}
}

func TestListFilters(t *testing.T) {
	now := testClock()
	service := NewService()
	service.Raise(blanketAlarm("line-1", "settler-line-1", "active fault", now()))
	id := service.Raise(blanketAlarm("line-2", "settler-line-2", "acked fault", now())).ID
	if _, err := service.Acknowledge(id, "operator-li", "", now()); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	id = service.Raise(blanketAlarm("line-3", "settler-line-3", "done fault", now())).ID
	if _, err := service.Acknowledge(id, "operator-li", "", now()); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if _, err := service.Recover(id, now()); err != nil {
		t.Fatalf("recover: %v", err)
	}

	cases := map[model.EpisodeStatus]int{
		model.EpisodeActive:       1,
		model.EpisodeAcknowledged: 1,
		model.EpisodeRecovered:    1,
		model.EpisodeAll:          3,
	}
	for filter, want := range cases {
		if got := len(service.List(filter)); got != want {
			t.Fatalf("filter %s = %d episodes, want %d", filter, got, want)
		}
	}
	if open := service.Open("line-2"); len(open) != 1 || open[0].LineID != "line-2" {
		t.Fatalf("open episodes for line-2 = %+v", open)
	}
}

func TestUnknownEpisode(t *testing.T) {
	service := NewService()
	if _, err := service.Acknowledge(uuid.New(), "li", "", time.Now()); !errors.Is(err, ErrEpisodeNotFound) {
		t.Fatalf("want ErrEpisodeNotFound, got %v", err)
	}
	if _, err := service.Recover(uuid.New(), time.Now()); !errors.Is(err, ErrEpisodeNotFound) {
		t.Fatalf("want ErrEpisodeNotFound, got %v", err)
	}
	if _, err := service.Handle(uuid.New(), "note", time.Now()); !errors.Is(err, ErrEpisodeNotFound) {
		t.Fatalf("want ErrEpisodeNotFound, got %v", err)
	}
}

func episodeID(service *Service, t *testing.T) uuid.UUID {
	t.Helper()
	all := service.All()
	if len(all) != 1 {
		t.Fatalf("want exactly one episode, got %d", len(all))
	}
	return all[0].ID
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func timelineKinds(entries []model.AlarmTimelineEntry) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Kind)
	}
	return result
}
