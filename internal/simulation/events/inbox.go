package events

import "time"

type InboxMessageDelivered struct {
	ScenarioID  string
	StepID      string
	MessageID   MessageID
	SenderEmail string
	SentAt      time.Time
	Subject     string
	Description string
}

func (InboxMessageDelivered) EventType() string { return "InboxMessageDelivered" }
