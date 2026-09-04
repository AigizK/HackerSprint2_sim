package simulation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func decideRecordControlCommandResponse(state State, command RecordControlCommandResponse) ([]events.Event, error) {
	if err := ensureRunExists(state); err != nil {
		return nil, err
	}
	if previous, exists := state.ControlCommandReceipts[command.CommandID]; exists {
		if previous.Command != command.Command || previous.PayloadSHA256 != command.PayloadSHA256 {
			return nil, ErrIdempotencyConflict
		}
		return nil, nil
	}
	if command.CommandID == "" || command.Command == "" || command.PayloadSHA256 == "" ||
		(command.StatusCode != 200 && command.StatusCode != 202) {
		return nil, fmt.Errorf("%w: invalid control command receipt", ErrInvalidCommand)
	}
	result := cloneJSON(command.Result)
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	at := state.Clock.CurrentTime
	return []events.Event{
		events.ControlCommandAccepted{CommandID: command.CommandID, Command: command.Command, PayloadSHA256: command.PayloadSHA256,
			Params: cloneJSON(command.Params), OperationID: command.OperationID, AcceptedAt: at},
		events.ControlCommandResponseRecorded{CommandID: command.CommandID, Command: command.Command, StatusCode: command.StatusCode,
			OperationID: command.OperationID, Result: result, ErrorCode: command.ErrorCode, Message: command.Message, RecordedAt: at},
	}, nil
}

func decideRecordControlOperationResult(state State, command RecordControlOperationResult) ([]events.Event, error) {
	if err := ensureRunExists(state); err != nil {
		return nil, err
	}
	if previous, exists := state.ControlOperationResults[command.OperationID]; exists {
		result := cloneJSON(command.Result)
		if previous.CommandID != command.CommandID || previous.Command != command.Command || !bytes.Equal(previous.Result, result) ||
			previous.ErrorCode != command.ErrorCode || previous.Message != command.Message {
			return nil, ErrIdempotencyConflict
		}
		return nil, nil
	}
	receipt, exists := state.ControlCommandReceipts[command.CommandID]
	operation, operationExists := state.Operations[command.OperationID]
	if command.OperationID == "" || command.CommandID == "" || command.Command == "" || !exists || !operationExists ||
		receipt.OperationID != command.OperationID || receipt.Command != command.Command ||
		(operation.Status != model.OperationStatusSucceeded && operation.Status != model.OperationStatusFailed) {
		return nil, fmt.Errorf("%w: invalid control operation result", ErrInvalidCommand)
	}
	result := cloneJSON(command.Result)
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	return []events.Event{events.ControlOperationResultRecorded{OperationID: command.OperationID, CommandID: command.CommandID,
		Command: command.Command, Result: result, ErrorCode: command.ErrorCode, Message: command.Message, RecordedAt: state.Clock.CurrentTime}}, nil
}

func decideIssueServerCredential(state State, command IssueServerCredential) ([]events.Event, error) {
	if err := ensureRunning(state); err != nil {
		return nil, err
	}
	server, serverExists := state.Servers[command.ServerID]
	if existing, exists := state.ServerCredentials[command.CredentialID]; exists {
		if existing.ServerID == command.ServerID && existing.Version == command.Version {
			return nil, nil
		}
		return nil, ErrIdempotencyConflict
	}
	if !serverExists || command.CredentialID == "" || command.Version == 0 || command.MessageID == "" ||
		command.ValidFrom.Before(state.Clock.CurrentTime) || !command.ExpiresAt.After(command.ValidFrom) {
		return nil, fmt.Errorf("%w: invalid server credential issuance", ErrInvalidCommand)
	}
	if command.Version == 1 {
		if command.SupersedesCredentialID != "" || (server.CredentialID != "" && server.CredentialID != command.CredentialID) {
			return nil, fmt.Errorf("%w: invalid first server credential", ErrInvalidCommand)
		}
	} else {
		previous, exists := state.ServerCredentials[command.SupersedesCredentialID]
		if !exists || previous.ServerID != command.ServerID || previous.Version+1 != command.Version || server.CredentialID != previous.CredentialID {
			return nil, fmt.Errorf("%w: invalid server credential rotation", ErrInvalidCommand)
		}
	}
	at := state.Clock.CurrentTime
	return []events.Event{
		events.ServerCredentialIssued{CredentialID: command.CredentialID, ServerID: command.ServerID, Version: command.Version,
			SupersedesCredentialID: command.SupersedesCredentialID, ValidFrom: command.ValidFrom, ExpiresAt: command.ExpiresAt, IssuedAt: at},
		events.InboxMessageDelivered{MessageID: command.MessageID, SenderEmail: "credentials@service.example", SentAt: at,
			Subject:     "Новые credentials сервера " + string(command.ServerID),
			Description: "Получите серверные credentials по credential_id=" + string(command.CredentialID) + ". Используйте их как target_auth только для server_id=" + string(command.ServerID) + "."},
	}, nil
}

