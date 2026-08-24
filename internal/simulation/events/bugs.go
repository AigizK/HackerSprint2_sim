package events

import "time"

type PageBugActivated struct {
	BugID                 BugID
	Page                  PageType
	ProductID             ProductID
	FailureProbabilityPPM uint32
	FixMessageHash        string
	ActivatedAt           time.Time
}

func (PageBugActivated) EventType() string { return "PageBugActivated" }

type PageBugTriggered struct {
	BugID       BugID
	RequestID   RequestID
	LogMessage  string
	TriggeredAt time.Time
}

func (PageBugTriggered) EventType() string { return "PageBugTriggered" }

type BugFixSubmitted struct {
	CommandID   CommandID
	Message     string
	SubmittedAt time.Time
}

func (BugFixSubmitted) EventType() string { return "BugFixSubmitted" }

type PageBugFixed struct {
	BugID   BugID
	FixedAt time.Time
}

func (PageBugFixed) EventType() string { return "PageBugFixed" }

type BugFixRejected struct {
	CommandID  CommandID
	Reason     string
	RejectedAt time.Time
}

func (BugFixRejected) EventType() string { return "BugFixRejected" }
