package spec

import (
	"fmt"
	"reflect"
	"time"
)

type InboxMessageView struct {
	MessageID   string
	SenderEmail string
	SentAt      time.Time
	Subject     string
	Description string
}

type InboxPageView struct {
	Messages []InboxMessageView
	HasNext  bool
}

type InboxScenarioInput struct {
	Messages  []InboxMessageView
	ReadTimes []time.Time
	Limit     int
}

type InboxScenarioResult struct {
	Reads [][]InboxPageView
}

type InboxFutureDriver interface {
	InboxScenario(runID string, input InboxScenarioInput) (InboxScenarioResult, error)
}

type InboxFutureDSL struct{ s *Scenario }

func (d InboxFutureDSL) InboxScenario(input InboxScenarioInput, want InboxScenarioResult) Assertion {
	return func(s *Scenario) error {
		if s.inbox == nil {
			return ErrFutureDriverNotImplemented
		}
		got, err := s.inbox.InboxScenario(s.runID, input)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("inbox scenario = %#v, want %#v", got, want)
		}
		return nil
	}
}
