package alarm

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

var (
	ErrEpisodeNotFound  = errors.New("alarm episode is not found")
	ErrEpisodeRecovered = errors.New("alarm episode is already recovered")
	ErrOperatorRequired = errors.New("operator is required")
)

type Service struct {
	mu        sync.RWMutex
	alarms    map[uuid.UUID]model.Alarm
	episodes  map[uuid.UUID]model.Episode
	openByKey map[string]uuid.UUID
}

func NewService() *Service {
	return &Service{
		alarms:    make(map[uuid.UUID]model.Alarm),
		episodes:  make(map[uuid.UUID]model.Episode),
		openByKey: make(map[string]uuid.UUID),
	}
}

func episodeKey(lineID model.LineID, code, deviceID string) string {
	return string(lineID) + "|" + code + "|" + deviceID
}

// Raise stores the raw alarm and folds it into the open episode for the same
// line, code and device, or opens a new episode when none is active.
func (s *Service) Raise(alarm model.Alarm) model.Alarm {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alarms[alarm.ID] = alarm
	key := episodeKey(alarm.LineID, alarm.Code, alarm.DeviceID)
	if id, ok := s.openByKey[key]; ok {
		if episode := s.episodes[id]; episode.RecoveredAt == nil {
			s.episodes[id] = episode.ReTrigger(alarm)
			return alarm
		}
	}
	episode := model.NewEpisode(alarm)
	s.episodes[episode.ID] = episode
	s.openByKey[key] = episode.ID
	return alarm
}

// Acknowledge records the operator and handling note on the episode. A
// repeated acknowledgement returns the current episode unchanged.
func (s *Service) Acknowledge(id uuid.UUID, operator, note string, now time.Time) (model.Episode, error) {
	if operator == "" {
		return model.Episode{}, ErrOperatorRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.episodes[id]
	if !ok {
		return model.Episode{}, ErrEpisodeNotFound
	}
	if episode.RecoveredAt != nil {
		return model.Episode{}, ErrEpisodeRecovered
	}
	if episode.AcknowledgedAt != nil {
		return episode, nil
	}
	updated, err := episode.Acknowledge(operator, note, now)
	if err != nil {
		return model.Episode{}, err
	}
	s.episodes[id] = updated
	for _, alarmID := range updated.AlarmIDs {
		s.alarms[alarmID] = s.alarms[alarmID].Acknowledge()
	}
	return updated, nil
}

// Recover closes the episode. A repeated recovery returns the current episode
// unchanged and keeps the original recovery time.
func (s *Service) Recover(id uuid.UUID, now time.Time) (model.Episode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.episodes[id]
	if !ok {
		return model.Episode{}, ErrEpisodeNotFound
	}
	if episode.RecoveredAt != nil {
		return episode, nil
	}
	updated := episode.Recover(now)
	s.episodes[id] = updated
	delete(s.openByKey, episodeKey(updated.LineID, updated.Code, updated.DeviceID))
	for _, alarmID := range updated.AlarmIDs {
		s.alarms[alarmID] = s.alarms[alarmID].Recover(now)
	}
	return updated, nil
}

// Episodes returns fault segments, optionally filtered by status
// ("active", "acknowledged", "recovered"); an empty status returns all.
func (s *Service) Episodes(status string) ([]model.Episode, error) {
	switch model.EpisodeStatus(status) {
	case "", model.EpisodeActive, model.EpisodeAcknowledged, model.EpisodeRecovered:
	default:
		return nil, fmt.Errorf("unknown episode status filter %q", status)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.Episode, 0, len(s.episodes))
	for _, episode := range s.episodes {
		if status != "" && string(episode.Status()) != status {
			continue
		}
		result = append(result, episode)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FirstRaisedAt.After(result[j].FirstRaisedAt) })
	return result, nil
}

// Active returns the open (not yet recovered) episodes for one process line.
func (s *Service) Active(lineID model.LineID) []model.Episode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.Episode, 0)
	for _, episode := range s.episodes {
		if episode.LineID == lineID && episode.RecoveredAt == nil {
			result = append(result, episode)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FirstRaisedAt.Before(result[j].FirstRaisedAt) })
	return result
}

// All returns the raw alarm timeline, newest first.
func (s *Service) All() []model.Alarm {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.Alarm, 0, len(s.alarms))
	for _, value := range s.alarms {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RaisedAt.After(result[j].RaisedAt) })
	return result
}
