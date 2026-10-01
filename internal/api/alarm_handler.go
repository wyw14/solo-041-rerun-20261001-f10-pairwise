package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/alarm"
)

func (s *Server) alarmList(w http.ResponseWriter, r *http.Request) {
	episodes, err := s.plant.AlarmEpisodes(r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alarms": episodes})
}

func (s *Server) alarmTimeline(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"alarms": s.plant.Alarms()})
}

type acknowledgeRequest struct {
	Operator string `json:"operator"`
	Note     string `json:"note"`
}

func (s *Server) acknowledgeAlarm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "alarmID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body acknowledgeRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	episode, err := s.plant.AcknowledgeAlarm(id, body.Operator, body.Note)
	if err != nil {
		writeAlarmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, episode)
}

func (s *Server) recoverAlarm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "alarmID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	episode, err := s.plant.RecoverAlarm(id)
	if err != nil {
		writeAlarmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, episode)
}

func writeAlarmError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, alarm.ErrEpisodeNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, alarm.ErrOperatorRequired):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, alarm.ErrEpisodeRecovered):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}