func decideScheduledServerCredentialRotation(state State, request events.ServerCredentialRotationRequested) []events.Event {
	server, exists := state.Servers[request.ServerID]
	if !exists {
		return nil
	}
	version := uint64(1)
	var supersedes model.CredentialID
	if current, ok := state.ServerCredentials[server.CredentialID]; ok {
		version = current.Version + 1
		supersedes = current.CredentialID
	}
	credentialID := ServerCredentialID(state.RunID, request.ServerID, version)
	validFrom, expiresAt := credentialValidityWindow(request.RequestedAt, state.Clock.EndsAt)
	return []events.Event{
		events.ServerCredentialIssued{CredentialID: credentialID, ServerID: request.ServerID, Version: version,
			SupersedesCredentialID: supersedes, ValidFrom: validFrom, ExpiresAt: expiresAt, IssuedAt: request.RequestedAt},
		events.InboxMessageDelivered{ScenarioID: request.RotationID, StepID: "credentials-rotated",
			MessageID: model.MessageID("message-" + request.RotationID), SenderEmail: "credentials@service.example", SentAt: request.RequestedAt,
			Subject: "Новые credentials сервера " + string(request.ServerID),
			Description: "Старые credentials больше не действуют. Получите новые по credential_id=" + string(credentialID) +
				" и используйте их как target_auth только для server_id=" + string(request.ServerID) + "."},
	}
}

