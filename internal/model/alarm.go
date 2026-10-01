package model

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type AlarmSeverity string

const (
	SeverityWarning  AlarmSeverity = "warning"
	SeverityCritical AlarmSeverity = "critical"
)

// Alarm is one raw entry on the alarm timeline. A single fault may produce
// several of these as the underlying signal flaps; AlarmEpisode merges them.
type Alarm struct {
	ID           uuid.UUID     `json:"id"`
	LineID       LineID        `json:"line_id"`
	Code         string        `json:"code"`
	DeviceID     string        `json:"device_id,omitempty"`
	Message      string        `json:"message"`
	Severity     AlarmSeverity `json:"severity"`
	RaisedAt     time.Time     `json:"raised_at"`
	EpisodeID    uuid.UUID     `json:"episode_id,omitempty"`
	Acknowledged bool          `json:"acknowledged"`
	RecoveredAt  *time.Time    `json:"recovered_at,omitempty"`
}

func NewAlarm(lineID LineID, code, message string, severity AlarmSeverity, now time.Time) Alarm {
	return Alarm{
		ID: uuid.New(), LineID: lineID, Code: code, Message: message,
		Severity: severity, RaisedAt: now.UTC(),
	}
}

// WithDevice tags the alarm with the device that raised it. Alarms sharing
// line, code and device are merged into the same fault episode.
func (a Alarm) WithDevice(deviceID string) Alarm {
	a.DeviceID = deviceID
	return a
}

func (a Alarm) Acknowledge() Alarm {
	a.Acknowledged = true
	return a
}

func (a Alarm) Recover(now time.Time) Alarm {
	stamp := now.UTC()
	a.RecoveredAt = &stamp
	return a
}

func (a Alarm) Active() bool {
	return a.RecoveredAt == nil
}

// EpisodeStatus is the lifecycle state of a merged fault episode.
type EpisodeStatus string

const (
	EpisodeAll          EpisodeStatus = "all"
	EpisodeActive       EpisodeStatus = "active"
	EpisodeAcknowledged EpisodeStatus = "acknowledged"
	EpisodeRecovered    EpisodeStatus = "recovered"
)

func ParseEpisodeStatus(raw string) (EpisodeStatus, error) {
	switch EpisodeStatus(raw) {
	case "", EpisodeAll:
		return EpisodeAll, nil
	case EpisodeActive:
		return EpisodeActive, nil
	case EpisodeAcknowledged:
		return EpisodeAcknowledged, nil
	case EpisodeRecovered:
		return EpisodeRecovered, nil
	default:
		return "", errors.New("alarm filter must be one of: all, active, acknowledged, recovered")
	}
}

// AlarmTimelineEntry records one transition of the raw alarm timeline so the
// original event sequence is never lost, even when it is merged into an
// episode.
type AlarmTimelineEntry struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Note    string    `json:"note,omitempty"`
}

const (
	TimelineRaised      = "raised"
	TimelineRetriggered = "retriggered"
	TimelineConfirmed   = "confirmed"
	TimelineHandled     = "handled"
	TimelineRecovered   = "recovered"
)

