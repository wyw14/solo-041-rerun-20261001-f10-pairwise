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

type Alarm struct {
	ID           uuid.UUID     `json:"id"`
	LineID       LineID        `json:"line_id"`
	DeviceID     string        `json:"device_id"`
	Code         string        `json:"code"`
	Message      string        `json:"message"`
	Severity     AlarmSeverity `json:"severity"`
	RaisedAt     time.Time     `json:"raised_at"`
	Acknowledged bool          `json:"acknowledged"`
	RecoveredAt  *time.Time    `json:"recovered_at,omitempty"`
}

func NewAlarm(lineID LineID, deviceID, code, message string, severity AlarmSeverity, now time.Time) Alarm {
	return Alarm{
		ID: uuid.New(), LineID: lineID, DeviceID: deviceID, Code: code, Message: message,
		Severity: severity, RaisedAt: now.UTC(),
	}
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

type EpisodeStatus string

const (
	EpisodeActive       EpisodeStatus = "active"
	EpisodeAcknowledged EpisodeStatus = "acknowledged"
	EpisodeRecovered    EpisodeStatus = "recovered"
)

// Episode groups repeated alarms of the same code on the same device into one
// fault segment. AlarmIDs keeps the original alarm timeline intact.
type Episode struct {
	ID             uuid.UUID     `json:"id"`
	LineID         LineID        `json:"line_id"`
	DeviceID       string        `json:"device_id"`
	Code           string        `json:"code"`
	Message        string        `json:"message"`
	Severity       AlarmSeverity `json:"severity"`
	FirstRaisedAt  time.Time     `json:"first_raised_at"`
	LastRaisedAt   time.Time     `json:"last_raised_at"`
	TriggerCount   int           `json:"trigger_count"`
	AcknowledgedBy string        `json:"acknowledged_by,omitempty"`
	AcknowledgedAt *time.Time    `json:"acknowledged_at,omitempty"`
	Note           string        `json:"note,omitempty"`
	RecoveredAt    *time.Time    `json:"recovered_at,omitempty"`
	AlarmIDs       []uuid.UUID   `json:"alarm_ids"`
}

func NewEpisode(alarm Alarm) Episode {
	return Episode{
		ID: uuid.New(), LineID: alarm.LineID, DeviceID: alarm.DeviceID, Code: alarm.Code,
		Message: alarm.Message, Severity: alarm.Severity,
		FirstRaisedAt: alarm.RaisedAt, LastRaisedAt: alarm.RaisedAt,
		TriggerCount: 1, AlarmIDs: []uuid.UUID{alarm.ID},
	}
}

func (e Episode) Status() EpisodeStatus {
	switch {
	case e.RecoveredAt != nil:
		return EpisodeRecovered
	case e.AcknowledgedAt != nil:
		return EpisodeAcknowledged
	default:
		return EpisodeActive
	}
}

// ReTrigger folds another occurrence of the same fault into the episode.
func (e Episode) ReTrigger(alarm Alarm) Episode {
	e.Message = alarm.Message
	e.Severity = alarm.Severity
	e.LastRaisedAt = alarm.RaisedAt
	e.TriggerCount++
	e.AlarmIDs = append(e.AlarmIDs, alarm.ID)
	return e
}

// Acknowledge records the operator and handling note once; repeated
// acknowledgements keep the original record untouched.
func (e Episode) Acknowledge(operator, note string, now time.Time) (Episode, error) {
	if e.RecoveredAt != nil {
		return Episode{}, errors.New("episode is already recovered")
	}
	if e.AcknowledgedAt != nil {
		return e, nil
	}
	stamp := now.UTC()
	e.AcknowledgedBy = operator
	e.AcknowledgedAt = &stamp
	e.Note = note
	return e, nil
}

// Recover closes the episode once; repeated recoveries keep the original
// recovery time untouched.
func (e Episode) Recover(now time.Time) Episode {
	if e.RecoveredAt != nil {
		return e
	}
	stamp := now.UTC()
	e.RecoveredAt = &stamp
	return e
}
