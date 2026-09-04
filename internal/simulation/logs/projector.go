package logs

import (
	"fmt"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// Project rebuilds site logs only from the event stream.
func Project(stream []events.Event) ([]Entry, error) {
	started := make(map[model.RequestID]events.PageRequestStarted)
	entries := make([]Entry, 0)

	for _, event := range stream {
		switch event := event.(type) {
		case events.PageRequestStarted:
			if _, exists := started[event.RequestID]; exists {
				return nil, fmt.Errorf("request %q started more than once", event.RequestID)
			}
			started[event.RequestID] = event

		case events.PageRequestCompleted:
			request, exists := started[event.RequestID]
			if !exists {
				return nil, fmt.Errorf("request %q completed without start", event.RequestID)
			}
			entries = append(entries, entryFromCompleted(request, event))

		case events.PageRequestRejected:
			request, exists := started[event.RequestID]
			if !exists {
				return nil, fmt.Errorf("request %q rejected without start", event.RequestID)
			}
			entries = append(entries, Entry{
				Timestamp:      event.RejectedAt,
				RequestID:      request.RequestID,
				Source:         request.Source,
				VisitorID:      request.VisitorID,
				Page:           request.Page,
				ProductID:      request.ProductID,
				SourceIP:       request.SourceIP,
				UserAgent:      request.UserAgent,
				RegionCode:     request.RegionCode,
				FirewallRuleID: event.FirewallRuleID,
				StatusCode:     event.StatusCode,
				ErrorCode:      event.ErrorCode,
				Message:        event.Message,
			})
		}
	}
	return entries, nil
}

func entryFromCompleted(request events.PageRequestStarted, completed events.PageRequestCompleted) Entry {
	return Entry{
		Timestamp:      completed.CompletedAt,
		RequestID:      request.RequestID,
		Source:         request.Source,
		VisitorID:      request.VisitorID,
		Page:           request.Page,
		ProductID:      request.ProductID,
		SourceIP:       request.SourceIP,
		UserAgent:      request.UserAgent,
		RegionCode:     request.RegionCode,
		FirewallRuleID: completed.FirewallRuleID,
		StatusCode:     completed.StatusCode,
		ErrorCode:      completed.ErrorCode,
		Message:        completed.Message,
	}
}