// AlarmEpisode is one complete fault merged from every raw alarm with the
// same alarm type (code) and device on one process line.
type AlarmEpisode struct {
	ID             uuid.UUID            `json:"id"`
	LineID         LineID               `json:"line_id"`
	Code           string               `json:"code"`
	DeviceID       string               `json:"device_id,omitempty"`
	Severity       AlarmSeverity        `json:"severity"`
	Message        string               `json:"message"`
	FirstRaisedAt  time.Time            `json:"first_raised_at"`
	LastRaisedAt   time.Time            `json:"last_raised_at"`
	RetriggeredAt  *time.Time           `json:"retriggered_at,omitempty"`
	TriggerCount   int                  `json:"trigger_count"`
	AcknowledgedAt *time.Time           `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string               `json:"acknowledged_by,omitempty"`
	HandledNote    string               `json:"handled_note,omitempty"`
	RecoveredAt    *time.Time           `json:"recovered_at,omitempty"`
	Timeline       []AlarmTimelineEntry `json:"timeline"`
}

// NewAlarmEpisode opens an episode from its first raw alarm.
func NewAlarmEpisode(first Alarm, now time.Time) AlarmEpisode {
	stamp := now.UTC()
	if !first.RaisedAt.IsZero() {
		stamp = first.RaisedAt
	}
	return AlarmEpisode{
		ID:            uuid.New(),
		LineID:        first.LineID,
		Code:          first.Code,
		DeviceID:      first.DeviceID,
		Severity:      first.Severity,
		Message:       first.Message,
		FirstRaisedAt: stamp,
		LastRaisedAt:  stamp,
		TriggerCount:  1,
		Timeline: []AlarmTimelineEntry{{
			At: stamp, Kind: TimelineRaised, Message: first.Message,
		}},
	}
}

// RecordTrigger folds another raw alarm for the same fault into the episode.
// Every trigger after the first is kept as a re-trigger on the timeline; the
// first one after the episode was confirmed is exposed as RetriggeredAt.
func (e AlarmEpisode) RecordTrigger(alarm Alarm) AlarmEpisode {
	stamp := alarm.RaisedAt
	e.LastRaisedAt = stamp
	e.TriggerCount++
	if alarm.Message != "" {
		e.Message = alarm.Message
	}
	if e.RetriggeredAt == nil && e.AcknowledgedAt != nil {
		e.RetriggeredAt = &stamp
	}
	e.Timeline = append(e.Timeline, AlarmTimelineEntry{At: stamp, Kind: TimelineRetriggered, Message: alarm.Message})
	return e
}

// Confirm marks the episode acknowledged. A repeat confirmation is safe and
// idempotent: once acknowledged, the first confirmer and time are kept and no
// duplicate timeline entry is written.
func (e AlarmEpisode) Confirm(operator string, now time.Time) (AlarmEpisode, error) {
	if e.RecoveredAt != nil {
		return e, errors.New("alarm episode has already recovered")
	}
	if e.AcknowledgedAt != nil {
		return e, nil
	}
	stamp := now.UTC()
	e.AcknowledgedAt = &stamp
	e.AcknowledgedBy = operator
	e.Timeline = append(e.Timeline, AlarmTimelineEntry{At: stamp, Kind: TimelineConfirmed, Actor: operator})
	return e, nil
}

// Handle records or updates the handling note for the episode.
func (e AlarmEpisode) Handle(note string, now time.Time) (AlarmEpisode, error) {
	if e.RecoveredAt != nil {
		return e, errors.New("alarm episode has already recovered")
	}
	if note == "" || note == e.HandledNote {
		return e, nil
	}
	stamp := now.UTC()
	e.HandledNote = note
	e.Timeline = append(e.Timeline, AlarmTimelineEntry{At: stamp, Kind: TimelineHandled, Note: note})
	return e, nil
}

// Close marks the episode recovered. Repeated recovery requests are safe: the
// first recovery time is preserved and the call stays idempotent.
func (e AlarmEpisode) Close(now time.Time) AlarmEpisode {
	if e.RecoveredAt != nil {
		return e
	}
	stamp := now.UTC()
	e.RecoveredAt = &stamp
	e.Timeline = append(e.Timeline, AlarmTimelineEntry{At: stamp, Kind: TimelineRecovered})
	return e
}

func (e AlarmEpisode) Active() bool {
	return e.RecoveredAt == nil
}

// Status derives the lifecycle filter value for the episode.
func (e AlarmEpisode) Status() EpisodeStatus {
	switch {
	case e.RecoveredAt != nil:
		return EpisodeRecovered
	case e.AcknowledgedAt != nil:
		return EpisodeAcknowledged
	default:
		return EpisodeActive
	}
}

// Matches reports whether the episode belongs to the given list filter.
func (e AlarmEpisode) Matches(filter EpisodeStatus) bool {
	switch filter {
	case EpisodeAll:
		return true
	case EpisodeAcknowledged:
		return e.Status() == EpisodeAcknowledged
	case EpisodeRecovered:
		return e.Status() == EpisodeRecovered
	case EpisodeActive:
		return e.Status() == EpisodeActive
	default:
		return true
	}
}
