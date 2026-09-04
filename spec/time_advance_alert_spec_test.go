package spec_test

import "testing"

func TestAdvanceTimeOpenAPIHasFirstNewLogErrorStopCondition(t *testing.T) {
	document := loadOpenAPI(t)
	schemas := objectField(t, objectField(t, document, "components"), "schemas")

	request := objectField(t, schemas, "AdvanceTimeRequest")
	assertRequiredFields(t, request, []string{"request_id", "duration_seconds"})
	requestProperties := objectField(t, request, "properties")
	if objectField(t, requestProperties, "duration_seconds")["minimum"] != 300 {
		t.Fatal("advance duration must remain a required five-minute-or-longer upper bound")
	}
	stopWhen := objectField(t, schemas, "AdvanceTimeStopCondition")
	assertRequiredFields(t, stopWhen, []string{"new_log_errors"})
	if objectField(t, objectField(t, stopWhen, "properties"), "new_log_errors")["const"] != 1 {
		t.Fatal("the first version must stop on exactly one new log error")
	}

	response := objectField(t, schemas, "AdvanceTimeResponse")
	assertRequiredFields(t, response, []string{
		"clock", "previous_simulation_time", "requested_duration_seconds",
		"processed_events", "new_logs", "stop_reason",
	})
	reasons := arrayField(t, objectField(t, objectField(t, response, "properties"), "stop_reason"), "enum")
	want := []any{"duration_elapsed", "log_error", "run_completed"}
	if len(reasons) != len(want) {
		t.Fatalf("stop reasons = %#v, want %#v", reasons, want)
	}
	for index := range want {
		if reasons[index] != want[index] {
			t.Fatalf("stop reasons = %#v, want %#v", reasons, want)
		}
	}
}
