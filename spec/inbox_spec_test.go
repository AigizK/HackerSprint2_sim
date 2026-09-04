package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestInboxReturnsOnlyDeliveredMessagesInStablePages(t *testing.T) {
	startsAt := time.Date(2033, 3, 1, 9, 0, 0, 0, time.UTC)
	s := spec.New(t, "run-inbox-delivery")
	s.Given(s.World.Created(42, startsAt, startsAt.Add(24*time.Hour)))

	first := spec.InboxMessageView{
		MessageID: "message-001", SenderEmail: "director@example.com", SentAt: startsAt.Add(time.Minute),
		Subject: "Доступ к магазину", Description: "Логин и пароль находятся в этом сообщении.",
	}
	second := spec.InboxMessageView{
		MessageID: "message-002", SenderEmail: "director@example.com", SentAt: startsAt.Add(5 * time.Minute),
		Subject: "Назначение менеджера", Description: "manager@acme.example управляет товарами Acme.",
	}
	third := spec.InboxMessageView{
		MessageID: "message-003", SenderEmail: "manager@acme.example", SentAt: startsAt.Add(5 * time.Minute),
		Subject: "Изменить цену", Description: "Измените цену product-001.",
	}
	future := spec.InboxMessageView{
		MessageID: "message-004", SenderEmail: "director@example.com", SentAt: startsAt.Add(20 * time.Minute),
		Subject: "Новое поручение", Description: "Это сообщение ещё не доставлено при первом чтении.",
	}

	s.Then(s.Inbox.InboxScenario(spec.InboxScenarioInput{
		Messages:  []spec.InboxMessageView{third, future, second, first},
		ReadTimes: []time.Time{startsAt.Add(10 * time.Minute), startsAt.Add(30 * time.Minute), startsAt.Add(30 * time.Minute)},
		Limit:     2,
	}, spec.InboxScenarioResult{Reads: [][]spec.InboxPageView{
		{
			{Messages: []spec.InboxMessageView{first, second}, HasNext: true},
			{Messages: []spec.InboxMessageView{third}},
		},
		{
			{Messages: []spec.InboxMessageView{first, second}, HasNext: true},
			{Messages: []spec.InboxMessageView{third, future}},
		},
		{
			{Messages: []spec.InboxMessageView{first, second}, HasNext: true},
			{Messages: []spec.InboxMessageView{third, future}},
		},
	}}))
}

func TestEmptyInboxReturnsAnEmptyPage(t *testing.T) {
	startsAt := time.Date(2033, 3, 1, 9, 0, 0, 0, time.UTC)
	s := spec.New(t, "run-empty-inbox")
	s.Given(s.World.Created(42, startsAt, startsAt.Add(24*time.Hour)))

	s.Then(s.Inbox.InboxScenario(spec.InboxScenarioInput{
		ReadTimes: []time.Time{startsAt}, Limit: 100,
	}, spec.InboxScenarioResult{Reads: [][]spec.InboxPageView{{{}}}}))
}
