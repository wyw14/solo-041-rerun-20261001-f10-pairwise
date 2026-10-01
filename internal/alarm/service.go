package alarm

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

var (
	// ErrEpisodeNotFound is returned when no episode matches the given ID.
	ErrEpisodeNotFound = errors.New("alarm episode is not found")
	// ErrEpisodeRecovered is returned when confirming a fault that already recovered.
	ErrEpisodeRecovered = errors.New("alarm episode has already recovered")
	// ErrConfirmerMissing is returned when the first confirmation has no operator.
	ErrConfirmerMissing = errors.New("confirmer is required")
)

// episodeKey merges raw alarms by alarm type (code) and device on one line.
type episodeKey struct {
	lineID model.LineID
	code   string
	device string
}

type Service struct {
	mu       sync.RWMutex
	episodes map[uuid.UUID]model.AlarmEpisode
	raw      map[uuid.UUID]model.Alarm
	open     map[episodeKey]uuid.UUID
}

func NewService() *Service {
	return &Service{
		episodes: make(map[uuid.UUID]model.AlarmEpisode),
		raw:      make(map[uuid.UUID]model.Alarm),
		open:     make(map[episodeKey]uuid.UUID),
	}
}

// Raise records a raw alarm on the timeline and folds it into the open fault
// episode for its line/code/device. A new episode only starts once the
// previous one has recovered, so repeated triggers extend one complete fault.
func (s *Service) Raise(alarm model.Alarm) model.AlarmEpisode {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := episodeKey{lineID: alarm.LineID, code: alarm.Code, device: alarm.DeviceID}
	if episodeID, ok := s.open[key]; ok {
		episode := s.episodes[episodeID]
		episode = episode.RecordTrigger(alarm)
		alarm.EpisodeID = episode.ID
		s.episodes[episode.ID] = episode
		s.raw[alarm.ID] = alarm
		return episode
	}
	episode := model.NewAlarmEpisode(alarm, alarm.RaisedAt)
	alarm.EpisodeID = episode.ID
	s.episodes[episode.ID] = episode
	s.raw[alarm.ID] = alarm
	s.open[key] = episode.ID
	return episode
}

// Acknowledge confirms the fault episode. Repeat confirmations are safe: the
// first confirmer is kept, an updated handling note is still recorded.
func (s *Service) Acknowledge(id uuid.UUID, confirmer, note string, now time.Time) (model.AlarmEpisode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.episodes[id]
	if !ok {
		return model.AlarmEpisode{}, ErrEpisodeNotFound
	}
	if episode.RecoveredAt != nil {
		return episode, ErrEpisodeRecovered
	}
	if episode.AcknowledgedAt == nil && confirmer == "" {
		return episode, ErrConfirmerMissing
	}
	episode, err := episode.Confirm(confirmer, now)
	if err != nil {
		return episode, err
	}
	if note != "" {
		episode, err = episode.Handle(note, now)
		if err != nil {
			return episode, err
		}
	}
	s.episodes[id] = episode
	return episode, nil
}

// Handle updates the handling note without changing confirmation state.
func (s *Service) Handle(id uuid.UUID, note string, now time.Time) (model.AlarmEpisode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.episodes[id]
	if !ok {
		return model.AlarmEpisode{}, ErrEpisodeNotFound
	}
	if episode.RecoveredAt != nil {
		return episode, ErrEpisodeRecovered
	}
	var err error
	episode, err = episode.Handle(note, now)
	if err != nil {
		return episode, err
	}
	s.episodes[id] = episode
	return episode, nil
}

// Recover closes the fault episode. A repeated recovery request is safe and
// idempotent: the first recovery time is returned unchanged.
func (s *Service) Recover(id uuid.UUID, now time.Time) (model.AlarmEpisode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.episodes[id]
	if !ok {
		return model.AlarmEpisode{}, ErrEpisodeNotFound
	}
	if episode.RecoveredAt != nil {
		return episode, nil
	}
	episode = episode.Close(now)
	s.episodes[id] = episode
	for key, openID := range s.open {
		if openID == id {
			delete(s.open, key)
		}
	}
	return episode, nil
}

// List returns episodes matching the lifecycle filter, newest first.
func (s *Service) List(filter model.EpisodeStatus) []model.AlarmEpisode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.AlarmEpisode, 0, len(s.episodes))
	for _, episode := range s.episodes {
		if episode.Matches(filter) {
			result = append(result, episode)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FirstRaisedAt.After(result[j].FirstRaisedAt) })
	return result
}

// Open returns every not-yet-recovered episode on a line, oldest first.
func (s *Service) Open(lineID model.LineID) []model.AlarmEpisode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.AlarmEpisode, 0)
	for _, episode := range s.episodes {
		if episode.LineID == lineID && episode.Active() {
			result = append(result, episode)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FirstRaisedAt.Before(result[j].FirstRaisedAt) })
	return result
}

// All returns every episode, newest first.
func (s *Service) All() []model.AlarmEpisode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.AlarmEpisode, 0, len(s.episodes))
	for _, episode := range s.episodes {
		result = append(result, episode)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FirstRaisedAt.After(result[j].FirstRaisedAt) })
	return result
}

// RawAlarms returns the unmerged alarm records in chronological order. The
// episode timeline preserves the same sequence; this accessor is kept for
// audits and tests.
func (s *Service) RawAlarms() []model.Alarm {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.Alarm, 0, len(s.raw))
	for _, alarm := range s.raw {
		result = append(result, alarm)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RaisedAt.Before(result[j].RaisedAt) })
	return result
}
