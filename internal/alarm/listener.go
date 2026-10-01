package alarm

import (
	"fmt"
	"time"

	"github.com/wyw14/cry-97/internal/model"
	"github.com/wyw14/cry-97/internal/settling"
)

type BlanketListener struct {
	service *Service
	state   *settling.State
	now     func() time.Time
}

func NewBlanketListener(service *Service, now func() time.Time) *BlanketListener {
	return &BlanketListener{service: service, now: now}
}

func (l *BlanketListener) Bind(state *settling.State) {
	l.state = state
}

func (l *BlanketListener) BlanketChanged(notice settling.BlanketNotice) {
	if l.service == nil || l.now == nil || l.state == nil || !notice.After.High {
		return
	}
	current := l.state.Snapshot()
	message := fmt.Sprintf("sludge blanket high in %s at %.2f m", current.BasinID, current.BlanketLevel)
	l.service.Raise(model.NewAlarm(current.LineID, current.BasinID, "SETTLING_BLANKET_HIGH", message, model.SeverityWarning, l.now()))
}
