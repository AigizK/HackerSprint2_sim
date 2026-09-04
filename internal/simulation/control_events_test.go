package simulation

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
)

func TestControlCommandAndCredentialEventsRoundTripThroughCodec(t *testing.T) {
	at := time.Date(2032, 4, 5, 6, 7, 8, 9, time.UTC)
	result := json.RawMessage(`{"server_id":"server-2","deleted":true}`)
	all := []events.Event{
		events.ControlCommandAccepted{CommandID: "request-1", Command: "server.delete", PayloadSHA256: "0123456789abcdef", Params: json.RawMessage(`{"server_id":"server-2"}`), OperationID: "operation-1", AcceptedAt: at},
		events.ControlCommandResponseRecorded{CommandID: "request-1", Command: "server.delete", StatusCode: 202, OperationID: "operation-1", Result: json.RawMessage("null"), RecordedAt: at},
		events.ControlCommandResponseRecorded{CommandID: "request-2", Command: "server.inspect", StatusCode: 200, Result: result, RecordedAt: at},
		events.ControlOperationResultRecorded{OperationID: "operation-1", CommandID: "request-1", Command: "server.delete", Result: result, RecordedAt: at},
		events.ServerCredentialIssued{CredentialID: "server-2-v2", ServerID: "server-2", Version: 2, SupersedesCredentialID: "server-2-v1", ValidFrom: at, ExpiresAt: at.Add(24 * time.Hour), IssuedAt: at},
		events.ServerCredentialRotationRequested{RotationID: "rotate-1", ServerID: "server-2", RequestedAt: at},
		events.DatabaseDeleted{DatabaseID: "db-old", ServerID: "server-old", DeletedAt: at},
		events.DiskLogsGrowthRequested{GrowthID: "logs-1", ServerID: "backend-1", DeltaBytes: 1 << 30, RequestedAt: at},
		events.DiskLogsIncreased{GrowthID: "logs-1", ServerID: "backend-1", BytesAdded: 1 << 30, IncreasedAt: at},
		events.DiskLogsGrowthBlocked{GrowthID: "logs-2", ServerID: "backend-1", RequiredBytes: 1 << 30, FreeBytes: 0, BlockedAt: at},
	}
	for _, original := range all {
		t.Run(original.EventType(), func(t *testing.T) {
			eventType, payload, err := EncodeEvent(original)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeEvent(eventType, payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, original) {
				t.Fatalf("decoded = %#v, want %#v", decoded, original)
			}
		})
	}
}

func TestControlEventsCannotContainAuthenticationSecrets(t *testing.T) {
	for _, value := range []any{
		events.ControlCommandAccepted{},
		events.ControlCommandResponseRecorded{},
		events.ControlOperationResultRecorded{},
		events.ServerCredentialIssued{},
		events.ServerCredentialRotationRequested{},
	} {
		typ := reflect.TypeOf(value)
		for _, forbidden := range []string{"Authorization", "TargetAuth", "Username", "Password", "Secret"} {
			if _, exists := typ.FieldByName(forbidden); exists {
				t.Fatalf("%s exposes forbidden field %s", typ.Name(), forbidden)
			}
		}
	}

	event := events.ServerCredentialIssued{CredentialID: "server-v1", ServerID: "server", Version: 1,
		ValidFrom: time.Unix(1, 0).UTC(), ExpiresAt: time.Unix(2, 0).UTC(), IssuedAt: time.Unix(1, 0).UTC()}
	_, payload, err := EncodeEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, secretField := range [][]byte{[]byte("username"), []byte("password"), []byte("target_auth"), []byte("authorization")} {
		if bytes.Contains(bytes.ToLower(payload), secretField) {
			t.Fatalf("credential metadata event contains secret field %q: %s", secretField, payload)
		}
	}
}

func TestServerProvisioningEventCarriesPublicIdentityReferences(t *testing.T) {
	typ := reflect.TypeOf(events.ServerProvisioningStarted{})
	for _, required := range []string{"Name", "CredentialID"} {
		if _, exists := typ.FieldByName(required); !exists {
			t.Fatalf("ServerProvisioningStarted is missing %s", required)
		}
	}
}