func (s *State) applyControlEvent(event events.Event) (bool, error) {
	switch event := event.(type) {
	case events.ControlCommandAccepted:
		if err := ensureRunExists(*s); err != nil {
			return true, err
		}
		if event.CommandID == "" || event.Command == "" || event.PayloadSHA256 == "" || !validFactTime(*s, event.AcceptedAt) {
			return true, fmt.Errorf("%w: invalid control command receipt", ErrInvalidEvent)
		}
		if s.ControlCommandReceipts == nil {
			s.ControlCommandReceipts = make(map[model.CommandID]ControlCommandReceiptState)
		}
		if _, exists := s.ControlCommandReceipts[event.CommandID]; exists {
			return true, ErrIdempotencyConflict
		}
		s.ControlCommandReceipts[event.CommandID] = ControlCommandReceiptState{CommandID: event.CommandID, Command: event.Command,
			PayloadSHA256: event.PayloadSHA256, Params: append([]byte(nil), event.Params...), OperationID: event.OperationID, AcceptedAt: event.AcceptedAt}
		return true, nil

	case events.ControlCommandResponseRecorded:
		receipt, exists := s.ControlCommandReceipts[event.CommandID]
		if !exists || receipt.RecordedAt.IsZero() == false || receipt.Command != event.Command || receipt.OperationID != event.OperationID ||
			(event.StatusCode != 200 && event.StatusCode != 202) || !validFactTime(*s, event.RecordedAt) {
			return true, fmt.Errorf("%w: invalid control command response", ErrInvalidEvent)
		}
		receipt.StatusCode, receipt.Result, receipt.ErrorCode, receipt.Message, receipt.RecordedAt = event.StatusCode,
			append([]byte(nil), event.Result...), event.ErrorCode, event.Message, event.RecordedAt
		s.ControlCommandReceipts[event.CommandID] = receipt
		return true, nil

	case events.ControlOperationResultRecorded:
		receipt, receiptExists := s.ControlCommandReceipts[event.CommandID]
		operation, operationExists := s.Operations[event.OperationID]
		if !receiptExists || !operationExists || receipt.OperationID != event.OperationID || receipt.Command != event.Command ||
			(operation.Status != model.OperationStatusSucceeded && operation.Status != model.OperationStatusFailed) || !validFactTime(*s, event.RecordedAt) {
			return true, fmt.Errorf("%w: invalid control operation result", ErrInvalidEvent)
		}
		if s.ControlOperationResults == nil {
			s.ControlOperationResults = make(map[model.OperationID]ControlOperationResultState)
		}
		if _, exists := s.ControlOperationResults[event.OperationID]; exists {
			return true, ErrIdempotencyConflict
		}
		s.ControlOperationResults[event.OperationID] = ControlOperationResultState{OperationID: event.OperationID,
			CommandID: event.CommandID, Command: event.Command, Result: append([]byte(nil), event.Result...),
			ErrorCode: event.ErrorCode, Message: event.Message, RecordedAt: event.RecordedAt}
		return true, nil

	case events.ServerCredentialIssued:
		server, exists := s.Servers[event.ServerID]
		if !exists || event.CredentialID == "" || event.Version == 0 || event.ValidFrom.Before(event.IssuedAt) ||
			!event.ExpiresAt.After(event.ValidFrom) || !validFactTime(*s, event.IssuedAt) {
			return true, fmt.Errorf("%w: invalid server credential", ErrInvalidEvent)
		}
		if s.ServerCredentials == nil {
			s.ServerCredentials = make(map[model.CredentialID]ServerCredentialState)
		}
		if _, duplicate := s.ServerCredentials[event.CredentialID]; duplicate {
			return true, fmt.Errorf("%w: duplicate server credential", ErrInvalidEvent)
		}
		if event.Version > 1 {
			previous, ok := s.ServerCredentials[event.SupersedesCredentialID]
			if !ok || previous.ServerID != event.ServerID || previous.Version+1 != event.Version || server.CredentialID != previous.CredentialID {
				return true, fmt.Errorf("%w: invalid credential rotation", ErrInvalidEvent)
			}
			if event.ValidFrom.Before(previous.ValidFrom) {
				return true, fmt.Errorf("%w: credential rotation predates previous version", ErrInvalidEvent)
			}
			previous.ExpiresAt = event.ValidFrom
			s.ServerCredentials[previous.CredentialID] = previous
		}
		if event.Version == 1 && server.CredentialID != "" && server.CredentialID != event.CredentialID {
			return true, fmt.Errorf("%w: server already references another credential", ErrInvalidEvent)
		}
		s.ServerCredentials[event.CredentialID] = ServerCredentialState{CredentialID: event.CredentialID, ServerID: event.ServerID,
			Version: event.Version, SupersedesCredentialID: event.SupersedesCredentialID, ValidFrom: event.ValidFrom,
			ExpiresAt: event.ExpiresAt, IssuedAt: event.IssuedAt}
		server.CredentialID = event.CredentialID
		s.Servers[event.ServerID] = server
		return true, nil

	case events.ServerCredentialRotationRequested:
		if event.RotationID == "" || event.ServerID == "" || !validFactTime(*s, event.RequestedAt) {
			return true, fmt.Errorf("%w: invalid credential rotation request", ErrInvalidEvent)
		}
		if _, exists := s.CredentialRotationIDs[event.RotationID]; exists {
			return true, fmt.Errorf("%w: duplicate credential rotation request", ErrInvalidEvent)
		}
		if s.CredentialRotationIDs == nil {
			s.CredentialRotationIDs = make(map[string]struct{})
		}
		s.CredentialRotationIDs[event.RotationID] = struct{}{}
		s.markScheduled(event, event.RequestedAt)
		return true, nil
	default:
		return false, nil
	}
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func credentialValidityWindow(now, runEndsAt time.Time) (time.Time, time.Time) {
	expires := now.Add(7 * 24 * time.Hour)
	if runEndsAt.Before(expires) {
		expires = runEndsAt
	}
	if !expires.After(now) {
		expires = now.Add(time.Second)
	}
	return now, expires
}
