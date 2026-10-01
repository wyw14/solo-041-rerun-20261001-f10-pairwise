package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/alarm"
	"github.com/wyw14/cry-97/internal/model"
)

func (s *Server) alarmList(w http.ResponseWriter, r *http.Request) {
	filter, err := model.ParseEpisodeStatus(r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": s.plant.Alarms(filter)})
}

type acknowledgeAlarmRequest struct {
	Confirmer string `json:"confirmer"`
	Note      string `json:"note"`
}

func (s *Server) acknowledgeAlarm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "alarmID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var input acknowledgeAlarmRequest
	if err := decodeJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	episode, err := s.plant.AcknowledgeAlarm(id, input.Confirmer, input.Note)
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
	case errors.Is(err, alarm.ErrEpisodeRecovered), errors.Is(err, alarm.ErrConfirmerMissing):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}
